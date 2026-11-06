package relay

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func TestOpenCodeClientHello(t *testing.T) {
	spec, err := openCodeClientHelloSpec()
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.CipherSuites) != 17 {
		t.Fatalf("cipher suites = %v", spec.CipherSuites)
	}
	var alpn []string
	for _, ext := range spec.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			alpn = a.AlpnProtocols
		}
	}
	if len(alpn) != 1 || alpn[0] != "http/1.1" {
		t.Fatalf("ALPN = %v", alpn)
	}
	tr := openCodeTransport(nil)
	defer tr.CloseIdleConnections()
	if tr.Proxy != nil || tr.ForceAttemptHTTP2 || len(tr.TLSNextProto) != 0 {
		t.Fatal("标准 HTTP CONNECT 或 HTTP/2 会绕开专用握手")
	}
}

func TestOpenCodeDirectTLSVerifiesCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	u, _ := url.Parse(srv.URL)
	if c, err := openCodeDialTLS(ctx, nil, u.Host); err == nil {
		c.Close()
		t.Fatal("未知 CA 的服务端证书被接受")
	}
}

func TestOpenCodeConnectProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "opencode.ai:443" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			t.Errorf("CONNECT 请求错误: %s %s %v", r.Method, r.Host, r.Header)
		}
		conn, _, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err == nil {
			conn.Write(b)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(strings.Replace(srv.URL, "http://", "http://user:pass@", 1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := openCodeDialRaw(ctx, u, "opencode.ai:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("隧道提前关闭: %q %v", b, err)
	}
}

func TestOpenCodeProxyCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	u, _ := url.Parse("http://" + listener.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := openCodeDialRaw(ctx, u, "opencode.ai:443"); done <- err }()
	conn := <-accepted
	defer conn.Close()
	if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("取消后代理握手仍成功")
		}
	case <-time.After(time.Second):
		t.Fatal("CONNECT 读响应没有响应取消")
	}
}

func TestOpenCodeSocksProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		greeting := make([]byte, 3)
		io.ReadFull(c, greeting)
		c.Write([]byte{5, 0})
		header := make([]byte, 5)
		io.ReadFull(c, header)
		if header[3] != 3 {
			t.Error("SOCKS 请求没有远端解析域名")
			return
		}
		dest := make([]byte, int(header[4])+2)
		io.ReadFull(c, dest)
		if string(dest[:len(dest)-2]) != "opencode.ai" || binary.BigEndian.Uint16(dest[len(dest)-2:]) != 443 {
			t.Error("SOCKS 目标错误")
		}
		c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
		b := make([]byte, 4)
		io.ReadFull(c, b)
		c.Write(b)
	}()
	u, _ := url.Parse("socks5h://" + listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := openCodeDialRaw(ctx, u, "opencode.ai:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	c.Write([]byte("ping"))
	b := make([]byte, 4)
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "ping" {
		t.Fatalf("SOCKS 隧道错误: %q %v", b, err)
	}
}

func TestOpenCodeClientCacheKeepsOtherChannelsSeparate(t *testing.T) {
	h := NewHandler(nil)
	client := h.openCodeClientFor("")
	if client != h.openCodeClientFor("") || client == h.clientFor("") {
		t.Fatal("原生客户端缓存不正确")
	}
	if h.Client.Transport.(*http.Transport).TLSClientConfig != nil {
		t.Fatal("普通渠道被修改")
	}
}

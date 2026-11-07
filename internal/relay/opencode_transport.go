package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"
)

// 同机对照中，普通 Go TLS 请求被拒绝，采用以下握手参数的 Go 请求成功。
// 这里的握手参数来自 Bun 1.3.14 的真实 ClientHello（本地抓包，无 key、无请求体），
// 用 uTLS 重放同一份握手；扩展顺序、密码套件顺序、ALPN 都与抓包一致。
//
// 证据：同一份 spec 在直连 DMIT 上拿到 HTTP 200，响应里 billing_source 是 free。
// 参考 docs/research/opencode-zen.md。

// openCodeClientHelloSpec 复刻 Bun 1.3.14 发出的 ClientHello。
func openCodeClientHelloSpec() (*utls.ClientHelloSpec, error) {
	raw, err := base64.StdEncoding.DecodeString(openCodeHello)
	if err != nil {
		return nil, err
	}
	return (&utls.Fingerprinter{}).FingerprintClientHello(raw)
}

// 本地无鉴权 TLS 探针捕获，只含握手参数和公开的临时公钥，不含 API key 或请求。
// 每次连接重新生成密钥、随机数和目标 SNI，不重用抓包里的会话密钥。
const openCodeHello = "FgMBAgABAAH8AwOK8nM2fm+w58soROI8OQAz8VLkspwGn9sjPv9qtD3IYCBDmsT7HThJvfGpX+MmJaCpVhWoqwWY8cbDOU+tJ3VykAAiEwETAhMDwCvAL8AswDDMqcyowAnAE8AKwBQAnACdAC8ANQEAAZEAAAAOAAwAAAlsb2NhbGhvc3QAFwAA/wEAAQAACgAIAAYAHQAXABgACwACAQAAIwAAABAACwAJCGh0dHAvMS4xAAUABQEAAAAAAA0AFAASBAMIBAQBBQMIBQUBCAYGAQIBABIAAAAzACYAJAAdACBIKBBHN8r6DIs2AvM1PX9GPwH8YS9j/QoxfEmdq27AOQAtAAIBAQArAAUEAwQDAwAVAO8AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

// openCodeDialTLS 用 Bun 的指纹完成 TLS 握手。proxy 为空时直连。
func openCodeDialTLS(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	raw, err := openCodeDialRaw(ctx, proxyURL, addr)
	if err != nil {
		return nil, err
	}
	spec, err := openCodeClientHelloSpec()
	if err != nil {
		raw.Close()
		return nil, err
	}
	conn := utls.UClient(raw, &utls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: false,
	}, utls.HelloCustom)
	if err := conn.ApplyPreset(spec); err != nil {
		raw.Close()
		return nil, err
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

// openCodeDialRaw 建立到目标的明文连接：直连、http(s) CONNECT 代理或 socks5 代理。
func openCodeDialRaw(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if proxyURL == nil {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	switch proxyURL.Scheme {
	case "http", "https":
		return dialViaConnectProxy(ctx, dialer, proxyURL, addr)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if proxyURL.User != nil {
			pass, _ := proxyURL.User.Password()
			auth = &proxy.Auth{User: proxyURL.User.Username(), Password: pass}
		}
		socks, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, dialer)
		if err != nil {
			return nil, err
		}
		conn, err := socks.(proxy.ContextDialer).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return conn, nil
	default:
		return nil, fmt.Errorf("代理只支持 socks5、socks5h、http、https，不支持 %q", proxyURL.Scheme)
	}
}

// dialViaConnectProxy 先连代理，再发 CONNECT 请求打通目标。
func dialViaConnectProxy(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, addr string) (net.Conn, error) {
	port := "80"
	if proxyURL.Scheme == "https" {
		port = "443"
	}
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	raw := conn
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		raw.SetDeadline(deadline)
	}
	if proxyURL.Scheme == "https" {
		host, _, err := net.SplitHostPort(proxyAddr)
		if err != nil {
			conn.Close()
			return nil, err
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tlsConn
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: http.Header{},
	}
	if proxyURL.User != nil {
		pass, _ := proxyURL.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+pass)))
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("代理 CONNECT 返回 %d", resp.StatusCode)
	}
	if br.Buffered() > 0 {
		conn.Close()
		return nil, fmt.Errorf("代理 CONNECT 响应后有多余数据")
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

// openCodeTransport 构造走 Bun 指纹的 Transport。只用于 HTTPS 上游；
// 普通 http 上游（如测试 mock）沿用标准 Transport。
func openCodeTransport(proxyURL *url.URL) *http.Transport {
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return openCodeDialTLS(ctx, proxyURL, addr)
		},
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 32,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
}

// openCodeClientFor 按渠道代理缓存；普通 HTTP（如本地测试）沿用已有客户端。
func (h *Handler) openCodeClientFor(proxy string) *http.Client {
	base := h.clientFor(proxy)
	proxy = strings.TrimSpace(proxy)
	p := &h.proxies
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clients == nil {
		p.clients = map[string]*http.Client{}
	}
	key := "opencode:" + proxy
	if c := p.clients[key]; c != nil {
		return c
	}
	c := &http.Client{Transport: &openCodeRoundTripper{base: base.Transport, proxy: proxy}, Timeout: h.Client.Timeout}
	p.clients[key] = c
	return c
}

type openCodeRoundTripper struct {
	base       http.RoundTripper
	proxy      string
	mu         sync.Mutex
	transports map[string]*http.Transport
}

func (t *openCodeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return t.base.RoundTrip(req)
	}
	var proxyURL *url.URL
	var err error
	if t.proxy != "" {
		proxyURL, err = url.Parse(t.proxy)
	} else {
		proxyURL, err = http.ProxyFromEnvironment(req)
	}
	if err != nil {
		return nil, err
	}
	key := ""
	if proxyURL != nil {
		key = proxyURL.String()
	}
	t.mu.Lock()
	if t.transports == nil {
		t.transports = map[string]*http.Transport{}
	}
	tr := t.transports[key]
	if tr == nil {
		tr = openCodeTransport(proxyURL)
		t.transports[key] = tr
	}
	t.mu.Unlock()
	return tr.RoundTrip(req)
}

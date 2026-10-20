package relay

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// 渠道级代理：只有填了代理的渠道走代理（例如 OpenRouter 在中国被按地区拒绝，要经美国出口），
// 其他渠道直连。请求转发、冒烟测试、拉取模型、查余额都用同一个渠道的代理。

// ValidateProxyURL 检查代理地址，空表示直连。支持 socks5 / socks5h / http / https。
func ValidateProxyURL(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return fmt.Errorf("代理地址 %q 格式不对，例如 socks5://127.0.0.1:10808", s)
	}
	switch u.Scheme {
	case "socks5", "socks5h", "http", "https":
		return nil
	}
	return fmt.Errorf("代理只支持 socks5、socks5h、http、https，不支持 %q", u.Scheme)
}

type proxyClients struct {
	mu      sync.Mutex
	clients map[string]*http.Client
}

// clientFor 返回走这个代理的 HTTP 客户端（按代理地址缓存，复用连接）；空地址返回默认客户端。
func (h *Handler) clientFor(proxy string) *http.Client {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return h.Client
	}
	p := &h.proxies
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.clients[proxy]; c != nil {
		return c
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return h.Client // 保存时已校验过，走不到这里
	}
	c := &http.Client{Transport: newTransport(http.ProxyURL(u)), Timeout: h.Client.Timeout}
	if p.clients == nil {
		p.clients = map[string]*http.Client{}
	}
	p.clients[proxy] = c
	return c
}

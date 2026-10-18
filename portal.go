package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const probeURL = "http://www.msftconnecttest.com/connecttest.txt"

type probeResult struct {
	online   bool
	redirect string
	reason   string
}

type portalClient struct {
	client     *http.Client
	cfg        config
	mu         sync.RWMutex
	sourceIP   net.IP
	diagnostic *diagnostics
}

func newPortalClient(cfg config) *portalClient {
	p := &portalClient{cfg: cfg}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		p.mu.RLock()
		ip := p.sourceIP
		p.mu.RUnlock()
		if ip != nil {
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
		}
		started := time.Now()
		conn, err := dialer.DialContext(ctx, "tcp4", address)
		if p.diagnostic != nil {
			local, remote := "", ""
			if conn != nil {
				local, remote = conn.LocalAddr().String(), conn.RemoteAddr().String()
			}
			p.diagnostic.log.Printf("portal dial target=%q source=%q local=%q remote=%q elapsed=%s error=%q", address, ip.String(), local, remote, time.Since(started), diagnosticError(err))
		}
		return conn, err
	}
	p.client = &http.Client{
		Timeout: 6 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return p
}

// All HTTP operations run on the controller goroutine. Binding the source
// address makes the selected Ethernet/WLAN priority effective for probes too.
func (p *portalClient) setSourceIP(address string) {
	p.mu.Lock()
	p.sourceIP = net.ParseIP(address).To4()
	p.mu.Unlock()
}

func (p *portalClient) probe(ctx context.Context) (result probeResult) {
	if p.diagnostic != nil {
		started := time.Now()
		defer func() {
			p.diagnostic.log.Printf("bound probe result online=%t redirect=%q reason=%q elapsed=%s", result.online, safePortal(result.redirect), result.reason, time.Since(started))
		}()
	}
	// Check the HTTP captive probe and independent HTTPS probe in parallel.
	// A single slow DNS/TCP timeout must not double the outage detection time.
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		primary  bool
		online   bool
		redirect string
		err      error
	}
	answers := make(chan answer, 2)
	check := func(primary bool, target string) {
		a := answer{primary: primary}
		request, requestErr := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
		if requestErr != nil {
			a.err = requestErr
			answers <- a
			return
		}
		resp, requestErr := p.doDiagnostic(request, "bound-probe")
		if requestErr != nil {
			a.err = requestErr
			answers <- a
			return
		}
		defer resp.Body.Close()
		if primary {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				if loc, locErr := resp.Location(); locErr == nil {
					a.redirect = loc.String()
				}
			}
			if resp.StatusCode == http.StatusOK {
				data, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
				a.online = readErr == nil && strings.TrimSpace(string(data)) == "Microsoft Connect Test"
			}
		} else {
			a.online = resp.StatusCode >= 200 && resp.StatusCode < 300
		}
		answers <- a
	}
	go check(true, probeURL)
	go check(false, "https://www.baidu.com/")
	var redirect string
	var primaryErr error
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			return probeResult{reason: "外网检测已取消"}
		case a := <-answers:
			if a.online {
				return probeResult{online: true}
			}
			if a.primary {
				redirect, primaryErr = a.redirect, a.err
			}
		}
	}
	if redirect != "" {
		return probeResult{redirect: redirect, reason: "被重定向到校园网认证页"}
	}
	var dnsErr *net.DNSError
	if errors.As(primaryErr, &dnsErr) {
		return probeResult{reason: "DNS 无法解析外网检测域名"}
	}
	if primaryErr != nil {
		return probeResult{reason: "外网检测地址不可达"}
	}
	return probeResult{reason: "外网检测地址返回非预期内容"}
}

var portalRedirectPattern = regexp.MustCompile(`(?i)(?:location\.href|window\.location|url)\s*[=:]\s*['"](https?://[^'"<> ]+)`)

var errNoAuthenticationEvidence = errors.New("本轮未收到可信校园网认证跳转，不能仅凭探测失败、旧入口或默认入口登录")

// Only this round's trusted redirects establish a need to authenticate. A
// cached/configured address is a destination, not evidence of lost login.
// Discovery can take seconds: always probe again after it finishes.
func (p *portalClient) prepareAuthentication(ctx context.Context, initial probeResult) (probeResult, error) {
	if err := ctx.Err(); err != nil {
		return probeResult{}, err
	}
	trusted := func(raw string) string {
		kind, err := classifyPortal(raw)
		if err == nil && kind != portalUnknown {
			return raw
		}
		return ""
	}
	portal := trusted(initial.redirect)
	if portal == "" {
		found, _ := p.discover(ctx)
		portal = trusted(found)
	}
	if err := ctx.Err(); err != nil {
		return probeResult{}, err
	}
	fresh := p.probe(ctx)
	if err := ctx.Err(); err != nil {
		return probeResult{}, err
	}
	if fresh.online {
		if p.diagnostic != nil {
			p.diagnostic.log.Print("authentication gate: external access recovered; LOGIN SUPPRESSED")
		}
		return fresh, nil
	}
	if redirect := trusted(fresh.redirect); redirect != "" {
		portal = redirect
	}
	if portal == "" {
		if p.diagnostic != nil {
			p.diagnostic.log.Print("authentication gate: no current trusted redirect; LOGIN SUPPRESSED")
		}
		return probeResult{}, errNoAuthenticationEvidence
	}
	if p.diagnostic != nil {
		p.diagnostic.log.Printf("authentication gate: fresh probe failed and current trusted redirect=%q", safePortal(portal))
	}
	return probeResult{redirect: portal, reason: fresh.reason}, nil
}

func (p *portalClient) discover(ctx context.Context) (string, error) {
	for _, target := range []string{"http://9.9.9.9/", "http://neverssl.com/"} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		resp, err := p.doDiagnostic(request, "discover")
		if err != nil {
			continue
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc, locErr := resp.Location()
			resp.Body.Close()
			if locErr == nil {
				return loc.String(), nil
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if match := portalRedirectPattern.FindSubmatch(body); match != nil {
			return string(match[1]), nil
		}
	}
	return "", errors.New("未收到校园网认证跳转")
}

func (p *portalClient) login(ctx context.Context, site campus, portal, currentIP, currentMAC string) error {
	if site != campusWenchang && site != campusNanhu {
		return errors.New("校区未识别，不能发送账号密码")
	}
	if _, err := classifyPortal(portal); err != nil {
		return err
	}
	// Both campuses use this backend. The a79 page is shared as well.
	// Never derive a credential-bearing destination from an arbitrary redirect.
	base, _ := url.Parse("http://10.2.5.251:801/eportal/")
	q := url.Values{}
	if portal != "" {
		u, _ := url.Parse(portal)
		q = u.Query()
	}
	ip := currentIP
	if ip == "" {
		for _, key := range []string{"wlanuserip", "wlan_user_ip", "userip"} {
			if net.ParseIP(q.Get(key)) != nil {
				ip = q.Get(key)
				break
			}
		}
	}
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
		return errors.New("没有可用的校园网 IPv4 地址")
	}
	account := p.cfg.accountFor(site)
	mac := strings.NewReplacer("-", "", ":", "").Replace(q.Get("mac"))
	if len(mac) != 12 {
		mac = strings.NewReplacer("-", "", ":", "").Replace(currentMAC)
	}
	if len(mac) != 12 {
		mac = "000000000000"
	}
	params := url.Values{
		"c": {"Portal"}, "a": {"login"}, "login_method": {"1"},
		"user_account": {account}, "user_password": {p.cfg.password},
		"wlan_user_ip": {ip}, "wlan_user_mac": {mac},
		"jsVersion": {"3.0"},
	}
	if q.Get("wlanacname") != "" || q.Get("wlanacip") != "" {
		params.Set("wlan_ac_name", q.Get("wlanacname"))
		params.Set("wlan_ac_ip", q.Get("wlanacip"))
	}
	stamp := time.Now().UnixMilli()
	params.Set("callback", fmt.Sprintf("dr%d", stamp))
	params.Set("_", fmt.Sprint(stamp))
	base.RawQuery = params.Encode()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	resp, err := p.doDiagnostic(request, "login")
	if err != nil {
		return errors.New("认证接口不可达或请求超时")
	} // Never log the URL containing credentials.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("认证接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return errors.New("读取认证响应失败")
	}
	start, end := strings.IndexByte(string(body), '{'), strings.LastIndexByte(string(body), '}')
	if start < 0 || end < start {
		return errors.New("认证响应不是可识别的 JSONP")
	}
	var result map[string]any
	if err := json.Unmarshal(body[start:end+1], &result); err != nil {
		return errors.New("认证响应 JSON 解析失败")
	}
	if fmt.Sprint(result["result"]) == "1" || fmt.Sprint(result["result"]) == "ok" {
		return nil
	}
	if fmt.Sprint(result["ret_code"]) == "2" {
		return nil
	} // "IP already online"; external probe remains authoritative.
	msg := fmt.Sprint(result["msg"])
	if msg == "" || msg == "<nil>" {
		msg = fmt.Sprint(result["message"])
	}
	if msg != "" && msg != "<nil>" {
		return fmt.Errorf("认证未获成功响应（result=%v，ret_code=%v，msg=%s）", result["result"], result["ret_code"], msg)
	}
	return fmt.Errorf("认证未获成功响应（result=%v，ret_code=%v）", result["result"], result["ret_code"])
}

func safePortal(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

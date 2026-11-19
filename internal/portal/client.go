package portal

import (
	"context"
	"encoding/base64"
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

	"wenchang-campus-keeper/internal/campus"
	"wenchang-campus-keeper/internal/config"
	"wenchang-campus-keeper/internal/diagnostics"
)

type ProbeResult struct {
	Online   bool
	Redirect string
	Reason   string
}

type Client struct {
	client     *http.Client
	cfg        config.Config
	mu         sync.RWMutex
	sourceIP   net.IP
	diagnostic *diagnostics.Diagnostics
}

func NewClient(cfg config.Config) *Client {
	p := &Client{cfg: cfg}
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
			p.diagnostic.Log.Printf("portal dial target=%q source=%q local=%q remote=%q elapsed=%s error=%q", address, ip.String(), local, remote, time.Since(started), diagnostics.DiagnosticError(err))
		}
		return conn, err
	}
	p.client = &http.Client{
		Timeout: 6 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return p
}

func (p *Client) SetSourceIP(address string) {
	p.mu.Lock()
	p.sourceIP = net.ParseIP(address).To4()
	p.mu.Unlock()
}

func (p *Client) SetDiagnostic(d *diagnostics.Diagnostics) {
	p.mu.Lock()
	p.diagnostic = d
	p.mu.Unlock()
}

func (p *Client) Diagnostic() *diagnostics.Diagnostics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.diagnostic
}

func (p *Client) SetConfig(cfg config.Config) {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}

func (p *Client) SetHTTPClient(client *http.Client) {
	p.client = client
}

func (p *Client) doDiagnostic(r *http.Request, label string) (*http.Response, error) {
	p.mu.RLock()
	d := p.diagnostic
	p.mu.RUnlock()
	if d == nil {
		return p.client.Do(r)
	}
	return d.Request(p.client, r, label)
}

func (p *Client) Probe(ctx context.Context) (result ProbeResult) {
	p.mu.RLock()
	diag := p.diagnostic
	p.mu.RUnlock()
	if diag != nil {
		started := time.Now()
		defer func() {
			diag.Log.Printf("bound probe result online=%t redirect=%q reason=%q elapsed=%s", result.Online, SafePortal(result.Redirect), result.Reason, time.Since(started))
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
				a.online = readErr == nil && strings.TrimSpace(string(data)) == PrimaryProbeExpectedContent
			}
		} else {
			a.online = resp.StatusCode >= 200 && resp.StatusCode < 300
		}
		answers <- a
	}
	go check(true, PrimaryProbeURL)
	go check(false, SecondaryProbeURL)
	var redirect string
	var primaryErr error
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			return ProbeResult{Reason: "外网检测已取消"}
		case a := <-answers:
			if a.online {
				return ProbeResult{Online: true}
			}
			if a.primary {
				redirect, primaryErr = a.redirect, a.err
			}
		}
	}
	if redirect != "" {
		return ProbeResult{Redirect: redirect, Reason: "被重定向到校园网认证页"}
	}
	var dnsErr *net.DNSError
	if errors.As(primaryErr, &dnsErr) {
		return ProbeResult{Reason: "DNS 无法解析外网检测域名"}
	}
	if primaryErr != nil {
		return ProbeResult{Reason: "外网检测地址不可达"}
	}
	return ProbeResult{Reason: "外网检测地址返回非预期内容"}
}

var portalRedirectPattern = regexp.MustCompile(`(?i)(?:location\.href|window\.location|url)\s*[=:]\s*['"](https?://[^'"<> ]+)`)

var ErrNoAuthenticationEvidence = errors.New("本轮未收到可信校园网认证跳转，不能仅凭探测失败、旧入口或默认入口登录")

// Only this round's trusted redirects establish a need to authenticate. A
// cached/configured address is a destination, not evidence of lost login.
// Discovery can take seconds: always probe again after it finishes.
func (p *Client) PrepareAuthentication(ctx context.Context, initial ProbeResult) (ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	p.mu.RLock()
	diag := p.diagnostic
	p.mu.RUnlock()
	trusted := func(raw string) string {
		kind, err := campus.ClassifyPortal(raw)
		if err == nil && kind != campus.PortalUnknown {
			return raw
		}
		return ""
	}
	portal := trusted(initial.Redirect)
	if portal == "" {
		found, _ := p.discover(ctx)
		portal = trusted(found)
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	fresh := p.Probe(ctx)
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	if fresh.Online {
		if diag != nil {
			diag.Log.Print("authentication gate: external access recovered; LOGIN SUPPRESSED")
		}
		return fresh, nil
	}
	if redirect := trusted(fresh.Redirect); redirect != "" {
		portal = redirect
	}
	if portal == "" {
		if direct := p.probeDirectPortal(ctx); direct != "" {
			portal = trusted(direct)
		}
	}
	if portal == "" {
		if diag != nil {
			diag.Log.Print("authentication gate: no current trusted redirect; LOGIN SUPPRESSED")
		}
		return ProbeResult{}, ErrNoAuthenticationEvidence
	}
	if diag != nil {
		diag.Log.Printf("authentication gate: fresh probe failed and current trusted redirect=%q", SafePortal(portal))
	}
	return ProbeResult{Redirect: portal, Reason: fresh.Reason}, nil
}

func (p *Client) discover(ctx context.Context) (string, error) {
	for _, target := range RedirectDiscoveryTargets {
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

// probeDirectPortal checks whether the campus portal host directly returns a valid portal login page.
// This is critical when external DNS is blocked prior to authentication, making public redirect targets unreachable.
func (p *Client) probeDirectPortal(ctx context.Context) string {
	directURL := "http://" + campus.AuthHost + campus.PathFrontPage
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directURL, nil)
	if err != nil {
		return ""
	}
	resp, err := p.doDiagnostic(req, "direct_portal")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc, locErr := resp.Location(); locErr == nil {
			return req.URL.ResolveReference(loc).String()
		}
		if rawLoc := resp.Header.Get("Location"); rawLoc != "" {
			return rawLoc
		}
		return directURL
	}
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		if match := portalRedirectPattern.FindSubmatch(body); match != nil {
			return string(match[1])
		}
		bodyStr := string(body)
		if strings.Contains(bodyStr, "Dr.COM") || strings.Contains(bodyStr, "a79.htm") || strings.Contains(bodyStr, "eportal") || strings.Contains(bodyStr, "校园网") {
			return directURL
		}
	}
	return ""
}

func (p *Client) buildPortalActionURL(action string, site campus.Campus, portal, currentIP, currentMAC string) (string, error) {
	return p.buildPortalActionURLWithMAC(action, site, portal, currentIP, currentMAC, site == campus.Wenchang || site == campus.ContinuingEdu)
}

func (p *Client) buildPortalActionURLWithMAC(action string, site campus.Campus, portal, currentIP, currentMAC string, useInterfaceMAC bool) (string, error) {
	if !campus.IsKnownCampus(site) {
		return "", errors.New("校区未识别，不能发送账号密码")
	}
	if _, err := campus.ClassifyPortal(portal); err != nil {
		return "", err
	}
	p.mu.RLock()
	cfg := p.cfg
	p.mu.RUnlock()
	// All configured campuses share this backend. The a79 page is shared as well.
	// Never derive a credential-bearing destination from an arbitrary redirect.
	base, _ := url.Parse(DefaultBackendURL)
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
		return "", errors.New("没有可用的校园网 IPv4 地址")
	}
	account := cfg.AccountFor(site)
	password := cfg.Password
	if action == "logout" && strings.TrimSpace(cfg.Account) == "" {
		// Matches official portal web JS (a42.js logout_portal) when no self account is configured.
		account = "drcom"
		password = "123"
	}
	mac := "000000000000"
	if raw := strings.NewReplacer("-", "", ":", "").Replace(q.Get("mac")); len(raw) == 12 {
		mac = raw
	} else if useInterfaceMAC {
		if raw := strings.NewReplacer("-", "", ":", "").Replace(currentMAC); len(raw) == 12 {
			mac = raw
		}
	}
	stamp := time.Now().UnixMilli()
	queryParts := []string{
		"c=Portal",
		"a=" + action,
		fmt.Sprintf("callback=dr%d", stamp),
		"login_method=1",
		"user_account=" + account,
		"user_password=" + url.QueryEscape(password),
		"wlan_user_ip=" + ip,
		"wlan_user_mac=" + mac,
	}
	if site == campus.Wenchang || site == campus.ContinuingEdu {
		if q.Get("wlanacname") != "" {
			queryParts = append(queryParts, "wlan_ac_name="+url.QueryEscape(q.Get("wlanacname")))
		}
		if q.Get("wlanacip") != "" {
			queryParts = append(queryParts, "wlan_ac_ip="+url.QueryEscape(q.Get("wlanacip")))
		}
	}
	queryParts = append(queryParts, "jsVersion=3.0", fmt.Sprintf("_=%d", stamp))
	base.RawQuery = strings.Join(queryParts, "&")
	return base.String(), nil
}

func (p *Client) doPortalAction(ctx context.Context, actionURL, diagKind, actionName string, allowAlreadyOnline bool) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, actionURL, nil)
	resp, err := p.doDiagnostic(request, diagKind)
	if err != nil {
		return fmt.Errorf("%s接口不可达或请求超时", actionName)
	} // Never log the URL containing credentials.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s接口返回 HTTP %d", actionName, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return fmt.Errorf("读取%s响应失败", actionName)
	}
	return parsePortalResult(body, actionName, allowAlreadyOnline)
}

func parsePortalResult(body []byte, actionName string, allowAlreadyOnline bool) error {
	start, end := strings.IndexByte(string(body), '{'), strings.LastIndexByte(string(body), '}')
	if start < 0 || end < start {
		return fmt.Errorf("%s响应不是可识别的 JSONP", actionName)
	}
	var result map[string]any
	if err := json.Unmarshal(body[start:end+1], &result); err != nil {
		return fmt.Errorf("%s响应 JSON 解析失败", actionName)
	}
	if fmt.Sprint(result["result"]) == "1" || fmt.Sprint(result["result"]) == "ok" {
		return nil
	}
	if allowAlreadyOnline && fmt.Sprint(result["ret_code"]) == "2" {
		return nil
	} // "IP already online"; external probe remains authoritative.
	msg := fmt.Sprint(result["msg"])
	if msg == "" || msg == "<nil>" {
		msg = fmt.Sprint(result["message"])
	}
	if msg != "" && msg != "<nil>" {
		return fmt.Errorf("%s未获成功响应（result=%v，ret_code=%v，msg=%s）", actionName, result["result"], result["ret_code"], decodePortalMessage(msg))
	}
	return fmt.Errorf("%s未获成功响应（result=%v，ret_code=%v）", actionName, result["result"], result["ret_code"])
}

// Logout terminates the active campus portal session for the current IP.
// It checks the JSONP result payload and falls back to interface MAC and Dr.COM /drcom/logout when needed.
func (p *Client) Logout(ctx context.Context, site campus.Campus, portal, currentIP, currentMAC string) error {
	actionURL, err := p.buildPortalActionURL("logout", site, portal, currentIP, currentMAC)
	if err != nil {
		return err
	}
	err = p.doPortalAction(ctx, actionURL, "logout", "注销", false)
	if err == nil {
		return nil
	}
	// If Nanhu failed with zeroed MAC and we have a real interface MAC, retry once with interface MAC.
	normalizedMAC := strings.NewReplacer("-", "", ":", "").Replace(currentMAC)
	if site == campus.Nanhu && len(normalizedMAC) == 12 {
		if altURL, altErr := p.buildPortalActionURLWithMAC("logout", site, portal, currentIP, currentMAC, true); altErr == nil && altURL != actionURL {
			if retryErr := p.doPortalAction(ctx, altURL, "logout", "注销", false); retryErr == nil {
				return nil
			}
		}
	}
	// Fallback to Dr.COM port-80 local IP logout (used by official a42.js when loginMethod == 0).
	stamp := time.Now().UnixMilli()
	drcomURL := fmt.Sprintf("%s?callback=dr%d&_=%d", DefaultDrcomLogoutURL, stamp, stamp)
	if drcomErr := p.doPortalAction(ctx, drcomURL, "logout-drcom", "注销", false); drcomErr == nil {
		return nil
	}
	return err
}

func (p *Client) Login(ctx context.Context, site campus.Campus, portal, currentIP, currentMAC string) error {
	actionURL, err := p.buildPortalActionURL("login", site, portal, currentIP, currentMAC)
	if err != nil {
		return err
	}
	return p.doPortalAction(ctx, actionURL, "login", "认证", true)
}

func decodePortalMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "<nil>" {
		return ""
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) > 0 {
		str := strings.TrimSpace(string(decoded))
		printable := true
		for _, r := range str {
			if r < 32 && r != '\n' && r != '\r' && r != '\t' {
				printable = false
				break
			}
		}
		if printable && str != "" {
			switch {
			case strings.Contains(str, "userid error1"):
				return fmt.Sprintf("%s (%s: 账号不存在或当前校区/运营商不匹配)", raw, str)
			case strings.Contains(str, "userid error2"):
				return fmt.Sprintf("%s (%s: 账号已停机或欠费)", raw, str)
			case strings.Contains(str, "password error"):
				return fmt.Sprintf("%s (%s: 密码错误)", raw, str)
			case strings.Contains(str, "ErrCode=16"):
				return fmt.Sprintf("%s (%s: 认证被服务器拒绝，通常为夜间宿舍定时断网/熄灯管制、账号并发数超限或临时抑制)", raw, str)
			default:
				return fmt.Sprintf("%s (%s)", raw, str)
			}
		}
	}
	return raw
}

func SafePortal(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

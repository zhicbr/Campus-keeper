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
	"time"
)

const probeURL = "http://www.msftconnecttest.com/connecttest.txt"

type probeResult struct {
	online   bool
	redirect string
	reason   string
}

type portalClient struct {
	client *http.Client
	cfg    config
}

func newPortalClient(cfg config) *portalClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", address)
	}
	return &portalClient{cfg: cfg, client: &http.Client{
		Timeout: 6 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *portalClient) probe(ctx context.Context) probeResult {
	redirect := ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return probeResult{reason: "检测请求配置错误"}
	}
	resp, err := p.client.Do(request)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			if loc, locErr := resp.Location(); locErr == nil {
				redirect = loc.String()
			}
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 128))
		if readErr == nil && resp.StatusCode == http.StatusOK && strings.TrimSpace(string(data)) == "Microsoft Connect Test" {
			return probeResult{online: true}
		}
	}
	// An independent HTTPS check avoids declaring an outage because a single
	// connectivity-test service or its DNS record is temporarily unavailable.
	request, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://www.baidu.com/", nil)
	backup, backupErr := p.client.Do(request)
	if backupErr == nil {
		backup.Body.Close()
		if backup.StatusCode >= 200 && backup.StatusCode < 400 {
			return probeResult{online: true}
		}
	}
	if redirect != "" {
		return probeResult{redirect: redirect, reason: "被重定向到校园网认证页"}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return probeResult{reason: "DNS 无法解析外网检测域名"}
	}
	if err != nil {
		return probeResult{reason: "外网检测地址不可达"}
	}
	return probeResult{reason: "外网检测地址返回非预期内容"}
}

var portalRedirectPattern = regexp.MustCompile(`(?i)(?:location\.href|window\.location|url)\s*[=:]\s*['"](https?://[^'"<> ]+)`)

func (p *portalClient) discover(ctx context.Context) (string, error) {
	for _, target := range []string{"http://9.9.9.9/", "http://neverssl.com/"} {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		resp, err := p.client.Do(request)
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

func (p *portalClient) login(ctx context.Context, site campus, portal, currentIP string) error {
	if site != campusWenchang && site != campusNanhu {
		return errors.New("校区未识别，不能发送账号密码")
	}
	if _, err := classifyPortal(portal); err != nil {
		return err
	}
	// The older Nanhu script and verified Wenchang flow both use this backend.
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
	params := url.Values{
		"c": {"Portal"}, "a": {"login"}, "login_method": {"1"},
		"user_account": {account}, "user_password": {p.cfg.password},
		"wlan_user_ip": {ip}, "wlan_user_mac": {"000000000000"},
		"jsVersion": {"3.0"},
	}
	if site == campusWenchang {
		mac := strings.NewReplacer("-", "", ":", "").Replace(q.Get("mac"))
		if len(mac) == 12 {
			params.Set("wlan_user_mac", mac)
		}
		params.Set("wlan_ac_name", q.Get("wlanacname"))
		params.Set("wlan_ac_ip", q.Get("wlanacip"))
	}
	stamp := time.Now().UnixMilli()
	params.Set("callback", fmt.Sprintf("dr%d", stamp))
	params.Set("_", fmt.Sprint(stamp))
	base.RawQuery = params.Encode()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	resp, err := p.client.Do(request)
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
	return fmt.Errorf("认证未获成功响应（result=%v，ret_code=%v）", result["result"], result["ret_code"])
}

func safePortal(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

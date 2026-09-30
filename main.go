package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	checkInterval = 5 * time.Second
	maxBackoff    = 5 * time.Minute
	probeURL      = "http://www.msftconnecttest.com/connecttest.txt"
)

type config struct {
	account        string
	password       string
	portalURL      string
	suffixExplicit bool
}

type keeper struct {
	client     *http.Client
	cfg        config
	state      string
	lastPortal string
	checkCount uint64
}

var errLinkDown = errors.New("有线网络接口未就绪")

func main() {
	once := flag.Bool("once", false, "check and attempt login once, then exit")
	flag.Parse()
	log.SetFlags(log.Ldate | log.Ltime)
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := loadConfig(filepath.Join(filepath.Dir(exe), ".env"))
	if err != nil {
		log.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", addr)
	}
	k := keeper{cfg: cfg, client: &http.Client{
		Timeout:       8 * time.Second,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	logPath := filepath.Join(filepath.Dir(exe), "keeper.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > 5<<20 {
		_ = os.Rename(logPath, logPath+".old")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.Printf("校园网守护进程启动，账号 %s，检测间隔 %s", mask(cfg.account), checkInterval)
	backoff := 5 * time.Second
	for {
		if err := k.step(); err != nil {
			if errors.Is(err, errLinkDown) {
				if *once {
					os.Exit(1)
				}
				backoff = 5 * time.Second
				time.Sleep(checkInterval)
				continue
			}
			log.Printf("连接未恢复：%v；%s 后重试", err, backoff)
			if *once {
				os.Exit(1)
			}
			if k.waitForRetry(jitter(backoff)) {
				backoff = 5 * time.Second
				continue
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = 5 * time.Second
		if *once {
			return
		}
		time.Sleep(checkInterval)
	}
}

func (k *keeper) step() error {
	k.checkCount++
	log.Printf("第 %d 次检测：正在检查 IPv4 外网", k.checkCount)
	ok, redirect, err := k.online()
	if ok {
		if k.state != "online" && k.state != "" {
			log.Print("当前状态：IPv4 外网已恢复")
		} else {
			log.Printf("当前状态：IPv4 外网正常；约 %s 后再次检测", checkInterval)
		}
		k.state = "online"
		return nil
	}
	log.Printf("当前状态：外网探测失败（第 1 次）：%s；2 秒后复核", probeReason(redirect, err))
	// A second failed check avoids starting authentication for a single lost packet.
	time.Sleep(2 * time.Second)
	ok, redirect2, err2 := k.online()
	if ok {
		log.Print("当前状态：短暂异常后 IPv4 外网已恢复")
		k.state = "online"
		return nil
	}
	if redirect2 != "" {
		redirect = redirect2
	}
	wasLinkDown := k.state == "link-down"
	log.Printf("当前状态：IPv4 外网中断（连续 2 次探测失败）：%s", probeReason(redirect, err2))
	k.state = "offline"
	if redirect != "" {
		k.lastPortal = redirect
	}
	portal := redirect
	if k.cfg.portalURL != "" {
		portal = k.cfg.portalURL
	}
	if portal == "" {
		portal = k.lastPortal
	}
	if !wiredLinkReady(portal) {
		log.Printf("当前状态：有线网卡或网线未就绪；约 %s 后再次检测，暂不发送认证请求", checkInterval)
		k.state = "link-down"
		return errLinkDown
	}
	if wasLinkDown {
		log.Print("有线接口已恢复，继续检查认证状态")
		k.state = "offline"
	}
	if portal == "" {
		portal, err = k.discoverPortal()
		if err != nil {
			return errors.New("有线接口已连接，但尚未发现认证入口")
		}
		k.lastPortal = portal
	}
	log.Printf("准备认证：%s", safePortal(portal))
	if err := k.login(portal); err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		time.Sleep(3 * time.Second)
		ok, _, err = k.online()
		if ok {
			log.Print("登录后确认外网恢复")
			k.state = "online"
			return nil
		}
	}
	return errors.New("认证接口已响应，但 15 秒后 IPv4 外网仍不可用")
}

func (k *keeper) waitForRetry(delay time.Duration) bool {
	deadline := time.Now().Add(delay)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		pause := min(remaining, checkInterval)
		log.Printf("当前状态：外网未恢复；认证退避剩余约 %s，%s 后复查状态", remaining.Round(time.Second), pause.Round(time.Second))
		time.Sleep(pause)
		if !wiredLinkReady(k.lastPortal) {
			log.Print("当前状态：有线网卡或网线未就绪，继续等待")
			continue
		}
		log.Print("退避期间正在检查 IPv4 外网")
		ok, _, _ := k.online()
		if ok {
			log.Print("当前状态：IPv4 外网已恢复")
			k.state = "online"
			return true
		}
		log.Print("当前状态：IPv4 外网仍中断，等待下次认证尝试")
	}
}

func (k *keeper) online() (bool, string, error) {
	resp, err := k.client.Get(probeURL)
	if err != nil {
		if k.httpsOnline() {
			return true, "", nil
		}
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc, err := resp.Location()
		if err == nil {
			return false, loc.String(), nil
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return false, "", err
	}
	if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(data)) == "Microsoft Connect Test" {
		return true, "", nil
	}
	if k.httpsOnline() {
		return true, "", nil
	}
	return false, "", fmt.Errorf("检测地址返回 HTTP %d，内容不是预期值", resp.StatusCode)
}

func (k *keeper) httpsOnline() bool {
	resp, err := k.client.Get("https://www.baidu.com/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func probeReason(redirect string, err error) string {
	if redirect != "" {
		return "HTTP 请求被重定向到校园网认证页"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS 无法解析检测域名"
	}
	if err != nil {
		return "检测地址暂时不可达"
	}
	return "检测地址返回了非预期内容"
}

func wiredLinkReady(portal string) bool {
	expected := ""
	if u, err := url.Parse(portal); err == nil {
		q := u.Query()
		expected = q.Get("wlanuserip")
		if expected == "" {
			expected = q.Get("wlan_user_ip")
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return true
	} // Do not block authentication if Windows cannot report interfaces.
	anyCampusIP := false
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			if expected != "" && ip.String() == expected {
				return iface.Flags&net.FlagUp != 0
			}
			if iface.Flags&net.FlagUp != 0 && ip.To4()[0] == 10 {
				anyCampusIP = true
			}
		}
	}
	return anyCampusIP
}

func (k *keeper) discoverPortal() (string, error) {
	for _, target := range []string{"http://9.9.9.9/", "http://neverssl.com/"} {
		resp, err := k.client.Get(target)
		if err != nil {
			continue
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc, err := resp.Location()
			resp.Body.Close()
			if err == nil {
				return loc.String(), nil
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if m := regexp.MustCompile(`(?i)(?:location\.href|window\.location|url)\s*[=:]\s*['"](https?://[^'"<> ]+)`).FindSubmatch(body); m != nil {
			return string(m[1]), nil
		}
	}
	return "", errors.New("无法从断网跳转发现认证入口；请提供登录页地址并写入 PORTAL_URL")
}

func (k *keeper) login(portal string) error {
	u, err := url.Parse(portal)
	if err != nil || u.Host == "" {
		return errors.New("认证入口 URL 无效")
	}
	isEportal := strings.Contains(strings.ToLower(u.Path), "eportal")
	isA79 := strings.EqualFold(u.Path, "/a79.htm")
	if !isEportal && !isA79 {
		return fmt.Errorf("发现的认证页面 %s 不是已知协议；需要根据实际页面补充适配", safePortal(portal))
	}
	q := u.Query()
	ip := ""
	for _, key := range []string{"wlan_user_ip", "wlanuserip", "userip", "user_ip", "ip"} {
		if v := q.Get(key); net.ParseIP(v) != nil {
			ip = v
			break
		}
	}
	if ip == "" {
		conn, err := net.DialTimeout("udp4", net.JoinHostPort(u.Hostname(), "80"), 3*time.Second)
		if err != nil {
			return fmt.Errorf("获取本机有线 IP 失败：%w", err)
		}
		ip = strings.Split(conn.LocalAddr().String(), ":")[0]
		conn.Close()
	}
	base := *u
	if isA79 {
		base.Host = net.JoinHostPort(u.Hostname(), "801")
		base.Path = "/eportal/"
	} else {
		idx := strings.Index(strings.ToLower(u.Path), "/eportal")
		if idx < 0 {
			return errors.New("认证入口不包含 eportal 路径")
		}
		base.Path = u.Path[:idx] + "/eportal/"
	}
	base.RawQuery = ""
	base.Fragment = ""
	query := url.Values{
		"c": {"Portal"}, "a": {"login"}, "login_method": {"1"},
		"user_account": {k.cfg.account}, "user_password": {k.cfg.password},
		"wlan_user_ip": {ip}, "wlan_user_mac": {"000000000000"},
		"jsVersion": {"3.0"},
	}
	if isA79 {
		account := k.cfg.account
		if !k.cfg.suffixExplicit && !strings.Contains(account, "@") {
			account += "@xyw"
		}
		query.Set("user_account", account)
		if mac := strings.NewReplacer("-", "", ":", "").Replace(q.Get("mac")); len(mac) == 12 {
			query.Set("wlan_user_mac", mac)
		}
		query.Set("wlan_ac_name", q.Get("wlanacname"))
		query.Set("wlan_ac_ip", q.Get("wlanacip"))
	}
	ts := time.Now().UnixMilli()
	query.Set("callback", fmt.Sprintf("dr%d", ts))
	query.Set("_", fmt.Sprint(ts))
	base.RawQuery = query.Encode()
	resp, err := k.client.Get(base.String())
	if err != nil {
		return errors.New("认证请求失败（网络不可达或超时）")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("认证接口 HTTP %d", resp.StatusCode)
	}
	start, end := strings.IndexByte(string(data), '{'), strings.LastIndexByte(string(data), '}')
	if start < 0 || end < start {
		return errors.New("认证接口未返回可识别的 JSON")
	}
	var result map[string]any
	if err := json.Unmarshal(data[start:end+1], &result); err != nil {
		return fmt.Errorf("认证响应解析失败：%w", err)
	}
	if fmt.Sprint(result["result"]) == "1" || fmt.Sprint(result["result"]) == "ok" {
		log.Print("认证接口返回成功，正在核验 IPv4 外网")
		return nil
	}
	if fmt.Sprint(result["ret_code"]) == "2" {
		log.Print("认证接口称 IP 已在线，正在核验 IPv4 外网")
		return nil
	}
	// Never print the raw response: some portals echo credentials or tokens.
	return fmt.Errorf("认证接口未报告成功（result=%v，ret_code=%v）", result["result"], result["ret_code"])
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("读取 %s 失败：%w", path, err)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vars[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	id, password := vars["STUDENT_ID"], vars["PASSWORD"]
	if id == "" || password == "" {
		return config{}, errors.New(".env 需要 STUDENT_ID 和 PASSWORD")
	}
	account := id
	if !strings.Contains(account, "@") {
		account += vars["ACCOUNT_SUFFIX"]
	}
	_, suffixExplicit := vars["ACCOUNT_SUFFIX"]
	return config{account: account, password: password, portalURL: vars["PORTAL_URL"], suffixExplicit: suffixExplicit}, nil
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

func safePortal(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d/5)+1))
}

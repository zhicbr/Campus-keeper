package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	healthInterval = 20 * time.Second
	linkInterval   = 5 * time.Second
	statusInterval = 10 * time.Second
	retryBase      = 5 * time.Second
	retryMax       = 5 * time.Minute
)

type controller struct {
	cfg          config
	portal       *portalClient
	logger       *log.Logger
	ui           *trayUI
	quiet        bool
	state        string
	campus       campus
	lastPortal   string
	link         linkState
	backoff      time.Duration
	nextRetry    time.Time
	retryTimer   *time.Timer
	retryChannel <-chan time.Time
	wifiBackoff  time.Duration
	nextWiFiTry  time.Time
	wifiTimer    *time.Timer
	wifiChannel  <-chan time.Time
	nextReset    time.Time
}

func main() {
	quiet := flag.Bool("quiet", false, "only log changes and actions (for scheduled task)")
	once := flag.Bool("once", false, "one check and one login attempt")
	console := flag.Bool("console", false, "show terminal at startup")
	flag.Parse()
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Dir(exe)
	cfg, err := loadConfig(filepath.Join(dir, ".env"))
	if err != nil {
		log.Fatal(err)
	}
	writer, err := newRotatingLog(filepath.Join(dir, "keeper.log"), 5<<20)
	if err != nil {
		log.Fatal(err)
	}
	defer writer.Close()
	logger := log.New(writer, "", log.Ldate|log.Ltime)
	if err := acquireSingleInstance(); err != nil {
		logger.Fatal(err)
	}
	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()
	ui, err := startTray(cancel, *console)
	if err != nil {
		logger.Fatal(err)
	}
	defer ui.close()
	c := &controller{
		cfg: cfg, portal: newPortalClient(cfg), logger: logger,
		ui: ui, quiet: *quiet, backoff: retryBase, retryTimer: time.NewTimer(time.Hour),
		wifiBackoff: retryBase, wifiTimer: time.NewTimer(time.Hour),
	}
	stopTimer(c.retryTimer)
	stopTimer(c.wifiTimer)
	c.nextReset = nextSix(time.Now())
	logger.Printf("校园网守护程序启动；账号 %s；校区模式 %s；外网检测 %s；链路核查 %s；退避每天 06:00 清零", mask(cfg.account), cfg.campusMode, healthInterval, linkInterval)
	if *once {
		if err := c.cycle(ctx, "手动单次检测"); err != nil {
			logger.Printf("本次检查未恢复：%v", err)
			os.Exit(1)
		}
		return
	}
	if err := c.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Fatal(err)
	}
}

func (c *controller) run(ctx context.Context) error {
	networkEvents, closeNotifier, err := startNetworkNotifications()
	if err != nil {
		c.logger.Printf("Windows 网络通知不可用，使用 %s 链路核查兜底：%v", linkInterval, err)
	}
	linkChanges := make(chan linkState, 1)
	monitorDone := make(chan struct{})
	go func() { monitorLink(ctx, c.logger, networkEvents, linkChanges); close(monitorDone) }()
	defer func() {
		if closeNotifier != nil {
			closeNotifier()
		}
		<-monitorDone
	}()
	healthTicker := time.NewTicker(healthInterval)
	statusTicker := time.NewTicker(statusInterval)
	sixTimer := time.NewTimer(time.Until(c.nextReset))
	defer healthTicker.Stop()
	defer statusTicker.Stop()
	defer sixTimer.Stop()
	defer c.retryTimer.Stop()
	defer c.wifiTimer.Stop()
	_ = c.cycle(ctx, "启动检查")
	for {
		select {
		case <-ctx.Done():
			c.logger.Print("收到停止信号，守护程序退出")
			return ctx.Err()
		case link := <-linkChanges:
			if link != c.link {
				c.adoptLink(link)
				_ = c.cycle(ctx, "网络接口变化")
			}
		case <-healthTicker.C:
			c.resetAtSixIfDue()
			_ = c.cycle(ctx, "定期外网检查")
		case <-c.retryChannel:
			c.retryChannel = nil
			c.resetAtSixIfDue()
			_ = c.cycle(ctx, "认证重试")
		case <-c.wifiChannel:
			c.wifiChannel = nil
			c.resetAtSixIfDue()
			_ = c.cycle(ctx, "Wi-Fi 重试")
		case <-statusTicker.C:
			if c.resetAtSixIfDue() && c.state != "online" {
				_ = c.cycle(ctx, "06:00 退避清零")
			}
			if !c.quiet {
				c.logger.Printf("当前状态：%s", c.status())
			}
		case <-sixTimer.C:
			reset := c.resetAtSixIfDue()
			sixTimer.Reset(time.Until(c.nextReset))
			if reset && c.state != "online" {
				_ = c.cycle(ctx, "06:00 退避清零")
			}
		}
	}
}

func (c *controller) cycle(ctx context.Context, reason string) error {
	c.resetAtSixIfDue()
	c.refreshLink()
	if !c.link.ready() {
		return c.ensureWiFi()
	}
	c.resetWiFiRetry()
	c.portal.setSourceIP(c.link.ipv4)
	c.detectCampusFromLink()
	probeLink := c.link
	if !c.quiet {
		c.logger.Printf("%s：正在检测 IPv4 外网", reason)
	}
	result := c.portal.probe(ctx)
	if result.online {
		c.detectCampusFromLink()
		c.setState("online", "IPv4 外网已连通")
		c.resetRetry()
		return nil
	}
	if result.redirect != "" {
		c.lastPortal = result.redirect
	}
	// Confirm a first failure before sending credentials. Captive redirects
	// are already a definitive sign of unauthenticated IPv4 access.
	if result.redirect == "" {
		if !c.quiet {
			c.logger.Printf("外网探测异常，2 秒后复核：%s", result.reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		second := c.portal.probe(ctx)
		if second.online {
			c.detectCampusFromLink()
			c.setState("online", "IPv4 外网已恢复")
			c.resetRetry()
			return nil
		}
		if second.redirect != "" {
			c.lastPortal = second.redirect
		}
		result = second
	}
	c.refreshLink()
	if !c.link.sameNetwork(probeLink) {
		return errors.New("外网检测期间网络已切换，等待重新识别校区")
	}
	if !c.link.ready() {
		c.setState("link-down", "校园网接口未就绪："+c.link.describe())
		return errors.New("校园网接口未就绪")
	}
	c.setState("offline", "IPv4 外网中断："+result.reason)
	if !isCampusLink(c.link) {
		return errors.New("当前接口没有校园网特征，等待网络恢复")
	}
	if !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry) {
		if !c.quiet {
			c.logger.Printf("认证退避中，预计 %s 后重试", time.Until(c.nextRetry).Round(time.Second))
		}
		return errors.New("等待认证重试")
	}
	portal := c.lastPortal
	if portal == "" {
		found, discoverErr := c.portal.discover(ctx)
		if discoverErr == nil {
			portal = found
		} else {
			portal = c.cfg.portalURL
			if portal == "" && (c.cfg.campusMode == campusWenchang || c.campus == campusWenchang) {
				portal = "http://10.2.5.251/a79.htm"
			}
			if portal == "" && (c.cfg.campusMode == campusNanhu || c.campus == campusNanhu) {
				portal = "http://10.2.5.251:801/eportal/"
			}
		}
	}
	site, err := chooseCampus(c.cfg.campusMode, portal, c.link, c.campus)
	if err != nil {
		c.scheduleRetry("不能选择认证流程：" + err.Error())
		return err
	}
	if c.campus != site {
		c.logger.Printf("已选择%s校区认证流程", campusName(site))
		c.campus = site
	}
	if kind, _ := classifyPortal(portal); kind == portalWenchang && site == campusNanhu {
		c.logger.Print("提示：CAMPUS=nanhu 已覆盖 /a79.htm 的文昌自动推断；请现场核对校区")
	}
	if _, portalErr := classifyPortal(portal); portalErr != nil {
		c.logger.Printf("忽略未知认证跳转 %s；仅使用已知校园网服务器", safePortal(portal))
		portal = ""
	}
	if portal != "" {
		c.lastPortal = portal
	}
	chosenLink := c.link
	c.refreshLink()
	if !c.link.sameNetwork(chosenLink) {
		return errors.New("认证前网络已切换，等待重新识别校区")
	}
	if !c.link.ready() {
		c.setState("link-down", "认证前校园网接口断开："+c.link.describe())
		return errors.New("校园网接口未就绪")
	}
	c.logger.Printf("尝试%s校园网认证：%s", campusName(site), safePortal(portal))
	if err := c.portal.login(ctx, site, portal, c.link.ipv4); err != nil {
		c.scheduleRetry("认证接口未确认成功：" + err.Error())
		return err
	}
	c.logger.Print("认证接口已响应，正在核验 IPv4 外网")
	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		if c.portal.probe(ctx).online {
			c.setState("online", "登录后确认 IPv4 外网已恢复")
			c.resetRetry()
			return nil
		}
		c.refreshLink()
		if !c.link.sameNetwork(chosenLink) {
			return errors.New("认证核验期间网络已切换")
		}
		if !c.link.ready() {
			c.setState("link-down", "认证核验期间校园网接口断开："+c.link.describe())
			return errors.New("校园网接口未就绪")
		}
	}
	c.scheduleRetry("认证后 15 秒仍无法访问 IPv4 外网")
	return errors.New("认证未使外网恢复")
}

func (c *controller) setState(next, message string) {
	if c.state != next {
		c.logger.Printf("状态变化：%s → %s；%s", stateName(c.state), stateName(next), message)
	}
	c.state = next
	if c.ui != nil {
		c.ui.setStatus(stateName(next))
	}
}

func (c *controller) status() string {
	site := c.campus
	if site == "" && c.cfg.campusMode != campusAuto {
		site = c.cfg.campusMode
	}
	s := campusName(site) + "；" + stateName(c.state) + "；" + c.link.describe()
	if !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry) {
		s += "；下次认证约 " + time.Until(c.nextRetry).Round(time.Second).String() + " 后"
	}
	if !c.nextWiFiTry.IsZero() && time.Now().Before(c.nextWiFiTry) {
		s += "；下次 Wi-Fi 检查约 " + time.Until(c.nextWiFiTry).Round(time.Second).String() + " 后"
	}
	return s
}

func stateName(s string) string {
	switch s {
	case "online":
		return "外网正常"
	case "offline":
		return "外网中断"
	case "link-down":
		return "校园网接口未就绪"
	case "wifi-connecting":
		return "正在连接 Wi-Fi"
	case "wifi-wait-ip":
		return "Wi-Fi 已连接，等待 IPv4"
	default:
		return "待检查"
	}
}

func (c *controller) scheduleRetry(reason string) {
	delay := min(c.backoff+time.Duration(rand.Int63n(int64(c.backoff/5)+1)), retryMax)
	c.nextRetry = time.Now().Add(delay)
	if !c.retryTimer.Stop() {
		select {
		case <-c.retryTimer.C:
		default:
		}
	}
	c.retryTimer.Reset(delay)
	c.retryChannel = c.retryTimer.C
	c.logger.Printf("%s；%s 后重试（指数退避上限 %s）", reason, delay.Round(time.Second), retryMax)
	c.backoff = min(c.backoff*2, retryMax)
}

func (c *controller) ensureWiFi() error {
	if !c.nextWiFiTry.IsZero() && time.Now().Before(c.nextWiFiTry) {
		return errors.New("等待 Wi-Fi 连接或重试")
	}
	status, err := requestSavedWiFi(studentWiFiProfile)
	if err != nil {
		c.setState("link-down", fmt.Sprintf("有线网不可用；Wi-Fi %s 未接入：%v", studentWiFiProfile, err))
		c.scheduleWiFiRetry(true)
		c.logger.Printf("Wi-Fi 连接失败：%v；%s 后重试", err, time.Until(c.nextWiFiTry).Round(time.Second))
		return err
	}
	if status == "已连接" {
		c.setState("wifi-wait-ip", "有线网不可用；无线网已连接，等待 IPv4 地址")
		c.scheduleWiFiRetry(false)
		return errors.New("Wi-Fi 等待 IPv4 地址")
	}
	c.setState("wifi-connecting", fmt.Sprintf("有线网不可用；%s %s", studentWiFiProfile, status))
	if status == "已请求连接" {
		c.logger.Printf("有线网不可用，已请求 Windows 连接已保存的 Wi-Fi %s；等待关联和 IPv4 地址", studentWiFiProfile)
	}
	c.scheduleWiFiRetry(status == "已请求连接")
	return errors.New("Wi-Fi 正在连接")
}

func (c *controller) scheduleWiFiRetry(grow bool) {
	delay := 10 * time.Second
	if grow {
		delay = c.wifiBackoff
		c.wifiBackoff = min(c.wifiBackoff*2, retryMax)
	}
	c.nextWiFiTry = time.Now().Add(delay)
	if c.wifiTimer != nil {
		stopTimer(c.wifiTimer)
		c.wifiTimer.Reset(delay)
		c.wifiChannel = c.wifiTimer.C
	}
}

func (c *controller) resetWiFiRetry() {
	c.wifiBackoff = retryBase
	c.nextWiFiTry = time.Time{}
	c.wifiChannel = nil
	stopTimer(c.wifiTimer)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func isCampusLink(link linkState) bool {
	if link.kind == "wifi" && link.ssid == studentWiFiProfile {
		return true
	}
	ip := net.ParseIP(link.ipv4).To4()
	return ip != nil && ip[0] == 10
}

func (c *controller) detectCampusFromLink() {
	if c.cfg.campusMode != campusAuto || c.campus != "" || !c.link.ready() {
		return
	}
	gateway, mac, err := gatewayFingerprint(c.link.index)
	if err != nil {
		return // Neighbor cache may be populated by the following HTTP probe.
	}
	if site := campusFromFingerprint(c.link, gateway, mac); site != "" {
		c.campus = site
		c.logger.Printf("本地网络特征匹配%s：网关 %s，网关 MAC %s；当前 IPv4 %s", campusName(site), gateway, mac, c.link.ipv4)
	}
}

func (c *controller) resetRetry() {
	c.backoff = retryBase
	c.nextRetry = time.Time{}
	c.retryChannel = nil
	if !c.retryTimer.Stop() {
		select {
		case <-c.retryTimer.C:
		default:
		}
	}
}

func (c *controller) resetAtSixIfDue() bool {
	now := time.Now()
	if now.Before(c.nextReset) {
		return false
	}
	c.backoff = retryBase
	c.wifiBackoff = retryBase
	c.nextReset = nextSix(now)
	c.logger.Printf("已到每天 06:00：指数退避清零；下次清零时间 %s", c.nextReset.Format("2006-01-02 15:04"))
	if !c.nextRetry.IsZero() {
		c.nextRetry = time.Time{}
		c.retryChannel = nil
		c.retryTimer.Stop()
	}
	if !c.nextWiFiTry.IsZero() {
		c.nextWiFiTry = time.Time{}
		c.wifiChannel = nil
		stopTimer(c.wifiTimer)
	}
	return true
}

func nextSix(now time.Time) time.Time {
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	local := now.In(china)
	next := time.Date(local.Year(), local.Month(), local.Day(), 6, 0, 0, 0, china)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

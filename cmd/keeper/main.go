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
	"strings"
	"sync"
	"syscall"
	"time"

	"wenchang-campus-keeper/internal/campus"
	"wenchang-campus-keeper/internal/config"
	"wenchang-campus-keeper/internal/diagnostics"
	"wenchang-campus-keeper/internal/network"
	"wenchang-campus-keeper/internal/portal"
	"wenchang-campus-keeper/internal/ui"
)

type controller struct {
	policy           DaemonPolicy
	mu               sync.Mutex
	cfg              config.Config
	portal           *portal.Client
	logger           *log.Logger
	ui               *ui.TrayUI
	quiet            bool
	state            string
	campus           campus.Campus
	lastPortal       string
	link             network.LinkState
	backoff          time.Duration
	nextRetry        time.Time
	failureStart     time.Time // 认证失败开始时间
	retryTimer       *time.Timer
	retryChannel     <-chan time.Time
	wifiBackoff      time.Duration
	nextWiFiTry      time.Time
	wifiFailureStart time.Time // Wi-Fi 连接失败开始时间
	wifiTimer        *time.Timer
	wifiChannel      <-chan time.Time
	nextReset        time.Time
	lastStatusLog    time.Time
	deadGatewayCount int
	consecutiveRoams int       // 连续触发 AP 漫游次数（熔断保护）
	linkConnectedAt  time.Time // 当前网络接口建立/切换时间戳
	lastWiFiReset    time.Time
	reconnectTrigger chan struct{}
	linkRecheck      chan struct{}
}

func main() {
	quiet := flag.Bool("quiet", false, "only log changes and actions (for scheduled task)")
	once := flag.Bool("once", false, "one check and one login attempt")
	console := flag.Bool("console", false, "show terminal at startup")
	diagnosticFlag := flag.Bool("diagnostic", false, "run detailed diagnostic recording")
	flag.Parse()

	if err := acquireSingleInstance(); err != nil {
		wakeExistingInstance()
		return
	}
	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	logFile := config.LogPath()
	writer, err := diagnostics.NewRotatingLog(logFile, 5<<20)
	if err != nil {
		log.Fatal(err)
	}
	defer writer.Close()
	logger := log.New(writer, "", log.Ldate|log.Ltime|log.Lmicroseconds)

	cfg, cfgErr := config.LoadEncryptedConfig()
	if cfgErr != nil {
		logger.Printf("未检测到登录配置，等待在设置面板填写：%v", cfgErr)
	}

	exe, err := os.Executable()
	if err != nil {
		logger.Fatal(err)
	}

	var diagnostic *diagnostics.Diagnostics
	if *diagnosticFlag || strings.Contains(strings.ToLower(filepath.Base(exe)), "diagnostic") {
		var err error
		diagnostic, err = diagnostics.StartDiagnostics(ctx, config.AppDataDir(), logger)
		if err != nil {
			logger.Fatal(err)
		}
		diagnostic.SetLinkSampler(func() (int, string, string) {
			link := network.ReadLink(network.LinkState{})
			if !link.Ready() {
				return 0, "", ""
			}
			gw, _, _ := campus.GatewayFingerprint(link.Index)
			return link.Index, link.IPv4, gw
		})
		defer diagnostic.Close()
	}

	portalClient := portal.NewClient(cfg)
	portalClient.SetDiagnostic(diagnostic)
	c := newController(cfg, portalClient, logger, *quiet)

	tray, err := ui.StartTray(
		ui.TrayCallbacks{
			Quit: cancel,
			Reconnect: func() {
				select {
				case c.reconnectTrigger <- struct{}{}:
				default:
				}
			},
			GetConfig: func() config.Config {
				c.mu.Lock()
				defer c.mu.Unlock()
				return c.cfg
			},
			SaveConfig: c.updateConfig,
		},
		*console,
	)
	if err != nil {
		logger.Fatal(err)
	}
	defer tray.Close()
	c.ui = tray
	c.syncUI()

	logger.Printf("CampusKeeper v%s 启动；账号 %s；运营商 %s；外网检测 %s；链路核查 %s；退避每天 %02d:%02d 清零", config.AppVersion, config.Mask(cfg.Account), cfg.OperatorName(), c.policy.HealthInterval, c.policy.LinkInterval, c.policy.DailyResetHour, c.policy.DailyResetMinute)
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
	p := c.getPolicy()
	networkEvents, closeNotifier, err := network.StartNetworkNotifications()
	if err != nil {
		c.logger.Printf("Windows 网络通知不可用，使用 %s 链路核查兜底：%v", p.LinkInterval, err)
	}
	linkChanges := make(chan network.LinkState, 1)
	monitorDone := make(chan struct{})
	go func() {
		network.MonitorLink(ctx, c.logger, networkEvents, linkChanges, c.portal.Diagnostic(), p.LinkInterval)
		close(monitorDone)
	}()
	defer func() {
		if closeNotifier != nil {
			closeNotifier()
		}
		<-monitorDone
	}()
	healthTicker := time.NewTicker(p.HealthInterval)
	statusTicker := time.NewTicker(p.StatusInterval)
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
		case <-c.reconnectTrigger:
			c.logger.Print("收到重新连接请求，立即重置退避并核查网络")
			c.resetRetry()
			c.resetWiFiRetry()
			_ = c.cycle(ctx, "手动重连")
		case <-c.linkRecheck:
			_ = c.cycle(ctx, "切网后立即复核")
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
			if !c.quiet && (c.state != "online" || time.Since(c.lastStatusLog) >= p.OnlineStatusInterval) {
				c.logger.Printf("当前状态：%s", c.status())
				c.lastStatusLog = time.Now()
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

func triggerGatewayARP(gatewayIP string) {
	if gatewayIP == "" {
		return
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(gatewayIP, "53"), 50*time.Millisecond)
	if err == nil {
		_, _ = conn.Write([]byte{0})
		_ = conn.Close()
	}
}

func (c *controller) cycle(ctx context.Context, reason string) error {
	p := c.getPolicy()
	if err := ctx.Err(); err != nil {
		return err
	}
	if diag := c.portal.Diagnostic(); diag != nil {
		diag.Log.Printf("cycle begin reason=%q link=%+v state=%q", reason, c.link, c.state)
		defer func() {
			diag.Log.Printf("cycle end reason=%q link=%+v state=%q", reason, c.link, c.state)
		}()
	}
	c.resetAtSixIfDue()
	c.refreshLink()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.link.Ready() {
		return c.ensureWiFi()
	}
	c.resetWiFiRetry()
	c.portal.SetSourceIP(c.link.IPv4)
	c.detectCampusFromLink()
	probeLink := c.link
	if !c.quiet && (reason != "定期外网检查" || c.state != "online") {
		c.logger.Printf("%s：正在检测 IPv4 网络", reason)
	}
	result := c.portal.Probe(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.verifyProbeLink(probeLink); err != nil {
		return err
	}
	if result.Online {
		c.deadGatewayCount = 0
		c.detectCampusFromLink()
		c.setState("online", "已连通")
		c.resetRetry()
		c.resetWiFiRetry()
		return nil
	}
	if result.Redirect != "" {
		c.lastPortal = result.Redirect
	}
	// Confirm a first failure before sending credentials. Captive redirects
	// are already a definitive sign of unauthenticated IPv4 access.
	if result.Redirect == "" {
		if !c.quiet {
			c.logger.Printf("网络探测异常，%v 后复核：%s", p.LinkDebounce, result.Reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.LinkDebounce):
		}
		second := c.portal.Probe(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.verifyProbeLink(probeLink); err != nil {
			return err
		}
		if second.Online {
			c.deadGatewayCount = 0
			c.detectCampusFromLink()
			c.setState("online", "已恢复")
			c.resetRetry()
			c.resetWiFiRetry()
			return nil
		}
		if second.Redirect != "" {
			c.lastPortal = second.Redirect
		}
		result = second
	}
	c.refreshLink()
	if !c.link.SameNetwork(probeLink) {
		c.scheduleLinkRecheck()
		return errors.New("网络检测期间网络已切换，等待重新识别")
	}
	if !c.link.Ready() {
		c.setState("link-down", "接口未就绪："+c.link.Describe())
		return errors.New("接口未就绪")
	}
	c.detectCampusFromLink()
	c.setState("offline", "网络中断："+result.Reason)
	if !network.IsCampusLink(c.link) {
		return errors.New("当前接口没有校园网特征，等待网络恢复")
	}
	if !c.cfg.Valid() {
		c.setState("config-required", "未配置登录账号或密码")
		return errors.New("未配置登录账号或密码")
	}
	if c.link.Kind == "wifi" && result.Redirect == "" {
		if p.HandshakeGracePeriod > 0 && time.Since(c.linkConnectedAt) < p.HandshakeGracePeriod {
			// 连接初期握手冷静期（默认 60s），等待 DHCP、802.11 关联及 ARP 学习稳定，绝不提前掐网
			c.deadGatewayCount = 0
		} else {
			gwIP, gwMac, gwErr := campus.GatewayFingerprint(c.link.Index)
			if gwErr != nil || gwMac == "" {
				if gwIP != "" {
					triggerGatewayARP(gwIP)
				}
				c.deadGatewayCount++
				if c.deadGatewayCount >= p.DeadGatewayThreshold && time.Since(c.lastWiFiReset) >= p.WiFiResetCooldown {
					if c.consecutiveRoams >= p.MaxConsecutiveRoams {
						c.logger.Printf("检测到 Wi-Fi 网关无响应（连续 %d 次无 ARP 邻居），但已达到最大漫游次数（%d 次），触发熔断暂停重连以防网络震荡", c.deadGatewayCount, p.MaxConsecutiveRoams)
					} else {
						c.deadGatewayCount = 0
						c.consecutiveRoams++
						c.lastWiFiReset = time.Now()
						c.logger.Printf("检测到 Wi-Fi 网关无响应（连续 %d 次无 ARP 邻居），执行断开重连以漫游至健康 AP（第 %d/%d 次）", p.DeadGatewayThreshold, c.consecutiveRoams, p.MaxConsecutiveRoams)
						c.setState("wifi-connecting", "Wi-Fi 网关失效，正在重新连接")
						if err := network.ReconnectSavedWiFi(network.StudentWiFiProfile, c.portal.Diagnostic()); err != nil {
							c.logger.Printf("Wi-Fi 重连失败：%v", err)
						}
						return errors.New("Wi-Fi 网关失效，已触发重连")
					}
				}
			} else {
				c.deadGatewayCount = 0
				c.consecutiveRoams = 0
			}
		}
	} else {
		c.deadGatewayCount = 0
		c.consecutiveRoams = 0
	}
	if !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry) {
		if reason == "手动重连" {
			c.resetRetry()
		} else {
			c.setState("retry-wait", "等待认证重试")
			if !c.quiet {
				c.logger.Printf("认证退避中，预计 %s 后重试", time.Until(c.nextRetry).Round(time.Second))
			}
			return errors.New("等待认证重试")
		}
	}
	c.setState("authenticating", "正在准备校园网认证")
	decision, err := c.portal.PrepareAuthentication(ctx, result)
	if err != nil {
		if ctx.Err() == nil {
			c.setState("offline", "暂时无法准备校园网认证")
		}
		if ctx.Err() == nil {
			c.logger.Printf("暂停认证：%v；继续检查网络", err)
		}
		return err
	}
	c.refreshLink()
	if !c.link.SameNetwork(probeLink) {
		c.scheduleLinkRecheck()
		return errors.New("认证复核期间网络已切换，取消本轮认证")
	}
	if decision.Online {
		c.deadGatewayCount = 0
		c.consecutiveRoams = 0
		c.setState("online", "已恢复，取消登录")
		c.resetRetry()
		c.resetWiFiRetry()
		return nil
	}
	portalURL := decision.Redirect
	site, err := campus.ChooseCampus(c.cfg.CampusMode, portalURL, c.link.LinkInfo(), c.campus)
	if err != nil {
		c.scheduleRetry("不能选择认证流程：" + err.Error())
		c.setState("retry-wait", "等待认证重试")
		return err
	}
	if c.campus != site {
		c.logger.Printf("已选择%s认证流程", campus.CampusName(site))
		c.campus = site
	}
	if portalURL != "" {
		c.lastPortal = portalURL
	}
	chosenLink := c.link
	c.refreshLink()
	if !c.link.SameNetwork(chosenLink) {
		c.scheduleLinkRecheck()
		return errors.New("认证前网络已切换，等待重新识别")
	}
	if !c.link.Ready() {
		c.setState("link-down", "认证前接口断开："+c.link.Describe())
		return errors.New("接口未就绪")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if diag := c.portal.Diagnostic(); diag != nil {
		diag.Trigger("before-login")
	}
	c.logger.Printf("尝试%s认证：%s", campus.CampusName(site), portal.SafePortal(portalURL))
	if err := c.portal.Login(ctx, site, portalURL, c.link.IPv4, c.link.MAC); err != nil {
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		c.scheduleRetry("认证接口未确认成功：" + err.Error())
		c.setState("retry-wait", "认证失败，等待重试")
		return err
	}
	c.logger.Print("认证接口已响应，正在核验网络")
	for i := 0; i < p.PostAuthVerifyAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.PostAuthVerifyInterval):
		}
		verified := c.portal.Probe(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.verifyProbeLink(chosenLink); err != nil {
			return err
		}
		if verified.Online {
			c.deadGatewayCount = 0
			c.consecutiveRoams = 0
			c.setState("online", "已连通")
			c.resetRetry()
			c.resetWiFiRetry()
			return nil
		}
		c.refreshLink()
		if !c.link.SameNetwork(chosenLink) {
			c.scheduleLinkRecheck()
			return errors.New("认证核验期间网络已切换")
		}
		if !c.link.Ready() {
			c.setState("link-down", "认证核验期间接口断开："+c.link.Describe())
			return errors.New("接口未就绪")
		}
	}
	c.scheduleRetry("认证后 15 秒仍无法访问外网")
	c.setState("retry-wait", "认证后外网仍未连通，等待重试")
	return errors.New("认证未使网络恢复")
}

func (c *controller) verifyProbeLink(expected network.LinkState) error {
	c.refreshLink()
	return c.acceptProbeLink(expected)
}

func (c *controller) acceptProbeLink(expected network.LinkState) error {
	if !probeLinkCurrent(expected, c.link) {
		c.scheduleLinkRecheck()
		return errors.New("网络探测期间网络已切换，等待重新识别")
	}
	return nil
}

func (c *controller) scheduleLinkRecheck() {
	// The monitor may report this same link later; the main loop would then
	// skip its event because refreshLink already adopted it.
	select {
	case c.linkRecheck <- struct{}{}:
	default:
	}
}

func probeLinkCurrent(expected, current network.LinkState) bool {
	return current.Ready() && current.SameNetwork(expected)
}

func (c *controller) refreshLink() bool {
	next := network.ReadLink(c.link)
	if c.portal != nil && c.portal.Diagnostic() != nil {
		c.portal.Diagnostic().Log.Printf("controller raw link previous=%+v next=%+v", c.link, next)
	}
	return c.adoptLink(next)
}

func (c *controller) adoptLink(next network.LinkState) bool {
	if next == c.link {
		return false
	}
	if !next.SameNetwork(c.link) || (!c.link.Ready() && next.Ready()) {
		// An online result from the previous network is no longer valid.
		c.state = "checking"
		hadContext := c.campus != "" || c.lastPortal != "" || !c.nextRetry.IsZero()
		c.lastPortal = ""
		c.campus = ""
		c.resetRetry()
		c.resetWiFiRetry()
		c.deadGatewayCount = 0
		c.consecutiveRoams = 0
		c.linkConnectedAt = time.Now()
		if hadContext && c.logger != nil {
			c.logger.Print("网络已切换；清除认证及重试缓存")
		}
	}
	c.link = next
	c.syncUI()
	return true
}

func (c *controller) setState(next, message string) {
	if c.state != next {
		if c.portal != nil && c.portal.Diagnostic() != nil {
			c.portal.Diagnostic().Trigger("state-change:" + next)
		}
		c.logger.Printf("状态变化：%s → %s；%s", stateName(c.state), stateName(next), message)
	}
	c.state = next
	c.syncUI()
}

func (c *controller) updateConfig(account, password, opStr string) (bool, error) {
	// 1. Snapshot and resolve candidate config under lock (pure memory operation)
	candidate, changed, err := func() (config.Config, bool, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		res, err := config.ResolveConfigUpdate(account, password, opStr, c.cfg)
		if err != nil {
			return config.Config{}, false, err
		}
		return res.NewConfig, res.Changed, nil
	}()
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}

	// 2. Heavy persistence I/O (DPAPI + temp file + fsync + atomic rename) OUTSIDE the lock
	// This prevents slow disk or antivirus file scanning from blocking the main loop or tray thread.
	if err := config.SaveEncryptedConfig(candidate); err != nil {
		c.logger.Printf("保存登录配置失败：%v", err)
		return false, errors.New("保存配置失败，请检查文件权限或稍后重试")
	}

	// 3. Commit new config under lock
	func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.cfg = candidate
		c.portal.SetConfig(candidate)
	}()

	// 4. Update UI and trigger reconnect outside the lock
	c.logger.Printf("登录配置已更新；账号 %s；运营商 %s", config.Mask(candidate.Account), candidate.OperatorName())
	c.syncUI()

	select {
	case c.reconnectTrigger <- struct{}{}:
	default:
	}
	return true, nil
}

func (c *controller) syncUI() {
	if c.ui == nil {
		return
	}
	c.mu.Lock()
	st := stateName(c.state)
	netSummary := c.link.NetworkSummary(c.campus)
	opName := c.cfg.OperatorName()
	ipv4 := c.link.IPv4
	isCampus := network.IsCampusLink(c.link)
	c.mu.Unlock()

	c.ui.UpdateDashboard(st, netSummary, opName, ipv4, isCampus, c.state)
}

func (c *controller) status() string {
	site := c.campus
	if site == "" && c.cfg.CampusMode != campus.Auto {
		site = c.cfg.CampusMode
	}
	s := c.link.NetworkSummary(site) + "；" + stateName(c.state)
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
		return "已连接"
	case "offline", "link-down":
		return "未连接"
	case "wifi-connecting":
		return "连接中"
	case "wifi-wait-ip":
		return "获取地址中"
	case "authenticating":
		return "正在认证"
	case "retry-wait":
		return "等待重试"
	case "config-required":
		return "需要配置账号"
	default:
		return "检测中"
	}
}

func (c *controller) scheduleRetry(reason string) {
	p := c.getPolicy()
	now := time.Now()
	if c.failureStart.IsZero() {
		c.failureStart = now
	}
	elapsed := now.Sub(c.failureStart)

	var delay time.Duration
	if elapsed < p.FastRetryWindow {
		// 初始快速重试期：固定短间隔重试，避免人在现场等待时进入退避
		delay = p.RetryBase + time.Duration(rand.Int63n(int64(time.Second)+1))
		c.backoff = p.RetryBase
		remaining := (p.FastRetryWindow - elapsed).Round(time.Second)
		c.logger.Printf("%s；%s 后重试（初始快速重试期，剩余 %s）", reason, delay.Round(time.Second), remaining)
	} else {
		// 连续失败超过初始快速期后，进入指数退避以保护无人值守或远程电脑
		delay = min(c.backoff+time.Duration(rand.Int63n(int64(c.backoff/5)+1)), p.RetryMax)
		c.logger.Printf("%s；%s 后重试（已进入退避保护，上限 %s）", reason, delay.Round(time.Second), p.RetryMax)
		c.backoff = min(c.backoff*2, p.RetryMax)
	}

	c.nextRetry = now.Add(delay)
	stopTimer(c.retryTimer)
	c.retryTimer.Reset(delay)
	c.retryChannel = c.retryTimer.C
}

func (c *controller) ensureWiFi() error {
	if !c.nextWiFiTry.IsZero() && time.Now().Before(c.nextWiFiTry) {
		return errors.New("等待 Wi-Fi 连接或重试")
	}
	if diag := c.portal.Diagnostic(); diag != nil {
		diag.Log.Printf("wifi action begin profile=%q link=%+v", network.StudentWiFiProfile, c.link)
		diag.Trigger("before-wifi-check")
	}
	status, err := network.RequestSavedWiFi(network.StudentWiFiProfile, c.portal.Diagnostic())
	if diag := c.portal.Diagnostic(); diag != nil {
		diag.Log.Printf("wifi action end status=%q error=%q", status, diagnostics.DiagnosticError(err))
	}
	if err != nil {
		c.setState("link-down", fmt.Sprintf("有线网不可用；Wi-Fi %s 未接入：%v", network.StudentWiFiProfile, err))
		c.scheduleWiFiRetry(true)
		c.logger.Printf("Wi-Fi 连接失败：%v；%s 后重试", err, time.Until(c.nextWiFiTry).Round(time.Second))
		return err
	}
	if status == "已连接" {
		c.setState("wifi-wait-ip", "有线网不可用；无线网已连接，等待 IPv4 地址")
		c.scheduleWiFiRetry(false)
		return errors.New("Wi-Fi 等待 IPv4 地址")
	}
	c.setState("wifi-connecting", fmt.Sprintf("有线网不可用；%s %s", network.StudentWiFiProfile, status))
	if status == "已请求连接" {
		c.logger.Printf("有线网不可用，已请求 Windows 连接已保存的 Wi-Fi %s；等待关联和 IPv4 地址", network.StudentWiFiProfile)
	}
	c.scheduleWiFiRetry(status == "已请求连接")
	return errors.New("Wi-Fi 正在连接")
}

func (c *controller) scheduleWiFiRetry(grow bool) {
	p := c.getPolicy()
	now := time.Now()
	if c.wifiFailureStart.IsZero() {
		c.wifiFailureStart = now
	}
	elapsed := now.Sub(c.wifiFailureStart)

	delay := p.WiFiInitialDelay
	if grow {
		if elapsed < p.FastRetryWindow {
			delay = p.WiFiInitialDelay
			c.wifiBackoff = p.RetryBase
			remaining := (p.FastRetryWindow - elapsed).Round(time.Second)
			c.logger.Printf("Wi-Fi 连接异常；%s 后重试（初始快速重试期，剩余 %s）", delay.Round(time.Second), remaining)
		} else {
			delay = c.wifiBackoff
			c.logger.Printf("Wi-Fi 连接异常；%s 后重试（已进入退避保护，上限 %s）", delay.Round(time.Second), p.RetryMax)
			c.wifiBackoff = min(c.wifiBackoff*2, p.RetryMax)
		}
	}
	c.nextWiFiTry = now.Add(delay)
	if c.wifiTimer != nil {
		stopTimer(c.wifiTimer)
		c.wifiTimer.Reset(delay)
		c.wifiChannel = c.wifiTimer.C
	}
}

func (c *controller) resetWiFiRetry() {
	c.wifiBackoff = c.getPolicy().RetryBase
	c.nextWiFiTry = time.Time{}
	c.wifiFailureStart = time.Time{}
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

func (c *controller) detectCampusFromLink() {
	if c.cfg.CampusMode != campus.Auto || c.campus != "" || !c.link.Ready() {
		return
	}
	gateway, mac, err := campus.GatewayFingerprint(c.link.Index)
	if err != nil {
		return // Neighbor cache may be populated by the following HTTP probe.
	}
	if site := campus.CampusFromFingerprint(c.link.IPv4, gateway, mac); site != "" {
		c.campus = site
		c.logger.Printf("本地网络特征匹配%s：网关 %s，网关 MAC %s；当前 IPv4 %s", campus.CampusName(site), gateway, mac, c.link.IPv4)
	}
}

func (c *controller) resetRetry() {
	c.backoff = c.getPolicy().RetryBase
	c.nextRetry = time.Time{}
	c.failureStart = time.Time{}
	c.retryChannel = nil
	stopTimer(c.retryTimer)
}

func (c *controller) resetAtSixIfDue() bool {
	now := time.Now()
	if now.Before(c.nextReset) {
		return false
	}
	p := c.getPolicy()
	c.backoff = p.RetryBase
	c.wifiBackoff = p.RetryBase
	c.failureStart = time.Time{}
	c.wifiFailureStart = time.Time{}
	c.nextReset = c.nextDailyReset(now)
	c.logger.Printf("已到每天 %02d:%02d：指数退避清零；下次清零时间 %s", p.DailyResetHour, p.DailyResetMinute, c.nextReset.Format("2006-01-02 15:04"))
	if !c.nextRetry.IsZero() {
		c.nextRetry = time.Time{}
		c.retryChannel = nil
		stopTimer(c.retryTimer)
	}
	if !c.nextWiFiTry.IsZero() {
		c.nextWiFiTry = time.Time{}
		c.wifiChannel = nil
		stopTimer(c.wifiTimer)
	}
	return true
}

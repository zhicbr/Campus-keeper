package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
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

const (
	healthInterval       = 20 * time.Second
	linkInterval         = 5 * time.Second
	statusInterval       = 10 * time.Second
	onlineStatusInterval = time.Minute
	retryBase            = 5 * time.Second
	retryMax             = 5 * time.Minute
	fastRetryWindow      = 2 * time.Minute // 1~3 分钟初始快速重试期，不进入指数退避
)

type controller struct {
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
	lastWiFiReset    time.Time
	reconnectTrigger chan struct{}
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

	c := &controller{
		cfg:              cfg,
		portal:           portal.NewClient(cfg),
		logger:           logger,
		quiet:            *quiet,
		backoff:          retryBase,
		retryTimer:       time.NewTimer(time.Hour),
		wifiBackoff:      retryBase,
		wifiTimer:        time.NewTimer(time.Hour),
		reconnectTrigger: make(chan struct{}, 1),
	}
	c.portal.SetDiagnostic(diagnostic)
	stopTimer(c.retryTimer)
	stopTimer(c.wifiTimer)
	c.nextReset = nextSix(time.Now())

	tray, err := ui.StartTray(
		cancel,
		func() {
			select {
			case c.reconnectTrigger <- struct{}{}:
			default:
			}
		},
		func() config.Config {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.cfg
		},
		func(newCfg config.Config) {
			c.mu.Lock()
			c.cfg = newCfg
			c.portal.SetConfig(newCfg)
			c.mu.Unlock()
			if err := config.SaveEncryptedConfig(newCfg); err != nil {
				c.logger.Printf("保存登录配置失败：%v", err)
			} else {
				c.logger.Printf("登录配置已更新；账号 %s；运营商 %s", config.Mask(newCfg.Account), newCfg.OperatorName())
			}
			c.syncUI()
			select {
			case c.reconnectTrigger <- struct{}{}:
			default:
			}
		},
		*console,
	)
	if err != nil {
		logger.Fatal(err)
	}
	defer tray.Close()
	c.ui = tray
	c.syncUI()

	logger.Printf("CampusKeeper v%s 启动；账号 %s；运营商 %s；外网检测 %s；链路核查 %s；退避每天 06:00 清零", config.AppVersion, config.Mask(cfg.Account), cfg.OperatorName(), healthInterval, linkInterval)
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
	networkEvents, closeNotifier, err := network.StartNetworkNotifications()
	if err != nil {
		c.logger.Printf("Windows 网络通知不可用，使用 %s 链路核查兜底：%v", linkInterval, err)
	}
	linkChanges := make(chan network.LinkState, 1)
	monitorDone := make(chan struct{})
	go func() {
		network.MonitorLink(ctx, c.logger, networkEvents, linkChanges, c.portal.Diagnostic(), linkInterval)
		close(monitorDone)
	}()
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
		case <-c.reconnectTrigger:
			c.logger.Print("收到重新连接请求，立即重置退避并核查网络")
			c.resetRetry()
			c.resetWiFiRetry()
			_ = c.cycle(ctx, "手动重连")
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
			if !c.quiet && (c.state != "online" || time.Since(c.lastStatusLog) >= onlineStatusInterval) {
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

func (c *controller) cycle(ctx context.Context, reason string) error {
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
			c.logger.Printf("网络探测异常，2 秒后复核：%s", result.Reason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		second := c.portal.Probe(ctx)
		if err := ctx.Err(); err != nil {
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
		return errors.New("未配置登录账号或密码")
	}
	if c.link.Kind == "wifi" && result.Redirect == "" {
		_, gwMac, gwErr := campus.GatewayFingerprint(c.link.Index)
		if gwErr != nil || gwMac == "" {
			c.deadGatewayCount++
			if c.deadGatewayCount >= 2 && time.Since(c.lastWiFiReset) >= 60*time.Second {
				c.logger.Printf("检测到 Wi-Fi 网关无响应（连续 %d 次无 ARP 邻居），执行断开重连以漫游至健康 AP", c.deadGatewayCount)
				c.deadGatewayCount = 0
				c.lastWiFiReset = time.Now()
				c.setState("wifi-connecting", "Wi-Fi 网关失效，正在重新连接")
				if err := network.ReconnectSavedWiFi(network.StudentWiFiProfile, c.portal.Diagnostic()); err != nil {
					c.logger.Printf("Wi-Fi 重连失败：%v", err)
				}
				return errors.New("Wi-Fi 网关失效，已触发重连")
			}
		} else {
			c.deadGatewayCount = 0
		}
	} else {
		c.deadGatewayCount = 0
	}
	if !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry) {
		if reason == "手动重连" {
			c.resetRetry()
		} else {
			if !c.quiet {
				c.logger.Printf("认证退避中，预计 %s 后重试", time.Until(c.nextRetry).Round(time.Second))
			}
			return errors.New("等待认证重试")
		}
	}
	decision, err := c.portal.PrepareAuthentication(ctx, result)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Printf("暂停认证：%v；继续检查网络", err)
		}
		return err
	}
	c.refreshLink()
	if !c.link.SameNetwork(probeLink) {
		return errors.New("认证复核期间网络已切换，取消本轮认证")
	}
	if decision.Online {
		c.deadGatewayCount = 0
		c.setState("online", "已恢复，取消登录")
		c.resetRetry()
		c.resetWiFiRetry()
		return nil
	}
	portalURL := decision.Redirect
	site, err := campus.ChooseCampus(c.cfg.CampusMode, portalURL, c.link.LinkInfo(), c.campus)
	if err != nil {
		c.scheduleRetry("不能选择认证流程：" + err.Error())
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
		return err
	}
	c.logger.Print("认证接口已响应，正在核验网络")
	for i := 0; i < 5; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		if c.portal.Probe(ctx).Online {
			c.deadGatewayCount = 0
			c.setState("online", "已连通")
			c.resetRetry()
			c.resetWiFiRetry()
			return nil
		}
		c.refreshLink()
		if !c.link.SameNetwork(chosenLink) {
			return errors.New("认证核验期间网络已切换")
		}
		if !c.link.Ready() {
			c.setState("link-down", "认证核验期间接口断开："+c.link.Describe())
			return errors.New("接口未就绪")
		}
	}
	c.scheduleRetry("认证后 15 秒仍无法访问外网")
	return errors.New("认证未使网络恢复")
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
	if !next.SameNetwork(c.link) {
		hadContext := c.campus != "" || c.lastPortal != "" || !c.nextRetry.IsZero()
		c.lastPortal = ""
		c.campus = ""
		c.resetRetry()
		c.resetWiFiRetry()
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

	c.ui.UpdateDashboard(st, netSummary, opName, ipv4, isCampus)
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
	default:
		return "检测中"
	}
}

func (c *controller) scheduleRetry(reason string) {
	now := time.Now()
	if c.failureStart.IsZero() {
		c.failureStart = now
	}
	elapsed := now.Sub(c.failureStart)

	var delay time.Duration
	if elapsed < fastRetryWindow {
		// 1~3 分钟初始快速重试期：固定短间隔重试，避免人在现场等待时进入退避
		delay = retryBase + time.Duration(rand.Int63n(int64(time.Second)+1))
		c.backoff = retryBase
		remaining := (fastRetryWindow - elapsed).Round(time.Second)
		c.logger.Printf("%s；%s 后重试（初始快速重试期，剩余 %s）", reason, delay.Round(time.Second), remaining)
	} else {
		// 连续失败超过初始快速期后，进入指数退避以保护无人值守或远程电脑
		delay = min(c.backoff+time.Duration(rand.Int63n(int64(c.backoff/5)+1)), retryMax)
		c.logger.Printf("%s；%s 后重试（已进入退避保护，上限 %s）", reason, delay.Round(time.Second), retryMax)
		c.backoff = min(c.backoff*2, retryMax)
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
	now := time.Now()
	if c.wifiFailureStart.IsZero() {
		c.wifiFailureStart = now
	}
	elapsed := now.Sub(c.wifiFailureStart)

	delay := 10 * time.Second
	if grow {
		if elapsed < fastRetryWindow {
			delay = 10 * time.Second
			c.wifiBackoff = retryBase
			remaining := (fastRetryWindow - elapsed).Round(time.Second)
			c.logger.Printf("Wi-Fi 连接异常；10s 后重试（初始快速重试期，剩余 %s）", remaining)
		} else {
			delay = c.wifiBackoff
			c.logger.Printf("Wi-Fi 连接异常；%s 后重试（已进入退避保护，上限 %s）", delay.Round(time.Second), retryMax)
			c.wifiBackoff = min(c.wifiBackoff*2, retryMax)
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
	c.wifiBackoff = retryBase
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
	c.backoff = retryBase
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
	c.backoff = retryBase
	c.wifiBackoff = retryBase
	c.failureStart = time.Time{}
	c.wifiFailureStart = time.Time{}
	c.nextReset = nextSix(now)
	c.logger.Printf("已到每天 06:00：指数退避清零；下次清零时间 %s", c.nextReset.Format("2006-01-02 15:04"))
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

func nextSix(now time.Time) time.Time {
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	local := now.In(china)
	next := time.Date(local.Year(), local.Month(), local.Day(), 6, 0, 0, 0, china)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

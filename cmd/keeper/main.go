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
	"wenchang-campus-keeper/internal/cloud"
	"wenchang-campus-keeper/internal/config"
	"wenchang-campus-keeper/internal/diagnostics"
	"wenchang-campus-keeper/internal/network"
	"wenchang-campus-keeper/internal/portal"
	"wenchang-campus-keeper/internal/sharing"
	"wenchang-campus-keeper/internal/updater"
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
	readLink         func(network.LinkState) network.LinkState
	updateTrigger      chan struct{}
	forceSwitchTrigger chan struct{}
	selfReloginTrigger chan struct{}
	notifiedVersion    string
	cachedFriendCode   string
	cachedFriendCreds  *sharing.SharedCredentials
	lastLoginAttempt   time.Time
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
			SaveConfig:        c.updateConfig,
			SwitchMode:        c.switchMode,
			PublishShare:      c.publishShare,
			RevokeShare:       c.revokeShare,
			ListMyShares:        c.listMyShares,
			IsAutoUpdateEnabled: c.isAutoUpdateEnabled,
			ToggleAutoUpdate:    c.toggleAutoUpdate,
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
	go c.runUpdateLoop(ctx)
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
		case <-c.forceSwitchTrigger:
			c.forceSwitchAccount(ctx, true)
		case <-c.selfReloginTrigger:
			c.forceSwitchAccount(ctx, false)
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
			if !c.quiet && time.Since(c.lastStatusLog) >= p.OnlineStatusInterval {
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

func (c *controller) inBackoff() bool {
	return !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry)
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
	startedInBackoff := c.inBackoff()
	probeLink := c.link
	if !c.quiet && reason != "定期外网检查" {
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
		c.consecutiveRoams = 0
		c.detectCampusFromLink()
		c.setState("online", "已连通")
		c.resetRetry()
		c.resetWiFiRetry()
		return nil
	}
	if result.Redirect != "" {
		c.lastPortal = result.Redirect
	}
	// In backoff window: periodic checks are silent opportunistic probes.
	// Evaluate using startedInBackoff captured before Probe() so a 6s probe timeout
	// crossing the backoff expiry boundary never hijacks retryTimer.
	if (startedInBackoff || c.inBackoff()) && reason == "定期外网检查" {
		return nil
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
	if c.inBackoff() && reason != "手动重连" {
		c.setState("retry-wait", "等待认证重试")
		if !c.quiet && reason != "定期外网检查" {
			c.logger.Printf("认证退避中，预计 %s 后重试", time.Until(c.nextRetry).Round(time.Second))
		}
		return errors.New("等待认证重试")
	}
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
				if p.RoamDecayDuration > 0 && time.Since(c.lastWiFiReset) > p.RoamDecayDuration {
					c.consecutiveRoams = 0
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
			}
		}
	} else {
		c.deadGatewayCount = 0
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
		if errors.Is(err, portal.ErrNoAuthenticationEvidence) && network.IsCampusLink(c.link) {
			defaultPortal := "http://" + campus.AuthHost + campus.PathFrontPage
			c.logger.Printf("未收到跳转特征，但在已知校园网链路上（%s），降级使用默认入口尝试认证：%s", c.link.Describe(), defaultPortal)
			decision = portal.ProbeResult{Redirect: defaultPortal, Reason: "已知校园网链路默认回退"}
		} else {
			if ctx.Err() == nil {
				c.setState("offline", "暂时无法准备校园网认证")
			}
			if ctx.Err() == nil {
				c.logger.Printf("暂停认证：%v；继续检查网络", err)
			}
			return err
		}
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
	loginCfg, err := c.prepareLoginConfig(ctx)
	if err != nil {
		c.scheduleRetry("获取认证凭据失败：" + err.Error())
		c.setState("retry-wait", "认证失败："+err.Error())
		return err
	}
	c.portal.SetConfig(loginCfg)

	c.logger.Printf("尝试%s认证：%s", campus.CampusName(site), portal.SafePortal(portalURL))
	c.lastLoginAttempt = time.Now()
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
	reader := c.readLink
	if reader == nil {
		reader = network.ReadLink
	}
	next := reader(c.link)
	if c.portal != nil && c.portal.Diagnostic() != nil {
		c.portal.Diagnostic().Log.Printf("controller raw link previous=%+v next=%+v", c.link, next)
	}
	return c.adoptLink(next)
}

func (c *controller) adoptLink(next network.LinkState) bool {
	c.mu.Lock()
	if next == c.link {
		c.mu.Unlock()
		return false
	}
	switched := !next.SameNetwork(c.link) || (!c.link.Ready() && next.Ready())
	hadContext := false
	if switched {
		// An online result from the previous network is no longer valid.
		c.state = "checking"
		hadContext = c.campus != "" || c.lastPortal != "" || !c.nextRetry.IsZero()
		c.lastPortal = ""
		c.campus = ""
		c.resetRetry()
		c.resetWiFiRetry()
		c.deadGatewayCount = 0
		c.consecutiveRoams = 0
		c.linkConnectedAt = time.Now()
	}
	c.link = next
	c.mu.Unlock()
	if switched && hadContext && c.logger != nil {
		c.logger.Print("网络已切换；清除认证及重试缓存")
	}
	c.syncUI()
	return true
}

func (c *controller) setState(next, message string) {
	c.mu.Lock()
	prev := c.state
	c.state = next
	c.mu.Unlock()
	if prev != next {
		if c.portal != nil && c.portal.Diagnostic() != nil {
			c.portal.Diagnostic().Trigger("state-change:" + next)
		}
		c.logger.Printf("状态变化：%s → %s；%s", stateName(prev), stateName(next), message)
	}
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

	if candidate.ActiveMode != config.ModeFriend && c.selfReloginTrigger != nil {
		select {
		case c.selfReloginTrigger <- struct{}{}:
		default:
		}
	} else {
		select {
		case c.reconnectTrigger <- struct{}{}:
		default:
		}
	}
	return true, nil
}

func (c *controller) prepareLoginConfig(ctx context.Context) (config.Config, error) {
	c.mu.Lock()
	cfg := c.cfg
	cachedCode := c.cachedFriendCode
	var cachedCreds *sharing.SharedCredentials
	if c.cachedFriendCreds != nil {
		cp := *c.cachedFriendCreds
		cachedCreds = &cp
	}
	c.mu.Unlock()

	if cfg.ActiveMode != config.ModeFriend {
		return cfg, nil
	}

	code := strings.TrimSpace(cfg.FriendShareCode)
	if code == "" {
		return cfg, errors.New("借用模式下未配置好友分享码")
	}

	shareID, key, err := sharing.ParseShareCode(code)
	if err != nil {
		return cfg, fmt.Errorf("解析好友分享码失败: %w", err)
	}

	cloudClient := cloud.NewClient(cfg.CloudHubURL)
	c.logger.Printf("正在从云端拉取好友分享凭据: shareID=%s", shareID)
	var creds *sharing.SharedCredentials
	resp, err := cloudClient.FetchShare(ctx, shareID)
	if err != nil {
		errMsg := err.Error()
		revokedOrExpired := strings.Contains(errMsg, "不存在或已被所有者吊销") || strings.Contains(errMsg, "已过期")
		if revokedOrExpired {
			c.mu.Lock()
			if c.cachedFriendCode == code {
				c.cachedFriendCode = ""
				c.cachedFriendCreds = nil
			}
			c.mu.Unlock()
			return cfg, fmt.Errorf("拉取好友分享凭据失败: %w", err)
		}
		if cachedCreds != nil && cachedCode == code {
			c.logger.Printf("当前无法直连云端（%v），使用本次会话已拉取的好友分享凭据进行登录", err)
			creds = cachedCreds
		} else {
			return cfg, fmt.Errorf("拉取好友分享凭据失败: %w", err)
		}
	} else {
		creds, err = sharing.DecryptCredentials(resp.EncryptedPayload, key)
		if err != nil {
			return cfg, fmt.Errorf("解密好友分享凭据失败: %w", err)
		}
		cp := *creds
		c.mu.Lock()
		c.cachedFriendCode = code
		c.cachedFriendCreds = &cp
		c.mu.Unlock()
	}

	c.logger.Printf("已成功获取好友分享凭据 (备注: %s, 运营商: %s)", creds.Memo, creds.Operator)
	loginCfg := cfg
	loginCfg.Account = creds.Account
	loginCfg.Password = creds.Password
	loginCfg.Operator = config.Operator(creds.Operator)
	return loginCfg, nil
}

func (c *controller) switchMode(mode config.ActiveMode, friendShareCode string) error {
	var fetchedCreds *sharing.SharedCredentials
	var cleanCode string
	if mode == config.ModeFriend {
		cleanCode = strings.TrimSpace(friendShareCode)
		if cleanCode == "" {
			return errors.New("请输入好友分享码")
		}
		shareID, key, err := sharing.ParseShareCode(cleanCode)
		if err != nil {
			return err
		}
		c.mu.Lock()
		hubURL := c.cfg.CloudHubURL
		c.mu.Unlock()
		cloudClient := cloud.NewClient(hubURL)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := cloudClient.FetchShare(ctx, shareID)
		if err != nil {
			return err
		}
		creds, err := sharing.DecryptCredentials(resp.EncryptedPayload, key)
		if err != nil {
			return fmt.Errorf("分享码解密失败: %w", err)
		}
		fetchedCreds = creds
	}

	cfgToSave := func() config.Config {
		c.mu.Lock()
		defer c.mu.Unlock()
		if mode == config.ModeFriend {
			c.cfg.FriendShareCode = cleanCode
			c.cachedFriendCode = cleanCode
			c.cachedFriendCreds = fetchedCreds
		} else {
			c.cachedFriendCode = ""
			c.cachedFriendCreds = nil
		}
		c.cfg.ActiveMode = mode
		return c.cfg
	}()

	if err := config.SaveEncryptedConfig(cfgToSave); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	c.logger.Printf("运行模式已切换为：%s", mode)
	c.syncUI()
	if c.forceSwitchTrigger != nil {
		select {
		case c.forceSwitchTrigger <- struct{}{}:
		default:
		}
	}
	return nil
}

func (c *controller) forceSwitchAccount(ctx context.Context, switchToCampusWiFi bool) {
	c.resetRetry()
	c.resetWiFiRetry()
	c.refreshLink()

	beforeSwitch := time.Now()
	switchedAccount := false
	if !c.link.Ready() || !network.IsCampusLink(c.link) {
		if !switchToCampusWiFi {
			_ = c.cycle(ctx, "配置更新核查")
			return
		}
		c.logger.Printf("当前不在校园网链路（%s），正在断开当前网络并自动切换至 %s...", c.link.Describe(), network.StudentWiFiProfile)
		c.setState("wifi-connecting", "正在切换至校园网 "+network.StudentWiFiProfile)
		if err := network.ReconnectSavedWiFi(network.StudentWiFiProfile, c.portal.Diagnostic()); err != nil {
			c.logger.Printf("自动切换校园网 %s 失败：%v", network.StudentWiFiProfile, err)
			_ = c.cycle(ctx, "切换模式核查")
			return
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			c.refreshLink()
			if c.link.Ready() && network.IsCampusLink(c.link) {
				break
			}
		}
		if !c.link.Ready() || !network.IsCampusLink(c.link) {
			_ = c.cycle(ctx, "切换校园网重连")
			return
		}
	} else {
		switchedAccount = true
		c.portal.SetSourceIP(c.link.IPv4)
		c.detectCampusFromLink()
		site := c.campus
		if site == "" {
			site, _ = campus.ChooseCampus(c.cfg.CampusMode, c.lastPortal, c.link.LinkInfo(), c.campus)
		}
		if site == "" {
			c.logger.Print("未识别当前所属校区，跳过门户注销并核验网络状态")
		} else {
			c.logger.Printf("正在注销当前%s校园网会话以切换账号登录...", campus.CampusName(site))
			if err := c.portal.Logout(ctx, site, c.lastPortal, c.link.IPv4, c.link.MAC); err != nil {
				c.logger.Printf("注销旧会话返回：%v（继续核验网络状态）", err)
			} else {
				c.logger.Printf("旧会话已注销，准备使用新模式账号登录")
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(800 * time.Millisecond):
			}
			c.setState("checking", "正在核验旧会话是否已注销")
		}
	}

	err := c.cycle(ctx, "切换账号重连")
	if switchedAccount && err == nil && c.lastLoginAttempt.Before(beforeSwitch) {
		c.logger.Print("提示：注销后外网仍处于连通状态，校园网旧会话可能未实际下线（或存在无感知/代理），本次未触发新账号认证")
	}
}

func (c *controller) publishShare(memo string, durationHours int) (string, error) {
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()

	if strings.TrimSpace(cfg.Account) == "" || strings.TrimSpace(cfg.Password) == "" {
		return "", errors.New("请先在「账号配置」中配置本人的校园网账号与密码，再生成分享")
	}

	key, err := sharing.GenerateRandomKey()
	if err != nil {
		return "", err
	}
	shareID, err := sharing.GenerateShareID()
	if err != nil {
		return "", err
	}

	creds := sharing.SharedCredentials{
		Account:  cfg.Account,
		Password: cfg.Password,
		Operator: string(cfg.Operator),
		Memo:     memo,
	}

	encryptedPayload, err := sharing.EncryptCredentials(creds, key)
	if err != nil {
		return "", fmt.Errorf("加密凭据失败: %w", err)
	}

	cloudClient := cloud.NewClient(cfg.CloudHubURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubResp, err := cloudClient.PublishShare(ctx, cfg.OwnerAdminToken, cloud.PublishRequest{
		ShareID:          shareID,
		EncryptedPayload: encryptedPayload,
		Operator:         string(cfg.Operator),
		Memo:             memo,
		DurationHours:    durationHours,
	})
	if err != nil {
		return "", err
	}

	shareCode := sharing.FormatShareCode(shareID, key)

	c.mu.Lock()
	c.cfg.PublishedShares = append(c.cfg.PublishedShares, config.PublishedShareItem{
		ShareID:   shareID,
		Memo:      memo,
		CreatedAt: time.Now().UnixMilli(),
		ExpiresAt: pubResp.ExpiresAt,
		ShareCode: shareCode,
	})
	_ = config.SaveEncryptedConfig(c.cfg)
	c.mu.Unlock()

	c.logger.Printf("已生成新好友分享码: shareID=%s, 备注=%s, 有效期=%d小时", shareID, memo, durationHours)
	return shareCode, nil
}

func (c *controller) revokeShare(shareID string) error {
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()

	cloudClient := cloud.NewClient(cfg.CloudHubURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := cloudClient.RevokeShare(ctx, cfg.OwnerAdminToken, shareID); err != nil {
		return err
	}

	c.mu.Lock()
	var remaining []config.PublishedShareItem
	for _, s := range c.cfg.PublishedShares {
		if s.ShareID != shareID {
			remaining = append(remaining, s)
		}
	}
	c.cfg.PublishedShares = remaining
	_ = config.SaveEncryptedConfig(c.cfg)
	c.mu.Unlock()

	c.logger.Printf("已从云端吊销分享: shareID=%s", shareID)
	return nil
}

func (c *controller) listMyShares() ([]cloud.ShareSummary, error) {
	c.mu.Lock()
	cfg := c.cfg
	state := c.state
	link := c.link
	c.mu.Unlock()

	if strings.TrimSpace(cfg.CloudHubURL) == "" || strings.TrimSpace(cfg.OwnerAdminToken) == "" {
		return []cloud.ShareSummary{}, nil
	}
	if !link.Ready() || (network.IsCampusLink(link) && (state == "offline" || state == "link-down" || state == "retry-wait" || state == "config-required")) {
		return []cloud.ShareSummary{}, errors.New("网络未连通，无法获取云端分享")
	}

	cloudClient := cloud.NewClient(cfg.CloudHubURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	shares, err := cloudClient.ListMyShares(ctx, cfg.OwnerAdminToken)
	if err != nil {
		return nil, err
	}
	codeByID := make(map[string]string, len(cfg.PublishedShares))
	for _, item := range cfg.PublishedShares {
		if item.ShareCode != "" {
			codeByID[item.ShareID] = item.ShareCode
		}
	}
	for i := range shares {
		if code, ok := codeByID[shares[i].ShareID]; ok {
			shares[i].ShareCode = code
		}
	}
	return shares, nil
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
	activeMode := string(c.cfg.ActiveMode)
	if activeMode == "" {
		activeMode = string(config.ModeSelf)
	}
	c.mu.Unlock()

	c.ui.UpdateDashboard(st, netSummary, opName, ipv4, isCampus, c.state, activeMode)
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
		c.logger.Printf("Wi-Fi 连接失败：%v", err)
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
	if err == nil {
		if site := campus.CampusFromFingerprint(c.link.IPv4, gateway, mac); site != "" {
			c.campus = site
			c.logger.Printf("本地网络特征匹配%s：网关 %s，网关 MAC %s；当前 IPv4 %s", campus.CampusName(site), gateway, mac, c.link.IPv4)
			return
		}
	}
	if site := campus.CampusFromIP(c.link.IPv4); site != "" {
		c.campus = site
		c.logger.Printf("本地 IPv4 网段匹配%s（网关 ARP 学习中）：当前 IPv4 %s", campus.CampusName(site), c.link.IPv4)
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

func (c *controller) isAutoUpdateEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.AutoUpdateEnabled()
}

func (c *controller) toggleAutoUpdate() (bool, error) {
	candidate := func() config.Config {
		c.mu.Lock()
		defer c.mu.Unlock()
		next := c.cfg
		next.DisableAutoUpdate = !c.cfg.DisableAutoUpdate
		return next
	}()

	if err := config.SaveEncryptedConfig(candidate); err != nil {
		return !candidate.DisableAutoUpdate, err
	}

	c.mu.Lock()
	c.cfg = candidate
	c.mu.Unlock()

	enabled := candidate.AutoUpdateEnabled()
	if enabled && c.updateTrigger != nil {
		select {
		case c.updateTrigger <- struct{}{}:
		default:
		}
	}
	return enabled, nil
}

func (c *controller) runUpdateLoop(ctx context.Context) {
	if c.updateTrigger == nil {
		c.updateTrigger = make(chan struct{}, 1)
	}
	// Initial delayed check shortly after startup
	select {
	case <-ctx.Done():
		return
	case <-time.After(20 * time.Second):
	case <-c.updateTrigger:
	}
	c.checkForUpdates(ctx)

	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkForUpdates(ctx)
		case <-c.updateTrigger:
			c.checkForUpdates(ctx)
		}
	}
}

func (c *controller) checkForUpdates(ctx context.Context) {
	if !c.isAutoUpdateEnabled() {
		return
	}
	c.mu.Lock()
	online := c.state == "online"
	notified := c.notifiedVersion
	c.mu.Unlock()
	if !online {
		return
	}

	client := updater.NewClient(config.AppVersion)
	info, hasUpdate, err := client.CheckLatest(ctx, config.AppVersion)
	if err != nil || !hasUpdate || info == nil {
		return
	}
	if info.Version == notified {
		return
	}

	c.mu.Lock()
	c.notifiedVersion = info.Version
	c.mu.Unlock()

	c.logger.Printf("发现新版本：%s（当前 v%s），下载地址：%s", info.Version, config.AppVersion, info.ReleaseURL)
	if c.ui != nil {
		c.ui.SetAvailableUpdate(info)
	}
}

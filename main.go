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
	quiet        bool
	state        string
	lastPortal   string
	link         linkState
	backoff      time.Duration
	nextRetry    time.Time
	retryTimer   *time.Timer
	retryChannel <-chan time.Time
	nextReset    time.Time
}

func main() {
	quiet := flag.Bool("quiet", false, "only log changes and actions (for scheduled task)")
	once := flag.Bool("once", false, "one check and one login attempt")
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := &controller{
		cfg: cfg, portal: newPortalClient(cfg), logger: logger,
		quiet: *quiet, backoff: retryBase, retryTimer: time.NewTimer(time.Hour),
	}
	if !c.retryTimer.Stop() {
		select {
		case <-c.retryTimer.C:
		default:
		}
	}
	c.nextReset = nextSix(time.Now())
	logger.Printf("校园网守护程序启动；账号 %s；外网检测 %s；链路核查 %s；退避每天 06:00 清零", mask(cfg.account), healthInterval, linkInterval)
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
	_ = c.cycle(ctx, "启动检查")
	for {
		select {
		case <-ctx.Done():
			c.logger.Print("收到停止信号，守护程序退出")
			return ctx.Err()
		case link := <-linkChanges:
			if link != c.link {
				c.link = link
				_ = c.cycle(ctx, "有线接口变化")
			}
		case <-healthTicker.C:
			c.resetAtSixIfDue()
			_ = c.cycle(ctx, "定期外网检查")
		case <-c.retryChannel:
			c.retryChannel = nil
			c.resetAtSixIfDue()
			_ = c.cycle(ctx, "认证重试")
		case <-statusTicker.C:
			if c.resetAtSixIfDue() && c.state == "offline" {
				_ = c.cycle(ctx, "06:00 退避清零")
			}
			if !c.quiet {
				c.logger.Printf("当前状态：%s", c.status())
			}
		case <-sixTimer.C:
			reset := c.resetAtSixIfDue()
			sixTimer.Reset(time.Until(c.nextReset))
			if reset && c.state == "offline" {
				_ = c.cycle(ctx, "06:00 退避清零")
			}
		}
	}
}

func (c *controller) cycle(ctx context.Context, reason string) error {
	c.resetAtSixIfDue()
	c.refreshLink()
	if !c.link.ready() {
		c.setState("link-down", fmt.Sprintf("有线接口未就绪（%s）；等待网线、网卡或 DHCP 恢复", c.link.describe()))
		return errors.New("有线接口未就绪")
	}
	if !c.quiet {
		c.logger.Printf("%s：正在检测 IPv4 外网", reason)
	}
	result := c.portal.probe(ctx)
	if result.online {
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
	if !c.link.ready() {
		c.setState("link-down", "有线接口未就绪："+c.link.describe())
		return errors.New("有线接口未就绪")
	}
	c.setState("offline", "IPv4 外网中断："+result.reason)
	if !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry) {
		if !c.quiet {
			c.logger.Printf("认证退避中，预计 %s 后重试", time.Until(c.nextRetry).Round(time.Second))
		}
		return errors.New("等待认证重试")
	}
	portal := c.lastPortal
	if portal == "" {
		portal = c.cfg.portalURL
	}
	if portal == "" {
		var err error
		portal, err = c.portal.discover(ctx)
		if err != nil {
			c.scheduleRetry("已连接校园网接口，但未发现认证跳转")
			return err
		}
		c.lastPortal = portal
	}
	c.refreshLink()
	if !c.link.ready() {
		c.setState("link-down", "认证前有线接口断开："+c.link.describe())
		return errors.New("有线接口未就绪")
	}
	c.logger.Printf("尝试校园网认证：%s", safePortal(portal))
	if err := c.portal.login(ctx, portal, c.link.ipv4); err != nil {
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
		if !c.link.ready() {
			c.setState("link-down", "认证核验期间有线接口断开："+c.link.describe())
			return errors.New("有线接口未就绪")
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
}

func (c *controller) status() string {
	s := stateName(c.state) + "；" + c.link.describe()
	if !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry) {
		s += "；下次认证约 " + time.Until(c.nextRetry).Round(time.Second).String() + " 后"
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
		return "网线或接口未就绪"
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
	c.nextReset = nextSix(now)
	c.logger.Printf("已到每天 06:00：指数退避清零；下次清零时间 %s", c.nextReset.Format("2006-01-02 15:04"))
	if !c.nextRetry.IsZero() {
		c.nextRetry = time.Time{}
		c.retryChannel = nil
		c.retryTimer.Stop()
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

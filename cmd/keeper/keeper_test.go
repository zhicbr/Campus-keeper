package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"wenchang-campus-keeper/internal/campus"
	"wenchang-campus-keeper/internal/config"
	"wenchang-campus-keeper/internal/network"
	"wenchang-campus-keeper/internal/portal"
)

func TestSixAMResetBoundary(t *testing.T) {
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	for _, tc := range []struct {
		at, want string
	}{
		{"2026-09-30 05:59:59", "2026-09-30 06:00:00"},
		{"2026-09-30 06:00:00", "2026-10-01 06:00:00"},
		{"2026-09-30 23:59:59", "2026-10-01 06:00:00"},
	} {
		at, _ := time.ParseInLocation("2006-01-02 15:04:05", tc.at, china)
		got := nextSix(at).In(china).Format("2006-01-02 15:04:05")
		if got != tc.want {
			t.Fatalf("nextSix(%s) = %s, want %s", tc.at, got, tc.want)
		}
	}
	c := &controller{backoff: defaultPolicy.RetryMax, nextReset: time.Now().Add(-time.Second), logger: log.New(io.Discard, "", 0)}
	c.resetAtSixIfDue()
	if c.backoff != defaultPolicy.RetryBase {
		t.Fatalf("06:00 reset left backoff at %s", c.backoff)
	}
}

func TestCustomDailyResetPolicy(t *testing.T) {
	c := newController(config.Config{}, nil, log.New(io.Discard, "", 0), true,
		WithDailyReset(7, 30),
		WithRetryPolicy(3*time.Second, 10*time.Minute, 2*time.Minute),
	)
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	at, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-09-30 07:00:00", china)
	got := c.nextDailyReset(at).In(china).Format("2006-01-02 15:04:05")
	if got != "2026-09-30 07:30:00" {
		t.Fatalf("custom reset got %s, want 2026-09-30 07:30:00", got)
	}

	// Unspecified options must retain full valid positive defaults (no zero-value ticker panic)
	p := c.getPolicy()
	if p.HealthInterval <= 0 || p.StatusInterval <= 0 || p.LinkInterval <= 0 {
		t.Fatalf("unspecified policy fields must retain positive defaults: %+v", p)
	}

	c.backoff = 10 * time.Minute
	c.nextReset = time.Now().Add(-time.Second)
	c.resetAtSixIfDue()
	if c.backoff != 3*time.Second {
		t.Fatalf("custom policy reset left backoff at %s, want 3s", c.backoff)
	}
}

func TestOptionNonPositiveValidation(t *testing.T) {
	c := newController(config.Config{}, nil, log.New(io.Discard, "", 0), true,
		WithHealthInterval(0),
		WithLinkInterval(-5*time.Second),
		WithDailyReset(99, 99),
		WithHandshakeGracePeriod(-10*time.Second),
		WithMaxConsecutiveRoams(0),
	)
	p := c.getPolicy()
	if p.HealthInterval != defaultPolicy.HealthInterval {
		t.Fatalf("zero HealthInterval should not override default, got %v", p.HealthInterval)
	}
	if p.LinkInterval != defaultPolicy.LinkInterval {
		t.Fatalf("negative LinkInterval should not override default, got %v", p.LinkInterval)
	}
	if p.DailyResetHour != defaultPolicy.DailyResetHour || p.DailyResetMinute != defaultPolicy.DailyResetMinute {
		t.Fatalf("out-of-range daily reset should not override default, got %d:%d", p.DailyResetHour, p.DailyResetMinute)
	}
	if p.HandshakeGracePeriod != defaultPolicy.HandshakeGracePeriod {
		t.Fatalf("negative HandshakeGracePeriod should not override default, got %v", p.HandshakeGracePeriod)
	}
	if p.MaxConsecutiveRoams != defaultPolicy.MaxConsecutiveRoams {
		t.Fatalf("zero MaxConsecutiveRoams should not override default, got %d", p.MaxConsecutiveRoams)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCancelledCycleDoesNotProbeOrLogin(t *testing.T) {
	p := portal.NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request after cancellation")
		return nil, nil
	})})
	c := &controller{portal: p}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.cycle(ctx, "exit"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestNetworkSwitchDropsCampusCredentialsContext(t *testing.T) {
	c := &controller{
		link:       network.LinkState{Index: 1, IPv4: "10.52.1.2"},
		state:      "online",
		campus:     campus.Wenchang,
		lastPortal: "http://10.2.5.251/a79.htm",
		backoff:    defaultPolicy.RetryMax,
		nextRetry:  time.Now().Add(time.Minute),
		retryTimer: time.NewTimer(time.Hour),
	}
	stopTimer(c.retryTimer)

	newLink := network.LinkState{Index: 2, IPv4: "192.168.1.100"}
	c.adoptLink(newLink)
	if c.state != "checking" {
		t.Fatalf("switch retained old online status: %q", c.state)
	}

	if c.campus != "" {
		t.Fatalf("expected campus to be reset, got %v", c.campus)
	}
	if c.lastPortal != "" {
		t.Fatalf("expected lastPortal to be reset, got %v", c.lastPortal)
	}
	if !c.nextRetry.IsZero() {
		t.Fatalf("expected nextRetry to be zero, got %v", c.nextRetry)
	}
	if !c.failureStart.IsZero() {
		t.Fatalf("expected failureStart to be zero, got %v", c.failureStart)
	}
}

func TestProbeResultBelongsToCurrentNetwork(t *testing.T) {
	old := network.LinkState{Index: 1, Up: true, IPv4: "10.52.1.2"}
	if !probeLinkCurrent(old, old) {
		t.Fatal("same ready network rejected")
	}
	if probeLinkCurrent(old, network.LinkState{Index: 2, Up: true, IPv4: "192.168.1.2"}) {
		t.Fatal("probe result accepted after network switch")
	}
	if probeLinkCurrent(old, network.LinkState{Index: 1, IPv4: "10.52.1.2"}) {
		t.Fatal("probe result accepted after link went down")
	}
}

func TestStaleProbeSchedulesCheckWhenMonitorEventIsSkipped(t *testing.T) {
	old := network.LinkState{Index: 1, Up: true, IPv4: "10.52.1.2"}
	newLink := network.LinkState{Index: 2, Up: true, IPv4: "10.3.1.2"}
	c := &controller{link: old, state: "online", linkRecheck: make(chan struct{}, 1)}
	c.adoptLink(newLink) // verifyProbeLink already observed the switch
	if err := c.acceptProbeLink(old); err == nil {
		t.Fatal("old probe result was accepted")
	}
	if err := c.acceptProbeLink(old); err == nil {
		t.Fatal("repeated old probe result was accepted")
	}
	if len(c.linkRecheck) != 1 {
		t.Fatalf("got %d queued checks, want one", len(c.linkRecheck))
	}
	if newLink != c.link {
		t.Fatal("monitor event would not be skipped in this scenario")
	}
	select {
	case <-c.linkRecheck:
		// The run loop can check the new link immediately despite the skipped event.
	default:
		t.Fatal("new network check was not scheduled")
	}
}

func TestRecoveryStatesHaveDistinctDescriptions(t *testing.T) {
	for state, want := range map[string]string{
		"checking":        "检测中",
		"offline":         "未连接",
		"authenticating":  "正在认证",
		"retry-wait":      "等待重试",
		"config-required": "需要配置账号",
	} {
		if got := stateName(state); got != want {
			t.Errorf("stateName(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestInitialFastRetryPeriodDoesNotBackoff(t *testing.T) {
	c := &controller{
		logger:     log.New(io.Discard, "", 0),
		backoff:    defaultPolicy.RetryBase,
		retryTimer: time.NewTimer(time.Hour),
	}
	stopTimer(c.retryTimer)

	// Simulate repeated failures within the first minute
	for i := 0; i < 5; i++ {
		c.scheduleRetry("failure")
		if c.backoff != defaultPolicy.RetryBase {
			t.Fatalf("attempt %d: backoff grew to %v during initial fast retry window", i, c.backoff)
		}
		if time.Until(c.nextRetry) > defaultPolicy.RetryBase+2*time.Second {
			t.Fatalf("attempt %d: delay too large (%v) during fast retry window", i, time.Until(c.nextRetry))
		}
	}

	// Simulate failure occurring after fastRetryWindow has passed
	c.failureStart = time.Now().Add(-defaultPolicy.FastRetryWindow - 10*time.Second)
	c.scheduleRetry("failure after 2min")
	if c.backoff <= defaultPolicy.RetryBase {
		t.Fatalf("expected backoff to grow after fast retry window elapsed, got %v", c.backoff)
	}
}

func TestWiFiFastRetryPeriod(t *testing.T) {
	c := &controller{
		logger:      log.New(io.Discard, "", 0),
		wifiBackoff: defaultPolicy.RetryBase,
		wifiTimer:   time.NewTimer(time.Hour),
	}
	stopTimer(c.wifiTimer)

	// In initial window, wifi backoff must not grow
	for i := 0; i < 4; i++ {
		c.scheduleWiFiRetry(true)
		if c.wifiBackoff != defaultPolicy.RetryBase {
			t.Fatalf("attempt %d: wifiBackoff grew to %v during initial fast retry window", i, c.wifiBackoff)
		}
	}

	// After fastRetryWindow elapsed, wifi backoff should double
	c.wifiFailureStart = time.Now().Add(-defaultPolicy.FastRetryWindow - 10*time.Second)
	c.scheduleWiFiRetry(true)
	if c.wifiBackoff <= defaultPolicy.RetryBase {
		t.Fatalf("expected wifiBackoff to grow after fast retry window, got %v", c.wifiBackoff)
	}
}

func TestManualReconnectResetsBackoff(t *testing.T) {
	p := portal.NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("Microsoft Connect Test")), Header: make(http.Header)}, nil
	})})

	c := &controller{
		portal:       p,
		logger:       log.New(io.Discard, "", 0),
		backoff:      defaultPolicy.RetryMax,
		nextRetry:    time.Now().Add(5 * time.Minute), // in backoff
		failureStart: time.Now().Add(-10 * time.Minute),
		retryTimer:   time.NewTimer(time.Hour),
	}
	stopTimer(c.retryTimer)

	ctx := context.Background()
	// Calling cycle with "手动重连" should clear nextRetry and succeed
	err := c.cycle(ctx, "手动重连")
	if err != nil {
		t.Fatalf("manual reconnect failed: %v", err)
	}
	if !c.nextRetry.IsZero() {
		t.Fatalf("expected nextRetry to be cleared on manual reconnect, got %v", c.nextRetry)
	}
	if c.backoff != defaultPolicy.RetryBase {
		t.Fatalf("expected backoff reset to retryBase, got %v", c.backoff)
	}
}

func TestCampusDetectionFromIPFallback(t *testing.T) {
	c := &controller{
		cfg:    config.Config{CampusMode: campus.Auto},
		link:   network.LinkState{Index: 1, Up: true, Kind: "wifi", IPv4: "10.3.101.56", SSID: "CUMT_Stu"},
		logger: log.New(io.Discard, "", 0),
	}
	c.detectCampusFromLink()
	if c.campus != campus.Nanhu {
		t.Fatalf("expected campus Nanhu from 10.3.101.56, got %s", c.campus)
	}

	c2 := &controller{
		cfg:    config.Config{CampusMode: campus.Auto},
		link:   network.LinkState{Index: 2, Up: true, Kind: "wired", IPv4: "10.52.130.12"},
		logger: log.New(io.Discard, "", 0),
	}
	c2.detectCampusFromLink()
	if c2.campus != campus.Wenchang {
		t.Fatalf("expected campus Wenchang from 10.52.130.12, got %s", c2.campus)
	}
}

func TestCampusLinkFallbackWhenNoRedirectEvidence(t *testing.T) {
	loginAttempted := false
	p := portal.NewClient(config.Config{
		Account:    "testuser",
		Password:   "testpass",
		CampusMode: campus.Auto,
	})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "login") || strings.Contains(r.URL.Path, "eportal") || strings.Contains(r.URL.Host, "10.2.5.251") {
			loginAttempted = true
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`dr1003({"result":1})`)),
				Header:     make(http.Header),
			}, nil
		}
		if loginAttempted && strings.Contains(r.URL.Path, "connecttest.txt") {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("Microsoft Connect Test")),
				Header:     make(http.Header),
			}, nil
		}
		return nil, errors.New("timeout")
	})})

	pol := defaultPolicy
	pol.LinkDebounce = time.Millisecond
	pol.PostAuthVerifyAttempts = 1
	pol.PostAuthVerifyInterval = time.Millisecond
	c := &controller{
		policy: pol,
		portal: p,
		cfg: config.Config{
			Account:    "testuser",
			Password:   "testpass",
			CampusMode: campus.Auto,
		},
		link: network.LinkState{
			Index: 1,
			Up:    true,
			Kind:  "wifi",
			SSID:  "CUMT_Stu",
			IPv4:  "10.3.101.56",
		},
		readLink: func(prev network.LinkState) network.LinkState {
			return prev
		},
		logger:     log.New(io.Discard, "", 0),
		backoff:    defaultPolicy.RetryBase,
		retryTimer: time.NewTimer(time.Hour),
		nextReset:  time.Now().Add(24 * time.Hour),
	}
	defer stopTimer(c.retryTimer)

	ctx := context.Background()
	_ = c.cycle(ctx, "测试校园网回退认证")
	if !loginAttempted {
		t.Fatal("expected login attempt using fallback portal on known campus link")
	}
}


func TestSilentProbeDuringBackoff(t *testing.T) {
	var logBuf bytes.Buffer
	p := portal.NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})})

	c := &controller{
		portal:     p,
		logger:     log.New(&logBuf, "", 0),
		state:      "retry-wait",
		nextRetry:  time.Now().Add(5 * time.Minute), // in backoff
		retryTimer: time.NewTimer(time.Hour),
		link: network.LinkState{
			Index: 1,
			Up:    true,
			Kind:  "wifi",
			SSID:  "CUMT_Stu",
			IPv4:  "10.3.101.56",
		},
		readLink: func(prev network.LinkState) network.LinkState {
			return prev
		},
		nextReset: time.Now().Add(24 * time.Hour),
	}
	defer stopTimer(c.retryTimer)

	ctx := context.Background()
	err := c.cycle(ctx, "定期外网检查")
	if err != nil {
		t.Fatalf("expected silent return nil during backoff, got %v", err)
	}
	if c.state != "retry-wait" {
		t.Fatalf("expected state to remain retry-wait, got %s", c.state)
	}
	if logBuf.Len() > 0 {
		t.Fatalf("expected 0 log output during silent opportunistic probe, got: %s", logBuf.String())
	}
}

func TestOpportunisticRecoveryDuringBackoff(t *testing.T) {
	p := portal.NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("Microsoft Connect Test")),
			Header:     make(http.Header),
		}, nil
	})})

	c := &controller{
		portal:     p,
		logger:     log.New(io.Discard, "", 0),
		state:      "retry-wait",
		nextRetry:  time.Now().Add(5 * time.Minute), // in backoff
		retryTimer: time.NewTimer(time.Hour),
		link: network.LinkState{
			Index: 1,
			Up:    true,
			Kind:  "wifi",
			SSID:  "CUMT_Stu",
			IPv4:  "10.3.101.56",
		},
		readLink: func(prev network.LinkState) network.LinkState {
			return prev
		},
		nextReset: time.Now().Add(24 * time.Hour),
	}
	defer stopTimer(c.retryTimer)

	ctx := context.Background()
	err := c.cycle(ctx, "定期外网检查")
	if err != nil {
		t.Fatalf("expected nil on recovery, got %v", err)
	}
	if c.state != "online" {
		t.Fatalf("expected state to become online on opportunistic recovery, got %s", c.state)
	}
	if !c.nextRetry.IsZero() {
		t.Fatalf("expected nextRetry to be cleared, got %v", c.nextRetry)
	}
}


func TestSilentProbeDoesNotHijackWhenBackoffExpiresMidProbe(t *testing.T) {
	var logBuf bytes.Buffer
	c := &controller{
		logger:    log.New(&logBuf, "", 0),
		state:     "retry-wait",
		nextRetry: time.Now().Add(40 * time.Millisecond), // expires mid-probe
		retryTimer: time.NewTimer(time.Hour),
		link: network.LinkState{
			Index: 1,
			Up:    true,
			Kind:  "wifi",
			SSID:  network.StudentWiFiProfile,
			IPv4:  "10.3.101.56",
		},
		readLink: func(prev network.LinkState) network.LinkState {
			return prev
		},
		nextReset: time.Now().Add(24 * time.Hour),
	}
	defer stopTimer(c.retryTimer)

	p := portal.NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		time.Sleep(80 * time.Millisecond) // slow probe exceeds 40ms nextRetry
		return nil, errors.New("network down")
	})})
	c.portal = p

	err := c.cycle(context.Background(), "定期外网检查")
	if err != nil {
		t.Fatalf("expected silent nil return when probe started in backoff, got %v", err)
	}
	if c.state != "retry-wait" {
		t.Fatalf("expected state retry-wait, got %s", c.state)
	}
	if logBuf.Len() > 0 {
		t.Fatalf("expected zero log spam, got: %s", logBuf.String())
	}
}

func TestContinuingEduSubnetAuthenticatesWithInterfaceMAC(t *testing.T) {
	var loginURL string
	authenticated := false
	p := portal.NewClient(config.Config{
		Account:    "TS26170082",
		Password:   "pass",
		Operator:   config.OperatorCMCC,
		CampusMode: campus.Auto,
	})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "10.2.5.251:801" && r.URL.Query().Get("a") == "login" {
			loginURL = r.URL.String()
			authenticated = true
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`dr1003({"result":1})`)),
				Header:     make(http.Header),
			}, nil
		}
		if authenticated && strings.Contains(r.URL.Path, "connecttest.txt") {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("Microsoft Connect Test")),
				Header:     make(http.Header),
			}, nil
		}
		if strings.Contains(r.URL.Path, "connecttest.txt") {
			h := make(http.Header)
			h.Set("Location", "http://10.2.5.251/a79.htm?wlanuserip=10.27.176.53&wlanacname=AC_JXJY")
			return &http.Response{
				StatusCode: 302,
				Header:     h,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}
		return nil, errors.New("offline")
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})

	pol := defaultPolicy
	pol.LinkDebounce = time.Millisecond
	pol.PostAuthVerifyAttempts = 1
	pol.PostAuthVerifyInterval = time.Millisecond
	c := &controller{
		policy: pol,
		portal: p,
		cfg: config.Config{
			Account:    "TS26170082",
			Password:   "pass",
			Operator:   config.OperatorCMCC,
			CampusMode: campus.Auto,
		},
		link: network.LinkState{
			Index: 1,
			Up:    true,
			Kind:  "wifi",
			SSID:  "CUMT_Stu",
			IPv4:  "10.27.176.53",
			MAC:   "AA-BB-CC-DD-EE-FF",
		},
		readLink: func(prev network.LinkState) network.LinkState {
			return prev
		},
		logger:     log.New(io.Discard, "", 0),
		backoff:    defaultPolicy.RetryBase,
		retryTimer: time.NewTimer(time.Hour),
		nextReset:  time.Now().Add(24 * time.Hour),
	}
	defer stopTimer(c.retryTimer)

	err := c.cycle(context.Background(), "切换账号重连")
	if err != nil {
		t.Fatalf("expected ContinuingEdu 10.27.x.x campus link to authenticate, got %v", err)
	}
	if !strings.Contains(loginURL, "wlan_user_ip=10.27.176.53") ||
		!strings.Contains(loginURL, "wlan_user_mac=AABBCCDDEEFF") ||
		!strings.Contains(loginURL, "wlan_ac_name=AC_JXJY") {
		t.Fatalf("unexpected login query for ContinuingEdu campus: %s", loginURL)
	}
	if c.campus != campus.ContinuingEdu || c.state != "online" {
		t.Fatalf("expected ContinuingEdu online state, got campus=%q state=%q", c.campus, c.state)
	}
}

func TestUnknownSubnetRejectedWithoutFallback(t *testing.T) {
	loginAttempted := false
	p := portal.NewClient(config.Config{
		Account:    "TS26170082",
		Password:   "pass",
		Operator:   config.OperatorCMCC,
		CampusMode: campus.Auto,
	})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "10.2.5.251:801" && r.URL.Query().Get("a") == "login" {
			loginAttempted = true
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`dr1003({"result":1})`)),
				Header:     make(http.Header),
			}, nil
		}
		if strings.Contains(r.URL.Path, "connecttest.txt") {
			h := make(http.Header)
			h.Set("Location", "http://10.2.5.251/a79.htm?wlanuserip=10.99.1.2")
			return &http.Response{
				StatusCode: 302,
				Header:     h,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}
		return nil, errors.New("offline")
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})

	pol := defaultPolicy
	pol.LinkDebounce = time.Millisecond
	c := &controller{
		policy: pol,
		portal: p,
		cfg: config.Config{
			Account:    "TS26170082",
			Password:   "pass",
			Operator:   config.OperatorCMCC,
			CampusMode: campus.Auto,
		},
		link: network.LinkState{
			Index: 1,
			Up:    true,
			Kind:  "wifi",
			SSID:  "CUMT_Stu",
			IPv4:  "10.99.1.2",
			MAC:   "AA-BB-CC-DD-EE-FF",
		},
		readLink: func(prev network.LinkState) network.LinkState {
			return prev
		},
		logger:     log.New(io.Discard, "", 0),
		backoff:    defaultPolicy.RetryBase,
		retryTimer: time.NewTimer(time.Hour),
		nextReset:  time.Now().Add(24 * time.Hour),
	}
	defer stopTimer(c.retryTimer)

	err := c.cycle(context.Background(), "切换账号重连")
	if err == nil {
		t.Fatal("expected uncatalogued 10.99.1.2 subnet to fail without generic fallback")
	}
	if loginAttempted {
		t.Fatal("unknown subnet must not submit credentials via generic fallback")
	}
}

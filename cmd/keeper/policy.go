package main

import (
	"log"
	"time"

	"wenchang-campus-keeper/internal/config"
	"wenchang-campus-keeper/internal/portal"
)

// DaemonPolicy holds operational timings, thresholds and policies for the keeper daemon.
type DaemonPolicy struct {
	// Probe and monitoring intervals
	HealthInterval       time.Duration // Period between health checks (default: 20s)
	LinkInterval         time.Duration // Periodic adapter verification poll (default: 5s)
	StatusInterval       time.Duration // Status summary check interval (default: 10s)
	OnlineStatusInterval time.Duration // Throttled status log interval while continuously online (default: 1m)

	// Debounce and verification durations
	LinkDebounce           time.Duration // Wait time to absorb transient network card disappears (default: 2s)
	PostAuthVerifyAttempts int           // Verification probe retries after login response (default: 5)
	PostAuthVerifyInterval time.Duration // Interval between verification probes (default: 3s)

	// Wi-Fi roaming and gateway health
	DeadGatewayThreshold int           // Consecutive missing ARP count before triggering AP roam (default: 2)
	WiFiResetCooldown    time.Duration // Cooldown between automatic Wi-Fi reset triggers (default: 15s)
	WiFiInitialDelay     time.Duration // Initial retry delay for Wi-Fi reconnection (default: 3s)
	HandshakeGracePeriod time.Duration // Grace period after link change before checking dead gateway (default: 3s)
	MaxConsecutiveRoams  int           // Maximum consecutive AP roams before circuit breaker trips (default: 4)
	RoamDecayDuration    time.Duration // Time window after last roam before resetting consecutive roam count (default: 2m)

	// Retry & Backoff
	RetryBase       time.Duration // Initial retry base for authentication failure (default: 5s)
	RetryMax        time.Duration // Upper limit for exponential backoff (default: 5m)
	FastRetryWindow time.Duration // Initial grace period before entering exponential backoff (default: 2m)

	// Scheduled daily reset
	DailyResetHour   int // Daily backoff reset hour in Beijing Time (06:00)
	DailyResetMinute int
}

var defaultPolicy = DaemonPolicy{
	HealthInterval:       20 * time.Second,
	LinkInterval:         5 * time.Second,
	StatusInterval:       10 * time.Second,
	OnlineStatusInterval: time.Minute,

	LinkDebounce:           2 * time.Second,
	PostAuthVerifyAttempts: 5,
	PostAuthVerifyInterval: 3 * time.Second,

	DeadGatewayThreshold: 2,
	WiFiResetCooldown:    15 * time.Second,
	WiFiInitialDelay:     3 * time.Second,
	HandshakeGracePeriod: 3 * time.Second,
	MaxConsecutiveRoams:  4,
	RoamDecayDuration:    2 * time.Minute,

	RetryBase:       5 * time.Second,
	RetryMax:        5 * time.Minute,
	FastRetryWindow: 2 * time.Minute,

	DailyResetHour:   6,
	DailyResetMinute: 0,
}

// Option configures DaemonPolicy for the controller.
type Option func(*DaemonPolicy)

// WithHealthInterval sets the periodic external health check interval.
func WithHealthInterval(d time.Duration) Option {
	return func(p *DaemonPolicy) {
		if d > 0 {
			p.HealthInterval = d
		}
	}
}

// WithLinkInterval sets the local network interface check interval.
func WithLinkInterval(d time.Duration) Option {
	return func(p *DaemonPolicy) {
		if d > 0 {
			p.LinkInterval = d
		}
	}
}

// WithStatusInterval sets the status summary check interval.
func WithStatusInterval(d time.Duration) Option {
	return func(p *DaemonPolicy) {
		if d > 0 {
			p.StatusInterval = d
		}
	}
}

// WithOnlineStatusInterval sets the interval for status logging while online.
func WithOnlineStatusInterval(d time.Duration) Option {
	return func(p *DaemonPolicy) {
		if d > 0 {
			p.OnlineStatusInterval = d
		}
	}
}

// WithRetryPolicy sets authentication failure backoff parameters.
func WithRetryPolicy(base, max, fastWindow time.Duration) Option {
	return func(p *DaemonPolicy) {
		if base > 0 {
			p.RetryBase = base
		}
		if max > 0 {
			p.RetryMax = max
		}
		if fastWindow > 0 {
			p.FastRetryWindow = fastWindow
		}
	}
}

// WithDailyReset sets the daily scheduled reset time in Beijing Time.
func WithDailyReset(hour, minute int) Option {
	return func(p *DaemonPolicy) {
		if hour >= 0 && hour < 24 {
			p.DailyResetHour = hour
		}
		if minute >= 0 && minute < 60 {
			p.DailyResetMinute = minute
		}
	}
}

// WithWiFiPolicy sets AP roaming and connection retry thresholds.
func WithWiFiPolicy(deadThreshold int, cooldown, initialDelay time.Duration) Option {
	return func(p *DaemonPolicy) {
		if deadThreshold > 0 {
			p.DeadGatewayThreshold = deadThreshold
		}
		if cooldown > 0 {
			p.WiFiResetCooldown = cooldown
		}
		if initialDelay > 0 {
			p.WiFiInitialDelay = initialDelay
		}
	}
}

// WithHandshakeGracePeriod sets the grace period after connecting before evaluating gateway health.
func WithHandshakeGracePeriod(d time.Duration) Option {
	return func(p *DaemonPolicy) {
		if d > 0 {
			p.HandshakeGracePeriod = d
		}
	}
}

// WithMaxConsecutiveRoams sets the circuit breaker limit for consecutive AP roams.
func WithMaxConsecutiveRoams(n int) Option {
	return func(p *DaemonPolicy) {
		if n > 0 {
			p.MaxConsecutiveRoams = n
		}
	}
}

// WithRoamDecayDuration sets the time window to reset consecutive roam count.
func WithRoamDecayDuration(d time.Duration) Option {
	return func(p *DaemonPolicy) {
		if d > 0 {
			p.RoamDecayDuration = d
		}
	}
}

func (c *controller) getPolicy() DaemonPolicy {
	if c.policy.HealthInterval <= 0 {
		return defaultPolicy
	}
	return c.policy
}

func newController(cfg config.Config, p *portal.Client, logger *log.Logger, quiet bool, opts ...Option) *controller {
	pol := defaultPolicy
	for _, opt := range opts {
		opt(&pol)
	}
	c := &controller{
		policy:           pol,
		cfg:              cfg,
		portal:           p,
		logger:           logger,
		quiet:            quiet,
		backoff:          pol.RetryBase,
		retryTimer:       time.NewTimer(time.Hour),
		wifiBackoff:      pol.RetryBase,
		wifiTimer:        time.NewTimer(time.Hour),
		reconnectTrigger: make(chan struct{}, 1),
		linkRecheck:      make(chan struct{}, 1),
		linkConnectedAt:  time.Now(),
	}
	stopTimer(c.retryTimer)
	stopTimer(c.wifiTimer)
	c.nextReset = c.nextDailyReset(time.Now())
	return c
}

func (c *controller) nextDailyReset(now time.Time) time.Time {
	return nextDailyReset(now, c.getPolicy())
}

func nextDailyReset(now time.Time, p DaemonPolicy) time.Time {
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	local := now.In(china)
	hour := p.DailyResetHour
	minute := p.DailyResetMinute
	next := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, china)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// nextSix is preserved for backward compatibility and test access using defaultPolicy.
func nextSix(now time.Time) time.Time {
	return nextDailyReset(now, defaultPolicy)
}

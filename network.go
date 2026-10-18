package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

type linkState struct {
	index int
	name  string
	kind  string
	ssid  string
	up    bool
	ipv4  string
	mac   string
}

func (s linkState) ready() bool { return s.index != 0 && s.up && s.ipv4 != "" }

func (s linkState) describe() string {
	if s.index == 0 {
		return "未找到校园网物理网卡"
	}
	if !s.up {
		return fmt.Sprintf("%s 已断开", s.name)
	}
	if s.ipv4 == "" {
		return fmt.Sprintf("%s 已连接，等待 IPv4 地址", s.name)
	}
	if s.kind == "wifi" && s.ssid != "" {
		return fmt.Sprintf("%s 已连接到 %s，IPv4 %s", s.name, s.ssid, s.ipv4)
	}
	return fmt.Sprintf("%s 已连接，IPv4 %s", s.name, s.ipv4)
}

func (s linkState) sameNetwork(other linkState) bool {
	return s.index == other.index && s.kind == other.kind && s.ssid == other.ssid && s.up == other.up && s.ipv4 == other.ipv4 && s.mac == other.mac
}

func (c *controller) refreshLink() bool {
	next := readLink(c.link)
	if c.portal != nil && c.portal.diagnostic != nil {
		c.portal.diagnostic.log.Printf("controller raw link previous=%+v next=%+v", c.link, next)
	}
	return c.adoptLink(next)
}

func (c *controller) adoptLink(next linkState) bool {
	if next == c.link {
		return false
	}
	if !next.sameNetwork(c.link) {
		hadContext := c.campus != "" || c.lastPortal != "" || !c.nextRetry.IsZero()
		c.lastPortal = ""
		c.campus = ""
		c.resetRetry()
		if hadContext && c.logger != nil {
			c.logger.Print("网络已切换；清除校区、认证入口及重试缓存")
		}
	}
	c.link = next
	return true
}

// monitorLink runs independently of HTTP login requests. Windows change
// notifications are the fast path; a five-second snapshot is the fallback.
func monitorLink(ctx context.Context, logger *log.Logger, notifications <-chan struct{}, changes chan linkState, diagnostic *diagnostics) {
	ticker := time.NewTicker(linkInterval)
	defer ticker.Stop()
	settleTimer := time.NewTimer(time.Hour)
	stopTimer(settleTimer)
	defer settleTimer.Stop()
	previous := linkState{}
	initialized := false
	var pendingUntil time.Time
	check := func() {
		next := readLink(previous)
		if diagnostic != nil {
			diagnostic.log.Printf("monitor raw sample previous=%+v next=%+v pending=%s", previous, next, pendingUntil)
		}
		// Interface enumeration and DHCP can briefly disappear during WLAN
		// roaming. Wait two seconds before reporting a loss or reconnecting.
		if initialized && previous.ready() && !next.ready() {
			if pendingUntil.IsZero() {
				pendingUntil = time.Now().Add(2 * time.Second)
				settleTimer.Reset(2 * time.Second)
				return
			}
			if time.Now().Before(pendingUntil) {
				return
			}
		}
		if !pendingUntil.IsZero() {
			pendingUntil = time.Time{}
			stopTimer(settleTimer)
		}
		if initialized && next == previous {
			return
		}
		if initialized && !next.ready() && !previous.ready() && next.describe() == previous.describe() {
			previous = next
			return
		}
		if !initialized {
			logger.Printf("网络接口初始状态：%s", next.describe())
		} else {
			logger.Printf("网络接口变化：%s → %s", previous.describe(), next.describe())
		}
		previous, initialized = next, true
		select {
		case changes <- next:
		default:
			select {
			case <-changes:
			default:
			}
			select {
			case changes <- next:
			default:
			}
		}
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-notifications:
			check()
		case <-settleTimer.C:
			check()
		case <-ticker.C:
			check()
		}
	}
}

func readLink(previous linkState) linkState {
	interfaces, err := net.Interfaces()
	if err != nil {
		return previous
	}
	var old linkState
	if previous.index != 0 {
		for _, iface := range interfaces {
			if iface.Index == previous.index || iface.Name == previous.name {
				old = snapshotInterface(iface)
				break
			}
		}
	}
	// Always check Ethernet first, even when Wi-Fi was selected previously. This
	// lets a restored cable take priority without restarting the application.
	for _, kind := range []string{"ethernet", "wifi"} {
		for _, iface := range interfaces {
			s := snapshotInterface(iface)
			if s.kind == kind && s.ready() && len(iface.HardwareAddr) > 0 {
				return withSSID(s)
			}
		}
	}
	// A WLAN may have associated but still be waiting for DHCP. Surface that
	// state before falling back to a stale, disconnected Ethernet adapter.
	for _, iface := range interfaces {
		s := snapshotInterface(iface)
		if s.kind == "wifi" && s.up && len(iface.HardwareAddr) > 0 {
			s = withSSID(s)
			if s.ssid != "" {
				return s
			}
		}
	}
	if old.index != 0 {
		return withSSID(old)
	}
	// Keep a physical adapter visible while awaiting a cable, Wi-Fi, or DHCP.
	for _, kind := range []string{"ethernet", "wifi"} {
		for _, iface := range interfaces {
			s := snapshotInterface(iface)
			if s.kind == kind && len(iface.HardwareAddr) > 0 {
				return withSSID(s)
			}
		}
	}
	return linkState{}
}

func withSSID(s linkState) linkState {
	if s.kind == "wifi" && s.up {
		s.ssid = connectedSSID()
	}
	return s
}

func adapterKind(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "ethernet") || strings.Contains(name, "以太网"):
		return "ethernet"
	case strings.Contains(name, "wi-fi") || strings.Contains(name, "wifi") || strings.Contains(name, "wlan") || strings.Contains(name, "无线"):
		return "wifi"
	default:
		return ""
	}
}

func snapshotInterface(iface net.Interface) linkState {
	s := linkState{index: iface.Index, name: iface.Name, kind: adapterKind(iface.Name), up: iface.Flags&net.FlagUp != 0, mac: formatMAC(iface.HardwareAddr)}
	addrs, err := iface.Addrs()
	if err != nil {
		return s
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || ip.To4() == nil || ip.IsLinkLocalUnicast() {
			continue
		}
		s.ipv4 = ip.String()
		if strings.HasPrefix(s.ipv4, "10.") {
			break
		}
	}
	return s
}

func formatMAC(hw net.HardwareAddr) string {
	s := strings.NewReplacer("-", "", ":", "").Replace(hw.String())
	if len(s) == 12 {
		return strings.ToUpper(s)
	}
	return ""
}

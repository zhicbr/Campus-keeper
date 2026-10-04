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
	return s.index == other.index && s.kind == other.kind && s.ssid == other.ssid && s.up == other.up && s.ipv4 == other.ipv4
}

func (c *controller) refreshLink() bool {
	next := readLink(c.link)
	return c.adoptLink(next)
}

func (c *controller) adoptLink(next linkState) bool {
	if next == c.link {
		return false
	}
	if !next.sameNetwork(c.link) {
		c.lastPortal = ""
		c.campus = ""
		c.resetRetry()
		if c.logger != nil && c.link.index != 0 {
			c.logger.Print("网络已切换；清除校区、认证入口及重试缓存")
		}
	}
	c.link = next
	return true
}

// monitorLink runs independently of HTTP login requests. Windows change
// notifications are the fast path; a five-second snapshot is the fallback.
func monitorLink(ctx context.Context, logger *log.Logger, notifications <-chan struct{}, changes chan linkState) {
	ticker := time.NewTicker(linkInterval)
	defer ticker.Stop()
	previous := linkState{}
	initialized := false
	check := func() {
		next := readLink(previous)
		if initialized && next == previous {
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
				if old.ready() && strings.HasPrefix(old.ipv4, "10.") {
					return withSSID(old)
				}
				break
			}
		}
	}
	// Prefer a physical wired campus link, then campus Wi-Fi. Virtual adapters
	// must not be used to decide where credentials should be sent.
	for _, kind := range []string{"ethernet", "wifi"} {
		for _, iface := range interfaces {
			s := snapshotInterface(iface)
			if s.kind == kind && s.up && strings.HasPrefix(s.ipv4, "10.") && len(iface.HardwareAddr) > 0 {
				return withSSID(s)
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
	s := linkState{index: iface.Index, name: iface.Name, kind: adapterKind(iface.Name), up: iface.Flags&net.FlagUp != 0}
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

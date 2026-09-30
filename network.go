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
	up    bool
	ipv4  string
}

func (s linkState) ready() bool { return s.index != 0 && s.up && s.ipv4 != "" }

func (s linkState) describe() string {
	if s.index == 0 {
		return "未找到校园网有线接口"
	}
	if !s.up {
		return fmt.Sprintf("%s 已断开", s.name)
	}
	if s.ipv4 == "" {
		return fmt.Sprintf("%s 已连接，等待 IPv4 地址", s.name)
	}
	return fmt.Sprintf("%s 已连接，IPv4 %s", s.name, s.ipv4)
}

func (c *controller) refreshLink() bool {
	next := readLink(c.link)
	if next == c.link {
		return false
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
			logger.Printf("有线接口初始状态：%s", next.describe())
		} else {
			logger.Printf("有线接口变化：%s → %s", previous.describe(), next.describe())
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
				if old.ready() {
					return old
				}
				break
			}
		}
	}
	// Prefer a campus IPv4 interface. This avoids Tailscale, Wi-Fi Direct,
	// and other virtual adapters while the wired connection has an address.
	for _, iface := range interfaces {
		s := snapshotInterface(iface)
		name := strings.ToLower(iface.Name)
		if s.up && s.ipv4 != "" && strings.HasPrefix(s.ipv4, "10.") && len(iface.HardwareAddr) > 0 && (strings.Contains(name, "ethernet") || strings.Contains(name, "以太网")) {
			return s
		}
	}
	if old.index != 0 {
		return old
	}
	// After startup with the cable already unplugged, retain the physical
	// Ethernet interface so later Windows notifications can identify its change.
	for _, iface := range interfaces {
		name := strings.ToLower(iface.Name)
		if len(iface.HardwareAddr) > 0 && (strings.Contains(name, "ethernet") || strings.Contains(name, "以太网")) {
			return snapshotInterface(iface)
		}
	}
	return linkState{}
}

func snapshotInterface(iface net.Interface) linkState {
	s := linkState{index: iface.Index, name: iface.Name, up: iface.Flags&net.FlagUp != 0}
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

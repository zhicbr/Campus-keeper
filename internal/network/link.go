package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"wenchang-campus-keeper/internal/campus"
	"wenchang-campus-keeper/internal/diagnostics"
)

type LinkState struct {
	Index int
	Name  string
	Kind  string
	SSID  string
	Up    bool
	IPv4  string
	MAC   string
}

func (s LinkState) Ready() bool { return s.Index != 0 && s.Up && s.IPv4 != "" }

func (s LinkState) Describe() string {
	if s.Index == 0 {
		return "未找到校园网物理网卡"
	}
	if !s.Up {
		return fmt.Sprintf("%s 已断开", s.Name)
	}
	if s.IPv4 == "" {
		return fmt.Sprintf("%s 已连接，等待 IPv4 地址", s.Name)
	}
	if s.Kind == "wifi" && s.SSID != "" {
		return fmt.Sprintf("%s 已连接到 %s，IPv4 %s", s.Name, s.SSID, s.IPv4)
	}
	return fmt.Sprintf("%s 已连接，IPv4 %s", s.Name, s.IPv4)
}

func (s LinkState) NetworkSummary(site campus.Campus) string {
	if !s.Ready() {
		return "未连接"
	}
	if IsCampusLink(s) {
		name := "校园网"
		if campus.IsKnownCampus(site) {
			name = campus.CampusName(site)
		}
		if s.Kind == "wifi" {
			return name + " (Wi-Fi)"
		}
		return name + " (有线)"
	}
	if s.Kind == "wifi" {
		if s.SSID != "" {
			return fmt.Sprintf("Wi-Fi (%s)", s.SSID)
		}
		return "Wi-Fi"
	}
	return "有线"
}

func (s LinkState) SameNetwork(other LinkState) bool {
	return s.Index == other.Index && s.Kind == other.Kind && s.SSID == other.SSID && s.Up == other.Up && s.IPv4 == other.IPv4 && s.MAC == other.MAC
}

func (s LinkState) LinkInfo() campus.LinkInfo {
	return campus.LinkInfo{
		Kind: s.Kind,
		SSID: s.SSID,
		IPv4: s.IPv4,
	}
}

func IsCampusLink(link LinkState) bool {
	if link.Kind == "wifi" && link.SSID == StudentWiFiProfile {
		return true
	}
	ip := net.ParseIP(link.IPv4).To4()
	return ip != nil && ip[0] == 10
}

// MonitorLink runs independently of HTTP login requests. Windows change
// notifications are the fast path; snapshot interval is the fallback.
func MonitorLink(ctx context.Context, logger *log.Logger, notifications <-chan struct{}, changes chan LinkState, diagnostic *diagnostics.Diagnostics, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	settleTimer := time.NewTimer(time.Hour)
	stopTimer(settleTimer)
	defer settleTimer.Stop()
	previous := LinkState{}
	initialized := false
	var pendingUntil time.Time
	check := func() {
		next := ReadLink(previous)
		if diagnostic != nil {
			diagnostic.Log.Printf("monitor raw sample previous=%+v next=%+v pending=%s", previous, next, pendingUntil)
		}
		// Interface enumeration and DHCP can briefly disappear during WLAN
		// roaming. Wait two seconds before reporting a loss or reconnecting.
		if initialized && previous.Ready() && !next.Ready() {
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
		if initialized && !next.Ready() && !previous.Ready() && next.Describe() == previous.Describe() {
			previous = next
			return
		}
		if !initialized {
			logger.Printf("网络接口初始状态：%s", next.Describe())
		} else {
			logger.Printf("网络接口变化：%s → %s", previous.Describe(), next.Describe())
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

func ReadLink(previous LinkState) LinkState {
	interfaces, err := net.Interfaces()
	if err != nil {
		return previous
	}
	var old LinkState
	if previous.Index != 0 {
		for _, iface := range interfaces {
			if iface.Index == previous.Index || iface.Name == previous.Name {
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
			if s.Kind == kind && s.Ready() && len(iface.HardwareAddr) > 0 {
				return resolveSSID(s, previous, ConnectedSSID())
			}
		}
	}
	// A WLAN may have associated but still be waiting for DHCP. Surface that
	// state before falling back to a stale, disconnected Ethernet adapter.
	for _, iface := range interfaces {
		s := snapshotInterface(iface)
		if s.Kind == "wifi" && s.Up && len(iface.HardwareAddr) > 0 {
			s = withSSID(s)
			if s.SSID != "" {
				return s
			}
		}
	}
	if old.Index != 0 {
		return withSSID(old)
	}
	// Keep a physical adapter visible while awaiting a cable, Wi-Fi, or DHCP.
	for _, kind := range []string{"ethernet", "wifi"} {
		for _, iface := range interfaces {
			s := snapshotInterface(iface)
			if s.Kind == kind && len(iface.HardwareAddr) > 0 {
				return withSSID(s)
			}
		}
	}
	return LinkState{}
}

func withSSID(s LinkState) LinkState {
	if s.Kind == "wifi" && s.Up {
		s.SSID = ConnectedSSID()
	}
	return s
}

// resolveSSID preserves previous.SSID when Windows WlanQueryInterface momentarily returns
// empty string even though the exact same Wi-Fi interface, Up state, IPv4, and MAC held steady.
func resolveSSID(s, previous LinkState, queriedSSID string) LinkState {
	if s.Kind == "wifi" && s.Up {
		s.SSID = queriedSSID
		if s.SSID == "" && previous.Kind == "wifi" && previous.SSID != "" &&
			s.Index == previous.Index && s.Up == previous.Up && s.IPv4 == previous.IPv4 && s.MAC == previous.MAC {
			s.SSID = previous.SSID
		}
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

func snapshotInterface(iface net.Interface) LinkState {
	s := LinkState{Index: iface.Index, Name: iface.Name, Kind: adapterKind(iface.Name), Up: iface.Flags&net.FlagUp != 0, MAC: formatMAC(iface.HardwareAddr)}
	addrs, err := iface.Addrs()
	if err != nil {
		return s
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil || ip.To4() == nil || ip.IsLinkLocalUnicast() {
			continue
		}
		s.IPv4 = ip.String()
		if strings.HasPrefix(s.IPv4, "10.") {
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

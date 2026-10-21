package campus

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

type Campus string

const (
	Auto     Campus = "auto"
	Wenchang Campus = "wenchang"
	Nanhu    Campus = "nanhu"
)

var wenchangObservedSubnet = &net.IPNet{IP: net.IPv4(10, 52, 128, 0), Mask: net.CIDRMask(18, 32)}
var nanhuObservedSubnet = &net.IPNet{IP: net.IPv4(10, 3, 0, 0), Mask: net.CIDRMask(16, 32)}

type LinkInfo struct {
	Kind string
	SSID string
	IPv4 string
}

// These gateway/subnet combinations are site observations, not global campus
// ranges. A loose IP, front-page URL, or SSID match is insufficient.
func CampusFromFingerprint(ipv4, gateway, gatewayMAC string) Campus {
	ip := net.ParseIP(ipv4).To4()
	mac := strings.ToUpper(strings.ReplaceAll(gatewayMAC, ":", "-"))
	if ip != nil && wenchangObservedSubnet.Contains(ip) && gateway == "10.52.191.254" && mac == "7C-1E-06-8F-98-02" {
		return Wenchang
	}
	if ip != nil && nanhuObservedSubnet.Contains(ip) && gateway == "10.3.255.254" && mac == "80-05-88-46-B4-F9" {
		return Nanhu
	}
	return ""
}

type PortalKind uint8

const (
	PortalUnknown PortalKind = iota
	PortalSharedFrontPage
	PortalSharedEportal
)

func CampusName(site Campus) string {
	switch site {
	case Wenchang:
		return "文昌"
	case Nanhu:
		return "南湖"
	default:
		return "未识别"
	}
}

// Both campuses use the same eportal backend and can show the a79 front page.
// Neither URL identifies the campus; they only establish a trusted endpoint.
func ClassifyPortal(raw string) (PortalKind, error) {
	if raw == "" {
		return PortalUnknown, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "10.2.5.251" || (u.Port() != "" && u.Port() != "80" && u.Port() != "801") {
		return PortalUnknown, fmt.Errorf("认证跳转不在已知校园网服务器：%s", safePortal(raw))
	}
	path := strings.ToLower(u.Path)
	switch {
	case path == "/a79.htm":
		return PortalSharedFrontPage, nil
	case path == "/eportal" || strings.HasPrefix(path, "/eportal/"):
		return PortalSharedEportal, nil
	default:
		return PortalUnknown, fmt.Errorf("未知的认证页：%s", safePortal(raw))
	}
}

func safePortal(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

func ChooseCampus(mode Campus, portal string, link LinkInfo, remembered Campus) (Campus, error) {
	_, err := ClassifyPortal(portal)
	if err != nil {
		if mode == Wenchang || mode == Nanhu {
			return mode, nil // Explicit campus; login still uses the fixed trusted backend.
		}
		if remembered == Wenchang || remembered == Nanhu {
			return remembered, nil // Verified fingerprint; ignore the unknown redirect later.
		}
		return "", err
	}
	if mode != Auto {
		return mode, nil
	}
	if remembered == Wenchang || remembered == Nanhu {
		return remembered, nil
	}
	_ = link // SSID CUMT_Stu exists on both campuses, so it cannot identify Nanhu.
	return "", errors.New("无法识别：等待网关指纹")
}

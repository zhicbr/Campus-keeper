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

type LinkInfo struct {
	Kind string
	SSID string
	IPv4 string
}

// CampusFromFingerprint matches against the central KnownFingerprints table.
// A loose IP, front-page URL, or SSID match is insufficient.
func CampusFromFingerprint(ipv4, gateway, gatewayMAC string) Campus {
	ip := net.ParseIP(ipv4).To4()
	if ip == nil {
		return ""
	}
	mac := strings.ToUpper(strings.ReplaceAll(gatewayMAC, ":", "-"))
	for _, fp := range compiledTable {
		if fp.subnet.Contains(ip) && gateway == fp.gatewayIP && mac == fp.gatewayMAC {
			return fp.campus
		}
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
	if err != nil || u.Scheme != "http" || u.Hostname() != AuthHost || (u.Port() != "" && u.Port() != AuthPortDefault && u.Port() != AuthPortPortal) {
		return PortalUnknown, fmt.Errorf("认证跳转不在已知校园网服务器：%s", safePortal(raw))
	}
	path := strings.ToLower(u.Path)
	switch {
	case path == PathFrontPage:
		return PortalSharedFrontPage, nil
	case path == PathEportalPrefix || strings.HasPrefix(path, PathEportalPrefix+"/"):
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

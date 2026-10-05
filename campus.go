package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

type campus string

const (
	campusAuto     campus = "auto"
	campusWenchang campus = "wenchang"
	campusNanhu    campus = "nanhu"
)

var wenchangObservedSubnet = &net.IPNet{IP: net.IPv4(10, 52, 128, 0), Mask: net.CIDRMask(18, 32)}

// This exact gateway and subnet have been observed on both Wenchang Ethernet
// and CUMT_Stu Wi-Fi. A loose IP or SSID match is deliberately insufficient.
func campusFromFingerprint(link linkState, gateway, gatewayMAC string) campus {
	ip := net.ParseIP(link.ipv4).To4()
	mac := strings.ToUpper(strings.ReplaceAll(gatewayMAC, ":", "-"))
	if ip != nil && wenchangObservedSubnet.Contains(ip) && gateway == "10.52.191.254" && mac == "7C-1E-06-8F-98-02" {
		return campusWenchang
	}
	return ""
}

type portalKind uint8

const (
	portalUnknown portalKind = iota
	portalWenchang
	portalSharedEportal
)

func campusName(site campus) string {
	switch site {
	case campusWenchang:
		return "文昌"
	case campusNanhu:
		return "南湖"
	default:
		return "校区未识别"
	}
}

// Both campus flows use the same eportal backend. Its host alone cannot
// identify the campus; the a79 front page is the Wenchang-specific evidence.
func classifyPortal(raw string) (portalKind, error) {
	if raw == "" {
		return portalUnknown, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "10.2.5.251" || (u.Port() != "" && u.Port() != "80" && u.Port() != "801") {
		return portalUnknown, fmt.Errorf("认证跳转不在已知校园网服务器：%s", safePortal(raw))
	}
	path := strings.ToLower(u.Path)
	switch {
	case path == "/a79.htm":
		return portalWenchang, nil
	case path == "/eportal" || strings.HasPrefix(path, "/eportal/"):
		return portalSharedEportal, nil
	default:
		return portalUnknown, fmt.Errorf("未知的认证页：%s", safePortal(raw))
	}
}

func chooseCampus(mode campus, portal string, link linkState, remembered campus) (campus, error) {
	kind, err := classifyPortal(portal)
	if err != nil {
		if mode == campusWenchang || mode == campusNanhu {
			return mode, nil // Explicit campus; login still uses the fixed trusted backend.
		}
		return "", err
	}
	if mode != campusAuto {
		return mode, nil
	}
	if kind == portalWenchang {
		return campusWenchang, nil
	}
	if remembered == campusWenchang || remembered == campusNanhu {
		return remembered, nil
	}
	_ = link // SSID CUMT_Stu exists on both campuses, so it cannot identify Nanhu.
	return "", errors.New("无法可靠识别校区；可在 .env 设置 CAMPUS=nanhu 或 CAMPUS=wenchang")
}

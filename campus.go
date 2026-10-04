package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type campus string

const (
	campusAuto     campus = "auto"
	campusWenchang campus = "wenchang"
	campusNanhu    campus = "nanhu"
)

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
	if kind == portalSharedEportal && link.kind == "wifi" && strings.EqualFold(link.ssid, "CUMT_Stu") {
		return campusNanhu, nil
	}
	return "", errors.New("无法可靠识别校区；可在 .env 设置 CAMPUS=nanhu 或 CAMPUS=wenchang")
}

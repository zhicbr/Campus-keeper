package network

import (
	"testing"

	"wenchang-campus-keeper/internal/campus"
)

func TestNetworkSummary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		link     LinkState
		site     campus.Campus
		expected string
	}{
		{
			name:     "unready link",
			link:     LinkState{Index: 0},
			site:     campus.Auto,
			expected: "未连接",
		},
		{
			name:     "campus ethernet wenchang",
			link:     LinkState{Index: 1, Up: true, IPv4: "10.52.1.2", Kind: "ethernet"},
			site:     campus.Wenchang,
			expected: "文昌 (有线)",
		},
		{
			name:     "campus wifi nanhu",
			link:     LinkState{Index: 2, Up: true, IPv4: "10.3.1.2", Kind: "wifi", SSID: StudentWiFiProfile},
			site:     campus.Nanhu,
			expected: "南湖 (Wi-Fi)",
		},
		{
			name:     "campus wifi continuing edu",
			link:     LinkState{Index: 5, Up: true, IPv4: "10.27.176.53", Kind: "wifi", SSID: StudentWiFiProfile},
			site:     campus.ContinuingEdu,
			expected: "继续教育学院 (Wi-Fi)",
		},
		{
			name:     "external wifi",
			link:     LinkState{Index: 3, Up: true, IPv4: "192.168.1.50", Kind: "wifi", SSID: "MyPhoneHotspot"},
			site:     campus.Auto,
			expected: "Wi-Fi (MyPhoneHotspot)",
		},
		{
			name:     "external ethernet",
			link:     LinkState{Index: 4, Up: true, IPv4: "192.168.2.100", Kind: "ethernet"},
			site:     campus.Auto,
			expected: "有线",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.link.NetworkSummary(tc.site)
			if got != tc.expected {
				t.Fatalf("NetworkSummary() = %q, want %q", got, tc.expected)
			}
		})
	}
}


func TestResolveSSIDPreservesTransientEmptyRead(t *testing.T) {
	prev := LinkState{Index: 2, Name: "WLAN", Kind: "wifi", SSID: StudentWiFiProfile, Up: true, IPv4: "10.3.249.106", MAC: "aabbccddeeff"}
	curSame := LinkState{Index: 2, Name: "WLAN", Kind: "wifi", Up: true, IPv4: "10.3.249.106", MAC: "aabbccddeeff"}
	got := resolveSSID(curSame, prev, "")
	if got.SSID != StudentWiFiProfile {
		t.Fatalf("expected transient empty SSID to keep %q, got %q", StudentWiFiProfile, got.SSID)
	}

	curNewIP := LinkState{Index: 2, Name: "WLAN", Kind: "wifi", Up: true, IPv4: "192.168.1.10", MAC: "aabbccddeeff"}
	gotDiff := resolveSSID(curNewIP, prev, "")
	if gotDiff.SSID != "" {
		t.Fatalf("expected different IPv4 not to inherit old SSID, got %q", gotDiff.SSID)
	}

	gotSwitched := resolveSSID(curSame, prev, "OtherWiFi")
	if gotSwitched.SSID != "OtherWiFi" {
		t.Fatalf("expected explicit new SSID, got %q", gotSwitched.SSID)
	}
}

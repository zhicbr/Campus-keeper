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

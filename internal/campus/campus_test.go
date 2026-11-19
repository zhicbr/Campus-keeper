package campus

import (
	"testing"
)

func TestCampusSelectionDoesNotGuessFromSharedHost(t *testing.T) {
	wifi := LinkInfo{Kind: "wifi", SSID: "CUMT_Stu"}
	wired := LinkInfo{Kind: "ethernet"}
	for _, tc := range []struct {
		name   string
		mode   Campus
		portal string
		link   LinkInfo
		want   Campus
		fail   bool
	}{
		{"shared front page does not identify campus", Auto, "http://10.2.5.251/a79.htm", wired, "", true},
		{"shared backend plus student wifi is still ambiguous", Auto, "http://10.2.5.251:801/eportal/", wifi, "", true},
		{"shared backend on ethernet is ambiguous", Auto, "http://10.2.5.251:801/eportal/", wired, "", true},
		{"ssid alone is insufficient", Auto, "", wifi, "", true},
		{"manual nanhu works without redirect", Nanhu, "", wired, Nanhu, false},
		{"manual nanhu overrides page inference", Nanhu, "http://10.2.5.251/a79.htm", wifi, Nanhu, false},
		{"untrusted host is rejected in auto mode", Auto, "http://example.com/eportal/", wifi, "", true},
		{"manual campus ignores unknown redirect", Nanhu, "http://example.com/eportal/", wifi, Nanhu, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChooseCampus(tc.mode, tc.portal, tc.link, "")
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("chooseCampus() = %q, %v; want %q, fail=%v", got, err, tc.want, tc.fail)
			}
		})
	}
	if site, err := ChooseCampus(Auto, "http://10.2.5.251/a79.htm", wifi, Nanhu); err != nil || site != Nanhu {
		t.Fatalf("shared front page overrode Nanhu fingerprint: %q, %v", site, err)
	}
	if site, err := ChooseCampus(Auto, "http://example.com/redirect", wifi, Nanhu); err != nil || site != Nanhu {
		t.Fatalf("unknown redirect overrode verified Nanhu fingerprint: %q, %v", site, err)
	}
}

func TestCampusFingerprintRequiresGatewayMACAndSubnet(t *testing.T) {
	wired := LinkInfo{Kind: "ethernet", IPv4: "10.52.157.34"}
	wifi := LinkInfo{Kind: "wifi", SSID: "CUMT_Stu", IPv4: "10.52.136.46"}
	for _, link := range []LinkInfo{wired, wifi} {
		if got := CampusFromFingerprint(link.IPv4, "10.52.191.254", "7C-1E-06-8F-98-02"); got != Wenchang {
			t.Fatalf("known Wenchang link %s was not matched", link.IPv4)
		}
	}
	if got := CampusFromFingerprint("10.52.136.46", "10.52.191.254", "00-11-22-33-44-55"); got != "" {
		t.Fatal("student SSID and IP incorrectly overrode a different gateway MAC")
	}
	if got := CampusFromFingerprint("10.53.136.46", "10.52.191.254", "7C-1E-06-8F-98-02"); got != "" {
		t.Fatal("nonmatching subnet was classified as Wenchang")
	}
	if got := CampusFromFingerprint("10.3.233.69", "10.3.255.254", "80-05-88-46-B4-F9"); got != Nanhu {
		t.Fatal("known Nanhu link was not matched")
	}
	if got := CampusFromFingerprint("10.3.233.69", "10.3.255.254", "7C-1E-06-8F-98-02"); got != "" {
		t.Fatal("Nanhu IP without its gateway MAC was incorrectly classified")
	}
	if got := CampusFromFingerprint("10.27.176.53", "10.27.255.254", "00-74-9C-B4-F0-5C"); got != ContinuingEdu {
		t.Fatal("known ContinuingEdu link was not matched")
	}
	if got := CampusFromFingerprint("10.27.176.53", "10.27.255.254", "7C-1E-06-8F-98-02"); got != "" {
		t.Fatal("ContinuingEdu IP without its gateway MAC was incorrectly classified")
	}
	if got := CampusName(ContinuingEdu); got != "继续教育学院" {
		t.Fatalf("unexpected ContinuingEdu name: %q", got)
	}
}

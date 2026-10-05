package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCampusSelectionDoesNotGuessFromSharedHost(t *testing.T) {
	wifi := linkState{kind: "wifi", ssid: "CUMT_Stu"}
	wired := linkState{kind: "ethernet"}
	for _, tc := range []struct {
		name   string
		mode   campus
		portal string
		link   linkState
		want   campus
		fail   bool
	}{
		{"wenchang front page", campusAuto, "http://10.2.5.251/a79.htm", wired, campusWenchang, false},
		{"shared backend plus student wifi is still ambiguous", campusAuto, "http://10.2.5.251:801/eportal/", wifi, "", true},
		{"shared backend on ethernet is ambiguous", campusAuto, "http://10.2.5.251:801/eportal/", wired, "", true},
		{"ssid alone is insufficient", campusAuto, "", wifi, "", true},
		{"manual nanhu works without redirect", campusNanhu, "", wired, campusNanhu, false},
		{"manual nanhu overrides page inference", campusNanhu, "http://10.2.5.251/a79.htm", wifi, campusNanhu, false},
		{"untrusted host is rejected in auto mode", campusAuto, "http://example.com/eportal/", wifi, "", true},
		{"manual campus ignores unknown redirect", campusNanhu, "http://example.com/eportal/", wifi, campusNanhu, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chooseCampus(tc.mode, tc.portal, tc.link, "")
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("chooseCampus() = %q, %v; want %q, fail=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}

func TestCampusAccountDefaultsAndOverride(t *testing.T) {
	cfg := config{account: "student"}
	if cfg.accountFor(campusWenchang) != "student@xyw" || cfg.accountFor(campusNanhu) != "student@cmcc" {
		t.Fatal("campus account defaults changed")
	}
	cfg.accountSuffix, cfg.suffixExplicit = "@dx", true
	if cfg.accountFor(campusNanhu) != "student@dx" {
		t.Fatal("explicit operator suffix was ignored")
	}
}

func TestNanhuLoginMatchesOlderScript(t *testing.T) {
	var got *url.URL
	p := &portalClient{cfg: config{account: "student", password: "secret"}}
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`dr123({"result":"1"})`)), Header: make(http.Header)}, nil
	})}
	if err := p.login(context.Background(), campusNanhu, "http://10.2.5.251:801/eportal/", "10.1.2.3"); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "10.2.5.251:801" || got.Path != "/eportal/" {
		t.Fatalf("unexpected login endpoint: %v", got)
	}
	q := got.Query()
	if q.Get("user_account") != "student@cmcc" || q.Get("wlan_user_ip") != "10.1.2.3" || q.Get("wlan_user_mac") != "000000000000" {
		t.Fatalf("Nanhu request parameters differ from older script: account=%q ip=%q mac=%q", q.Get("user_account"), q.Get("wlan_user_ip"), q.Get("wlan_user_mac"))
	}
	if q.Has("wlan_ac_name") || q.Has("wlan_ac_ip") {
		t.Fatal("Nanhu request unexpectedly contains Wenchang access-controller fields")
	}
}

func TestNetworkSwitchDropsCampusCredentialsContext(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	c := &controller{
		logger: log.New(io.Discard, "", 0), retryTimer: timer,
		link:   linkState{index: 3, kind: "wifi", ssid: "CUMT_Stu", up: true, ipv4: "10.1.2.3"},
		campus: campusNanhu, lastPortal: "http://10.2.5.251:801/eportal/",
		backoff: retryMax, nextRetry: time.Now().Add(time.Minute),
	}
	if !c.adoptLink(linkState{index: 3, kind: "wifi", ssid: "Other", up: true, ipv4: "10.1.2.3"}) {
		t.Fatal("network change was missed")
	}
	if c.campus != "" || c.lastPortal != "" || c.backoff != retryBase || !c.nextRetry.IsZero() {
		t.Fatal("campus-specific state survived a network change")
	}
}

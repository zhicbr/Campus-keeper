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

func TestSixAMResetBoundary(t *testing.T) {
	china := time.FixedZone("Asia/Shanghai", 8*3600)
	for _, tc := range []struct {
		at, want string
	}{
		{"2026-09-30 05:59:59", "2026-09-30 06:00:00"},
		{"2026-09-30 06:00:00", "2026-10-01 06:00:00"},
		{"2026-09-30 23:59:59", "2026-10-01 06:00:00"},
	} {
		at, _ := time.ParseInLocation("2006-01-02 15:04:05", tc.at, china)
		got := nextSix(at).In(china).Format("2006-01-02 15:04:05")
		if got != tc.want {
			t.Fatalf("nextSix(%s) = %s, want %s", tc.at, got, tc.want)
		}
	}
	c := &controller{backoff: retryMax, nextReset: time.Now().Add(-time.Second), logger: log.New(io.Discard, "", 0)}
	c.resetAtSixIfDue()
	if c.backoff != retryBase {
		t.Fatalf("06:00 reset left backoff at %s", c.backoff)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWenchangLoginRequestMatchesPortal(t *testing.T) {
	var got *url.URL
	p := &portalClient{cfg: config{account: "student", password: "secret"}}
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`dr123({"result":"1"})`)), Header: make(http.Header)}, nil
	})}
	err := p.login(context.Background(), "http://10.2.5.251/a79.htm?wlanuserip=10.52.157.34&wlanacname=BRAS&mac=08-26-AE-37-F2-E2", "10.52.157.34")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "10.2.5.251:801" || got.Path != "/eportal/" {
		t.Fatalf("wrong endpoint: %v", got)
	}
	q := got.Query()
	for key, want := range map[string]string{
		"c": "Portal", "a": "login", "login_method": "1",
		"user_account": "student@xyw", "wlan_user_ip": "10.52.157.34",
		"wlan_user_mac": "0826AE37F2E2", "wlan_ac_name": "BRAS",
	} {
		if q.Get(key) != want {
			t.Fatalf("%s = %q, want %q", key, q.Get(key), want)
		}
	}
}

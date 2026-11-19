package portal

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wenchang-campus-keeper/internal/campus"
	"wenchang-campus-keeper/internal/config"
)

func TestProbeDoesNotWaitForSlowPrimaryWhenHTTPSWorks(t *testing.T) {
	p := NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "www.msftconnecttest.com" {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if result := p.Probe(ctx); !result.Online {
		t.Fatalf("working HTTPS should win without waiting for the slow probe: %+v", result)
	}
}

func TestWenchangLoginRequestMatchesPortal(t *testing.T) {
	var got *url.URL
	p := NewClient(config.Config{Account: "student", Password: "secret"})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`dr123({"result":"1"})`)), Header: make(http.Header)}, nil
	})})
	err := p.Login(context.Background(), campus.Wenchang, "http://10.2.5.251/a79.htm?wlanuserip=10.52.157.34&wlanacname=BRAS&mac=08-26-AE-37-F2-E2", "10.52.157.34", "")
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

func TestLoginUsesInterfaceMACWhenPortalOmitsMAC(t *testing.T) {
	var got *url.URL
	p := NewClient(config.Config{Account: "student", Password: "secret"})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`dr123({"result":"1"})`)), Header: make(http.Header)}, nil
	})})
	err := p.Login(context.Background(), campus.Wenchang, "http://10.2.5.251:801/eportal/?wlanuserip=10.52.157.34", "10.52.157.34", "A4-B1-C2-D3-E4-F5")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no request sent")
	}
	if q := got.Query(); q.Get("wlan_user_mac") != "A4B1C2D3E4F5" {
		t.Fatalf("expected physical MAC to be used when portal query omits it: got %q", q.Get("wlan_user_mac"))
	}
}

func TestNanhuLoginMatchesOlderScript(t *testing.T) {
	var got *url.URL
	p := NewClient(config.Config{Account: "student", Password: "secret", AccountSuffix: "@cmcc", SuffixExplicit: true})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`dr123({"result":"1"})`)), Header: make(http.Header)}, nil
	})})
	if err := p.Login(context.Background(), campus.Nanhu, "http://10.2.5.251:801/eportal/", "10.1.2.3", ""); err != nil {
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
		t.Fatal("request without portal metadata unexpectedly contains access-controller fields")
	}
}


func TestDecodePortalMessageErrCode16(t *testing.T) {
	// Base64 encoding of "Authentication Fail ErrCode=16"
	raw := "QXV0aGVudGljYXRpb24gRmFpbCBFcnJDb2RlPTE2"
	decoded := decodePortalMessage(raw)
	if !strings.Contains(decoded, "ErrCode=16") {
		t.Fatalf("expected ErrCode=16 in decoded message, got %s", decoded)
	}
	if !strings.Contains(decoded, "夜间宿舍定时断网") {
		t.Fatalf("expected explanation about night curfew/dorm shutdown, got %s", decoded)
	}
}

func TestContinuingEduLoginUsesInterfaceMACAndACParams(t *testing.T) {
	var got *url.URL
	p := NewClient(config.Config{Account: "student", Password: "secret", Operator: config.OperatorTelecom})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`dr123({"result":"1"})`)), Header: make(http.Header)}, nil
	})})
	err := p.Login(context.Background(), campus.ContinuingEdu, "http://10.2.5.251/a79.htm?wlanuserip=10.27.176.53&wlanacname=AC_PARK&wlanacip=10.27.0.1", "10.27.176.53", "08-8E-90-19-58-B6")
	if err != nil {
		t.Fatal(err)
	}
	q := got.Query()
	for k, want := range map[string]string{
		"user_account":  "student@dx",
		"wlan_user_ip":  "10.27.176.53",
		"wlan_user_mac": "088E901958B6",
		"wlan_ac_name":  "AC_PARK",
		"wlan_ac_ip":    "10.27.0.1",
	} {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestLogoutChecksJSONPAndFallsBackToDrcom(t *testing.T) {
	p := NewClient(config.Config{Account: "student", Password: "secret"})
	// 1. Both :801 eportal logout and :80 /drcom/logout return result=0 -> must fail instead of falsely succeeding on HTTP 200.
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`dr123({"result":"0","msg":"logout rejected"})`)),
			Header:     make(http.Header),
		}, nil
	})})
	err := p.Logout(context.Background(), campus.ContinuingEdu, "", "10.27.176.53", "08-8E-90-19-58-B6")
	if err == nil || !strings.Contains(err.Error(), "logout rejected") {
		t.Fatalf("expected logout failure containing server message, got %v", err)
	}

	// 2. :801 eportal logout returns result=0, fallback :80 /drcom/logout returns result=1 -> must succeed.
	var hitDrcom bool
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/drcom/logout" {
			hitDrcom = true
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`dr456({"result":1})`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`dr123({"result":"0","msg":"not online on 801"})`)),
			Header:     make(http.Header),
		}, nil
	})})
	if err := p.Logout(context.Background(), campus.ContinuingEdu, "", "10.27.176.53", "08-8E-90-19-58-B6"); err != nil {
		t.Fatalf("expected /drcom/logout fallback to succeed, got %v", err)
	}
	if !hitDrcom {
		t.Fatal("expected fallback call to /drcom/logout")
	}
}

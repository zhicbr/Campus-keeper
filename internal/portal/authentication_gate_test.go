package portal

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"wenchang-campus-keeper/internal/campus"
	"wenchang-campus-keeper/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func gateResponse(status int, body, redirect string) *http.Response {
	h := make(http.Header)
	if redirect != "" {
		h.Set("Location", redirect)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func TestAuthenticationGateRecoversDuringDiscovery(t *testing.T) {
	var recovered atomic.Bool
	var loginCalls atomic.Int32
	p := NewClient(config.Config{CampusMode: campus.Nanhu, PortalURL: "http://10.2.5.251/a79.htm"})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("a") == "login" {
			loginCalls.Add(1)
			return nil, errors.New("unexpected credentials")
		}
		if r.URL.Host == "9.9.9.9" {
			return nil, context.DeadlineExceeded
		}
		if r.URL.Host == "neverssl.com" {
			// Reproduce 16:05:21: connectivity returns while discovery finishes.
			recovered.Store(true)
			return gateResponse(200, "ordinary public page", ""), nil
		}
		if !recovered.Load() {
			return nil, errors.New("still offline")
		}
		return gateResponse(200, "Microsoft Connect Test", ""), nil
	})})
	decision, err := p.PrepareAuthentication(context.Background(), ProbeResult{Reason: "earlier timeout"})
	if err != nil || !decision.Online || decision.Redirect != "" || loginCalls.Load() != 0 {
		t.Fatalf("recovery must suppress login: %+v %v", decision, err)
	}
}

func TestAuthenticationGateRequiresCurrentTrustedEvidence(t *testing.T) {
	const known = "http://10.2.5.251/a79.htm?wlanuserip=10.3.138.215"
	for _, tc := range []struct {
		name, initial, discovered, fresh string
		want                             string
		blocked                          bool
	}{
		{"timeouts with configured default", "", "", "", "", true},
		{"foreign redirects", "http://example.com/a79.htm", "http://example.com/eportal/", "http://example.com/a79.htm", "", true},
		{"current initial portal", known, "", "", known, false},
		{"current discovered portal", "", known, "", known, false},
		{"fresh probe portal", "", "", known, known, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewClient(config.Config{CampusMode: campus.Nanhu, PortalURL: known})
			p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				redirect := tc.fresh
				if r.URL.Host == "9.9.9.9" || r.URL.Host == "neverssl.com" {
					redirect = tc.discovered
				}
				if redirect != "" {
					return gateResponse(302, "", redirect), nil
				}
				return nil, context.DeadlineExceeded
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
			decision, err := p.PrepareAuthentication(context.Background(), ProbeResult{Redirect: tc.initial})
			if tc.blocked {
				if !errors.Is(err, ErrNoAuthenticationEvidence) || decision.Redirect != "" {
					t.Fatalf("missing evidence should block: %+v %v", decision, err)
				}
			} else if err != nil || decision.Redirect != tc.want {
				t.Fatalf("valid current evidence lost: %+v %v", decision, err)
			}
		})
	}
}

func TestAuthenticationGateOnlineOverridesTrustedRedirect(t *testing.T) {
	p := NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return gateResponse(200, "Microsoft Connect Test", ""), nil
	})})
	decision, err := p.PrepareAuthentication(context.Background(), ProbeResult{Redirect: "http://10.2.5.251/a79.htm"})
	if err != nil || !decision.Online || decision.Redirect != "" {
		t.Fatalf("healthy network must win: %+v %v", decision, err)
	}
}

func TestAuthenticationGateCancellationDuringDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	p := NewClient(config.Config{})
	p.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, context.Canceled
	})})
	_, err := p.PrepareAuthentication(ctx, ProbeResult{Reason: "probe timeout"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestProbeDirectPortal(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		location   string
		body       string
		wantURL    string
	}{
		{
			name:       "302 redirect with Location parameter",
			statusCode: 302,
			location:   "http://10.2.5.251/a79.htm?wlanuserip=10.3.97.58",
			wantURL:    "http://10.2.5.251/a79.htm?wlanuserip=10.3.97.58",
		},
		{
			name:       "200 OK with Dr.COM body",
			statusCode: 200,
			body:       "<html><head><title>Dr.COM</title></head><body>login</body></html>",
			wantURL:    "http://10.2.5.251/a79.htm",
		},
		{
			name:       "200 OK unrelated page",
			statusCode: 200,
			body:       "<html><body>hello world</body></html>",
			wantURL:    "",
		},
		{
			name:       "500 Internal Server Error",
			statusCode: 500,
			wantURL:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewClient(config.Config{})
			p.SetHTTPClient(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return gateResponse(tt.statusCode, tt.body, tt.location), nil
				}),
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			})
			got := p.probeDirectPortal(context.Background())
			if got != tt.wantURL {
				t.Errorf("probeDirectPortal() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

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
	p := &portalClient{cfg: config{campusMode: campusNanhu, portalURL: "http://10.2.5.251/a79.htm"}}
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
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
	})}
	decision, err := p.prepareAuthentication(context.Background(), probeResult{reason: "earlier timeout"})
	if err != nil || !decision.online || decision.redirect != "" || loginCalls.Load() != 0 {
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
			p := &portalClient{cfg: config{campusMode: campusNanhu, portalURL: known}}
			p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				redirect := tc.fresh
				if r.URL.Host == "9.9.9.9" || r.URL.Host == "neverssl.com" {
					redirect = tc.discovered
				}
				if redirect != "" {
					return gateResponse(302, "", redirect), nil
				}
				return nil, context.DeadlineExceeded
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			decision, err := p.prepareAuthentication(context.Background(), probeResult{redirect: tc.initial})
			if tc.blocked {
				if !errors.Is(err, errNoAuthenticationEvidence) || decision.redirect != "" {
					t.Fatalf("missing evidence should block: %+v %v", decision, err)
				}
			} else if err != nil || decision.redirect != tc.want {
				t.Fatalf("valid current evidence lost: %+v %v", decision, err)
			}
		})
	}
}

func TestAuthenticationGateOnlineOverridesTrustedRedirect(t *testing.T) {
	p := &portalClient{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return gateResponse(200, "Microsoft Connect Test", ""), nil
	})}}
	decision, err := p.prepareAuthentication(context.Background(), probeResult{redirect: "http://10.2.5.251/a79.htm"})
	if err != nil || !decision.online || decision.redirect != "" {
		t.Fatalf("healthy network must win: %+v %v", decision, err)
	}
}

func TestAuthenticationGateCancellationDuringDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	p := &portalClient{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, context.Canceled
	})}}
	_, err := p.prepareAuthentication(ctx, probeResult{})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled discovery must stop: calls=%d err=%v", calls, err)
	}
}

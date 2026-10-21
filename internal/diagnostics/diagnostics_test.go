//go:build windows

package diagnostics

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiagnosticRequestRedactsCredentialsAndRedirectQueries(t *testing.T) {
	var output bytes.Buffer
	d := &Diagnostics{Log: log.New(&output, "", 0)}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("user_password") != "sensitive-password" {
			t.Fatal("diagnostics altered request")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"http://10.2.5.251/a79.htm?token=sensitive-token"}}, Body: io.NopCloser(strings.NewReader("sensitive-body"))}, nil
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, _ := http.NewRequestWithContext(context.Background(), "GET", "http://10.2.5.251/eportal/?user_password=sensitive-password", nil)
	resp, err := d.Request(client, r, "login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, secret := range []string{"sensitive-password", "sensitive-token", "sensitive-body", "?"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("secret in log: %q", secret)
		}
	}
	if !strings.Contains(output.String(), "status=302") {
		t.Fatal("HTTP status missing")
	}
}

func TestDiagnosticErrorUnwrapsCredentialURL(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "http://10.2.5.251/?user_password=secret", Err: context.DeadlineExceeded}
	got := DiagnosticError(err)
	if strings.Contains(got, "secret") || !strings.Contains(got, "deadline exceeded") {
		t.Fatalf("unsafe or unhelpful error: %s", got)
	}
}

// Explicit opt-in smoke test: read-only OS collection and public probes only,
// without running the controller or touching authentication/WLAN recovery.
func TestDiagnosticCollectorsSmoke(t *testing.T) {
	dir := os.Getenv("KEEPER_DIAGNOSTIC_SMOKE_DIR")
	if dir == "" {
		t.Skip("live collectors require explicit opt-in")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	d, err := StartDiagnostics(ctx, dir, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	d.Close()
	data, err := os.ReadFile(filepath.Join(dir, "network-diagnostic.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"SESSION START", "system-route-https", "windows-default-proxy-https", "SECTION adapters", "SECTION routes", "windows-events", "EVENT CHANNEL", "SESSION END"} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("missing collector marker: %s", marker)
		}
	}
	t.Logf("live diagnostic collectors completed; preserved %d log bytes", len(data))
}

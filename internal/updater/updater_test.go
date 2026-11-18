package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v1.0.3", "1.0.2", true},
		{"v1.1.0", "v1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"v1.0.2", "1.0.2", false},
		{"v1.0.1", "1.0.2", false},
		{"1.0.2-beta", "1.0.2", false},
		{"v1.0.3-rc1", "1.0.2", true},
		{"invalid", "1.0.2", false},
	}
	for _, tc := range cases {
		if got := IsNewerVersion(tc.candidate, tc.current); got != tc.want {
			t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestCheckLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.0.3",
			"html_url": "https://github.com/zhicbr/net-keeper/releases/tag/v1.0.3",
			"body": "Bug fixes",
			"assets": [
				{"name": "wenchang-campus-keeper.exe", "browser_download_url": "https://github.com/zhicbr/net-keeper/releases/download/v1.0.3/wenchang-campus-keeper.exe"}
			]
		}`))
	}))
	defer srv.Close()

	client := NewClient("1.0.2")
	client.SetAPIURL(srv.URL)

	info, hasUpdate, err := client.CheckLatest(context.Background(), "1.0.2")
	if err != nil {
		t.Fatalf("CheckLatest failed: %v", err)
	}
	if !hasUpdate {
		t.Fatal("expected hasUpdate=true for v1.0.3 > 1.0.2")
	}
	if info.Version != "v1.0.3" {
		t.Fatalf("unexpected version: %q", info.Version)
	}
	if info.DownloadURL != "https://github.com/zhicbr/net-keeper/releases/download/v1.0.3/wenchang-campus-keeper.exe" {
		t.Fatalf("unexpected downloadURL: %q", info.DownloadURL)
	}

	_, hasUpdate, err = client.CheckLatest(context.Background(), "1.0.3")
	if err != nil {
		t.Fatalf("CheckLatest failed: %v", err)
	}
	if hasUpdate {
		t.Fatal("expected hasUpdate=false when already on v1.0.3")
	}
}

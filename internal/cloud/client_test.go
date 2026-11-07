package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudClientShareLifecycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/share/sh_123":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true,"shareId":"sh_123","encryptedPayload":"enc_abc","operator":"cmcc","memo":"test"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/share/sh_deleted":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"share_not_found"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/share/publish":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true,"shareId":"sh_new","expiresAt":1800000}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/share/revoke":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/share/my":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"success":true,"shares":[{"shareId":"sh_1","memo":"roommate","operator":"cmcc"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL)

	// 1. Fetch valid
	share, err := client.FetchShare(context.Background(), "sh_123")
	if err != nil {
		t.Fatalf("FetchShare failed: %v", err)
	}
	if share.ShareID != "sh_123" || share.Operator != "cmcc" {
		t.Errorf("Unexpected share data: %+v", share)
	}

	// 2. Fetch deleted
	_, err = client.FetchShare(context.Background(), "sh_deleted")
	if err == nil {
		t.Errorf("Expected error for deleted share, got nil")
	}

	// 3. Publish
	pub, err := client.PublishShare(context.Background(), "mock_token", PublishRequest{
		ShareID:          "sh_new",
		EncryptedPayload: "test",
		Operator:         "cmcc",
		DurationHours:    24,
	})
	if err != nil {
		t.Fatalf("PublishShare failed: %v", err)
	}
	if pub.ShareID != "sh_new" {
		t.Errorf("Unexpected pub data: %+v", pub)
	}

	// 4. Revoke
	err = client.RevokeShare(context.Background(), "mock_token", "sh_new")
	if err != nil {
		t.Fatalf("RevokeShare failed: %v", err)
	}

	// 5. List
	list, err := client.ListMyShares(context.Background(), "mock_token")
	if err != nil {
		t.Fatalf("ListMyShares failed: %v", err)
	}
	if len(list) != 1 || list[0].ShareID != "sh_1" {
		t.Errorf("Unexpected list data: %+v", list)
	}
}

//go:build windows

package main

import (
	"bytes"
	"os"
	"testing"
)

func TestDPAPIEncryptDecrypt(t *testing.T) {
	orig := []byte("student123:my_secret_pass_888:cmcc")
	enc, err := encryptData(orig)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if bytes.Equal(enc, orig) {
		t.Fatal("encrypted data matches plaintext!")
	}
	dec, err := decryptData(enc)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("got %q, want %q", dec, orig)
	}
}

func TestConfigEncryptedRoundTrip(t *testing.T) {
	cfg := config{
		account:  "TS2024001",
		password: "Password123!",
		operator: operatorCMCC,
	}
	tmp := t.TempDir()
	origAppDir := appDataDir()
	_ = origAppDir

	p := tmp + "\\config.dat"
	raw, err := encryptData([]byte(`{"account":"TS2024001","password":"Password123!","operator":"cmcc"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	decRaw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := decryptData(decRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dec, []byte("TS2024001")) {
		t.Fatalf("unexpected decrypted content: %s", string(dec))
	}
	if cfg.accountFor(campusNanhu) != "TS2024001@cmcc" {
		t.Fatalf("expected TS2024001@cmcc, got %s", cfg.accountFor(campusNanhu))
	}
	if cfg.operatorName() != "移动" {
		t.Fatalf("expected 移动, got %s", cfg.operatorName())
	}
}

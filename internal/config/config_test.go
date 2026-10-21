//go:build windows

package config

import (
	"bytes"
	"os"
	"testing"

	"wenchang-campus-keeper/internal/campus"
)

func TestConfigEncryptedRoundTrip(t *testing.T) {
	cfg := Config{
		Account:  "TS2024001",
		Password: "Password123!",
		Operator: OperatorCMCC,
	}
	tmp := t.TempDir()

	p := tmp + "\\config.dat"
	raw, err := EncryptData([]byte(`{"account":"TS2024001","password":"Password123!","operator":"cmcc"}`))
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
	dec, err := DecryptData(decRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dec, []byte("TS2024001")) {
		t.Fatalf("unexpected decrypted content: %s", string(dec))
	}
	if cfg.AccountFor(campus.Nanhu) != "TS2024001@cmcc" {
		t.Fatalf("expected TS2024001@cmcc, got %s", cfg.AccountFor(campus.Nanhu))
	}
	if cfg.OperatorName() != "移动" {
		t.Fatalf("expected 移动, got %s", cfg.OperatorName())
	}
}

func TestCampusAccountDefaultsAndOverride(t *testing.T) {
	cfg := Config{Account: "student"}
	if cfg.AccountFor(campus.Wenchang) != "student@xyw" || cfg.AccountFor(campus.Nanhu) != "student@xyw" {
		t.Fatal("the verified account realm changed across campuses")
	}
	cfg.AccountSuffix, cfg.SuffixExplicit = "@dx", true
	if cfg.AccountFor(campus.Nanhu) != "student@dx" {
		t.Fatal("explicit operator suffix was ignored")
	}
}

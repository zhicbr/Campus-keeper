//go:build windows

package config

import (
	"bytes"
	"os"
	"syscall"
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

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	target := dir + "\\test_config.dat"

	// Initial write
	initialData := []byte("first content")
	if err := atomicWriteFile(target, initialData); err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(content, initialData) {
		t.Fatalf("unexpected content after first write: %q, err: %v", content, err)
	}

	// Overwrite atomically
	updatedData := []byte("updated content that replaces previous")
	if err := atomicWriteFile(target, updatedData); err != nil {
		t.Fatalf("atomicWriteFile overwrite failed: %v", err)
	}
	content, err = os.ReadFile(target)
	if err != nil || !bytes.Equal(content, updatedData) {
		t.Fatalf("unexpected content after overwrite: %q, err: %v", content, err)
	}

	// Real replacement failure path: Target file is locked without FILE_SHARE_DELETE,
	// causing os.Rename (MoveFileEx) to fail with ERROR_ACCESS_DENIED.
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(
		targetPtr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ, // omitting FILE_SHARE_DELETE prevents rename replacement
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("failed to lock target file for test: %v", err)
	}

	// Attempt atomic overwrite while target file is locked; must fail at the rename step
	err = atomicWriteFile(target, []byte("should fail during rename"))
	if err == nil {
		syscall.CloseHandle(h)
		t.Fatalf("expected atomicWriteFile to fail on locked target, got nil")
	}

	// Release lock and verify:
	syscall.CloseHandle(h)

	// 1. Original target file remained completely intact
	content, err = os.ReadFile(target)
	if err != nil || !bytes.Equal(content, updatedData) {
		t.Fatalf("original file was corrupted after rename failure: %q", content)
	}

	// 2. The temporary file created during atomicWriteFile was cleaned up by defer
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected temporary files to be cleaned up after rename failure, found %d entries", len(entries))
	}
}

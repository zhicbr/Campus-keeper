//go:build windows

package config

import (
	"bytes"
	"testing"
)

func TestDPAPIEncryptDecrypt(t *testing.T) {
	orig := []byte("student123:my_secret_pass_888:cmcc")
	enc, err := EncryptData(orig)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if bytes.Equal(enc, orig) {
		t.Fatal("encrypted data matches plaintext!")
	}
	dec, err := DecryptData(enc)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("got %q, want %q", dec, orig)
	}
}

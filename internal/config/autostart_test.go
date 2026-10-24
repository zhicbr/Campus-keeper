//go:build windows

package config

import (
	"strings"
	"testing"
)

func TestAutoStartToggle(t *testing.T) {
	// Isolate unit test from real production autostart registry entry
	oldName := runValName
	runValName = "WenchangCampusKeeper_UnitTest"
	defer func() {
		_ = SetAutoStart(false)
		runValName = oldName
	}()

	// Ensure clean starting state
	_ = SetAutoStart(false)

	if err := SetAutoStart(true); err != nil {
		if strings.Contains(err.Error(), "Access is denied") {
			t.Skipf("skipping test in restricted sandbox environment: %v", err)
		}
		t.Fatalf("SetAutoStart(true) failed: %v", err)
	}
	if !IsAutoStartEnabled() {
		t.Fatalf("expected IsAutoStartEnabled to be true after SetAutoStart(true)")
	}

	if err := SetAutoStart(false); err != nil {
		t.Fatalf("SetAutoStart(false) failed: %v", err)
	}
	if IsAutoStartEnabled() {
		t.Fatalf("expected IsAutoStartEnabled to be false after SetAutoStart(false)")
	}
}

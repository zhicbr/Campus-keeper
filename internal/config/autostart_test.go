//go:build windows

package config

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
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

func TestAutoStartMismatchedPath(t *testing.T) {
	oldName := runValName
	runValName = "WenchangCampusKeeper_UnitTestMismatch"
	defer func() {
		_ = SetAutoStart(false)
		runValName = oldName
	}()

	_ = SetAutoStart(false)

	// Simulate an obsolete path from a moved directory
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if strings.Contains(err.Error(), "Access is denied") {
			t.Skipf("skipping test in restricted sandbox environment: %v", err)
		}
		t.Fatalf("CreateKey failed: %v", err)
	}
	defer k.Close()

	fakeCmd := `"C:\OldFolder\wenchang-campus-keeper.exe" -quiet`
	if err := k.SetStringValue(runValName, fakeCmd); err != nil {
		t.Fatalf("SetStringValue failed: %v", err)
	}

	// Must report false because the registered path does not match current executable
	if IsAutoStartEnabled() {
		t.Fatalf("expected IsAutoStartEnabled to be false when registered path does not match current executable")
	}

	// Calling SetAutoStart(true) should heal/calibrate it to the current executable path
	if err := SetAutoStart(true); err != nil {
		t.Fatalf("SetAutoStart(true) failed: %v", err)
	}
	if !IsAutoStartEnabled() {
		t.Fatalf("expected IsAutoStartEnabled to be true after healing with SetAutoStart(true)")
	}
}

func TestExtractExecPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"C:\Program Files\app.exe" -quiet`, `C:\Program Files\app.exe`},
		{`"D:\app.exe"`, `D:\app.exe`},
		{`C:\app.exe -quiet`, `C:\app.exe`},
		{`"E:\app.exe`, `E:\app.exe`},
		{`   `, ``},
	}
	for _, c := range cases {
		got := extractExecPath(c.in)
		if got != c.want {
			t.Errorf("extractExecPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

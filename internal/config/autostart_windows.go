//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// runValName is the registry value name in HKCU\...\Run.
// It is a package variable so unit tests can use an isolated key name.
var runValName = "WenchangCampusKeeper"

// IsAutoStartEnabled reports whether the auto-start registry key exists in HKCU\...\Run.
func IsAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	val, _, err := k.GetStringValue(runValName)
	if err != nil || strings.TrimSpace(val) == "" {
		return false
	}
	return true
}

// SetAutoStart adds or removes the auto-start entry in HKCU\...\Run.
// When enabled, it registers the current executable with the -quiet flag.
func SetAutoStart(enable bool) error {
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()

		cmd := fmt.Sprintf("\"%s\" -quiet", filepath.Clean(exe))
		return k.SetStringValue(runValName, cmd)
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer k.Close()

	err = k.DeleteValue(runValName)
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

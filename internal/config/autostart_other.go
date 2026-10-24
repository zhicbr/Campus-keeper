//go:build !windows

package config

func IsAutoStartEnabled() bool {
	return false
}

func SetAutoStart(enable bool) error {
	return nil
}

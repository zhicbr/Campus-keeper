//go:build windows

package main

import "testing"

func TestCreateShieldIcons(t *testing.T) {
	for _, kind := range []iconKind{iconCampusOnline, iconExternalOnline, iconConnecting, iconOffline} {
		hIcon := createShieldIcon(kind)
		if hIcon == 0 {
			t.Fatalf("failed to create icon for kind %d", kind)
		}
		destroyIcon.Call(hIcon)
	}
}

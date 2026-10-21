//go:build windows

package ui

import (
	"syscall"
	"testing"
	"unsafe"
)

func TestCreateShieldIcons(t *testing.T) {
	for _, kind := range []IconKind{
		IconCampusOnline,
		IconExternalOnline,
		IconConnecting,
		IconOffline,
	} {
		hIcon := CreateShieldIcon(kind)
		if hIcon == 0 {
			t.Fatalf("Failed to create shield icon for kind %v", kind)
		}
		destroyIcon.Call(hIcon)
	}
}

func TestShellNotifyIconDirect(t *testing.T) {
	nid := notifyIconData{
		size:  uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:  0,
		id:    100,
		flags: nifMessageIconTip,
	}
	t.Logf("unsafe.Sizeof(notifyIconData{}): %d", nid.size)

	// Test with Standard System Icon IDI_APPLICATION = 32512
	hIcon, _, _ := user32.NewProc("LoadIconW").Call(0, 32512)
	if hIcon == 0 {
		t.Fatal("Failed to load standard system icon")
	}
	nid.icon = hIcon

	copy(nid.tip[:], syscall.StringToUTF16("Test Tip Direct"))

	res, _, err := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	t.Logf("stdIcon res: %v, err: %v", res, err)

	// Clean up
	shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

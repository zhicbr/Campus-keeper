//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

func TestCreateShieldIcons(t *testing.T) {
	for _, kind := range []iconKind{iconCampusOnline, iconExternalOnline, iconConnecting, iconOffline} {
		hIcon := createShieldIcon(kind)
		if hIcon == 0 {
			t.Fatalf("failed to create icon for kind %d", kind)
		}
		destroyIcon.Call(hIcon)
	}
}

func TestShellNotifyIconDirect(t *testing.T) {
	openDesktop := user32.NewProc("OpenDesktopW")
	setThreadDesktop := user32.NewProc("SetThreadDesktop")
	if hDesk, _, _ := openDesktop.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Default"))), 0, 0, 0x10000000); hDesk != 0 {
		setThreadDesktop.Call(hDesk)
	}

	loadIcon := user32.NewProc("LoadIconW")
	stdIcon, _, _ := loadIcon.Call(0, 32512)
	hIcon := createShieldIcon(iconConnecting)
	if hIcon == 0 {
		t.Fatal("failed to create shield icon")
	}
	defer destroyIcon.Call(hIcon)

	instance, _, _ := getModuleHandle.Call(0)
	className := syscall.StringToUTF16Ptr("TestNotifyIconClass")
	callback := syscall.NewCallback(func(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
		res, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return res
	})
	wc := windowClass{size: uint32(unsafe.Sizeof(windowClass{})), proc: callback, instance: instance, className: className}
	registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		t.Fatal("failed to create window")
	}
	defer destroyWindow.Call(hwnd)

	t.Logf("unsafe.Sizeof(notifyIconData{}): %d", unsafe.Sizeof(notifyIconData{}))
	data := notifyIconData{
		size:     uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:     hwnd,
		id:       1,
		flags:    nifMessageIconTip,
		callback: trayMessage,
		icon:     stdIcon,
	}
	copy(data.tip[:], syscall.StringToUTF16("CampusKeeper"))
	resStd, _, errStd := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	t.Logf("stdIcon res: %d, err: %v", resStd, errStd)
	if resStd != 0 {
		shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	}

	data.icon = hIcon
	resShield, _, errShield := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if resShield == 0 {
		t.Fatalf("shieldIcon failed: %v", errShield)
	}
	shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func TestActivateVisualStyles(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "test.manifest")
	if err := os.WriteFile(manifestPath, []byte(appManifestXML), 0644); err != nil {
		t.Fatal(err)
	}
	hAct, cookie := enableVisualStyles(manifestPath)
	if hAct == 0 {
		t.Fatal("failed to enable visual styles")
	}
	t.Logf("Visual styles enabled successfully, hAct: %d, cookie: %d", hAct, cookie)
}

//go:build windows

package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"wenchang-campus-keeper/internal/ui"
)

var (
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")
	user32DLL   = syscall.NewLazyDLL("user32.dll")
	createMutex = kernel32DLL.NewProc("CreateMutexW")
	mutexHandle uintptr
)

func acquireSingleInstance() error {
	name, err := syscall.UTF16PtrFromString("Global\\WenchangCampusKeeper")
	if err != nil {
		return err
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return fmt.Errorf("创建单实例锁失败：%w", callErr)
	}
	mutexHandle = handle
	if errors.Is(callErr, syscall.Errno(183)) {
		return errors.New("程序已在运行中")
	}
	return nil
}

func wakeExistingInstance() {
	name := syscall.StringToUTF16Ptr("WenchangCampusKeeperTray")
	hwnd, _, _ := user32DLL.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(name)), 0)
	if hwnd != 0 {
		user32DLL.NewProc("PostMessageW").Call(hwnd, ui.WakeMessage, 0, 0)
	}
}

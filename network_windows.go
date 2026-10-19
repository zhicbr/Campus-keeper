//go:build windows

package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	ipHelper                  = syscall.NewLazyDLL("iphlpapi.dll")
	notifyIPInterface         = ipHelper.NewProc("NotifyIpInterfaceChange")
	notifyUnicastIPAddress    = ipHelper.NewProc("NotifyUnicastIpAddressChange")
	cancelNetworkNotification = ipHelper.NewProc("CancelMibChangeNotify2")
	createMutex               = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	mutexHandle               uintptr
)

func startNetworkNotifications() (<-chan struct{}, func(), error) {
	ch := make(chan struct{}, 1)
	callback := syscall.NewCallback(func(_, _, _ uintptr) uintptr {
		select {
		case ch <- struct{}{}:
		default:
		}
		return 0
	})
	var interfaceHandle, addressHandle uintptr
	r1, _, _ := notifyIPInterface.Call(2, callback, 0, 0, uintptr(unsafe.Pointer(&interfaceHandle)))
	r2, _, _ := notifyUnicastIPAddress.Call(2, callback, 0, 0, uintptr(unsafe.Pointer(&addressHandle)))
	if r1 != 0 && r2 != 0 {
		return nil, nil, fmt.Errorf("接口通知错误 %d，地址通知错误 %d", r1, r2)
	}
	closeFn := func() {
		if interfaceHandle != 0 {
			cancelNetworkNotification.Call(interfaceHandle)
		}
		if addressHandle != 0 {
			cancelNetworkNotification.Call(addressHandle)
		}
	}
	return ch, closeFn, nil
}

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
	hwnd, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(name)), 0)
	if hwnd != 0 {
		user32.NewProc("PostMessageW").Call(hwnd, wakeMessage, 0, 0)
	}
}

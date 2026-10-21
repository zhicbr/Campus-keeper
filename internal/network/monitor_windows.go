//go:build windows

package network

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	ipHelper                  = syscall.NewLazyDLL("iphlpapi.dll")
	notifyIPInterface         = ipHelper.NewProc("NotifyIpInterfaceChange")
	notifyUnicastIPAddress    = ipHelper.NewProc("NotifyUnicastIpAddressChange")
	cancelNetworkNotification = ipHelper.NewProc("CancelMibChangeNotify2")
)

func StartNetworkNotifications() (<-chan struct{}, func(), error) {
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

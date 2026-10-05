//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	wlanAPI            = syscall.NewLazyDLL("wlanapi.dll")
	wlanOpenHandle     = wlanAPI.NewProc("WlanOpenHandle")
	wlanCloseHandle    = wlanAPI.NewProc("WlanCloseHandle")
	wlanEnumInterfaces = wlanAPI.NewProc("WlanEnumInterfaces")
	wlanQueryInterface = wlanAPI.NewProc("WlanQueryInterface")
	wlanConnect        = wlanAPI.NewProc("WlanConnect")
	wlanFreeMemory     = wlanAPI.NewProc("WlanFreeMemory")
)

const studentWiFiProfile = "CUMT_Stu"

type wlanConnectionParameters struct {
	mode         uint32
	profile      *uint16
	ssid         uintptr
	desiredBSSID uintptr
	bssType      uint32
	flags        uint32
}

type wlanInterfaceInfo struct {
	guid        [16]byte
	description [256]uint16
	state       uint32
}

type wlanSSID struct {
	length uint32
	bytes  [32]byte
}

type wlanAssociation struct {
	ssid     wlanSSID
	bssType  uint32
	bssid    [6]byte
	_        [2]byte
	phyType  uint32
	phyIndex uint32
	quality  uint32
	rxRate   uint32
	txRate   uint32
}

type wlanConnection struct {
	state       uint32
	mode        uint32
	profileName [256]uint16
	association wlanAssociation
}

// requestSavedWiFi asks Windows to connect using an existing profile. The
// request is asynchronous; IP acquisition is observed by monitorLink.
// A connected or connecting WLAN is never replaced by this program.
func requestSavedWiFi(profile string) (string, error) {
	if err := wlanOpenHandle.Find(); err != nil {
		return "", fmt.Errorf("Windows WLAN API 不可用：%w", err)
	}
	var version uint32
	var handle uintptr
	code, _, _ := wlanOpenHandle.Call(2, 0, uintptr(unsafe.Pointer(&version)), uintptr(unsafe.Pointer(&handle)))
	if code != 0 || handle == 0 {
		return "", fmt.Errorf("打开 Windows WLAN 失败：错误码 %d", code)
	}
	defer wlanCloseHandle.Call(handle, 0)
	var list uintptr
	code, _, _ = wlanEnumInterfaces.Call(handle, 0, uintptr(unsafe.Pointer(&list)))
	if code != 0 || list == 0 {
		return "", fmt.Errorf("枚举无线网卡失败：错误码 %d", code)
	}
	defer wlanFreeMemory.Call(list)
	count := *(*uint32)(unsafe.Pointer(list))
	if count == 0 || count > 32 {
		return "", fmt.Errorf("没有可用的无线网卡")
	}
	base := list + 8
	var disconnected *wlanInterfaceInfo
	for i := uint32(0); i < count; i++ {
		info := (*wlanInterfaceInfo)(unsafe.Pointer(base + uintptr(i)*unsafe.Sizeof(wlanInterfaceInfo{})))
		switch info.state {
		case 1: // connected: preserve whatever network the user selected.
			return "已连接", nil
		case 2, 3, 5, 6, 7: // ad hoc, disconnecting, associating, discovering, authenticating.
			return "连接进行中", nil
		case 4: // disconnected
			if disconnected == nil {
				disconnected = info
			}
		}
	}
	if disconnected == nil {
		return "", fmt.Errorf("无线网卡尚未就绪")
	}
	name, err := syscall.UTF16PtrFromString(profile)
	if err != nil {
		return "", err
	}
	params := wlanConnectionParameters{mode: 0, profile: name, bssType: 1} // profile, infrastructure
	code, _, _ = wlanConnect.Call(handle, uintptr(unsafe.Pointer(&disconnected.guid)), uintptr(unsafe.Pointer(&params)), 0)
	if code != 0 {
		return "", fmt.Errorf("请求连接 Wi-Fi %s 失败：Windows 错误码 %d", profile, code)
	}
	return "已请求连接", nil
}

// connectedSSID calls the native WLAN API without launching netsh every few
// seconds. Failure simply leaves Wi-Fi location inference unavailable.
func connectedSSID() string {
	if wlanOpenHandle.Find() != nil {
		return ""
	}
	var version uint32
	var handle uintptr
	result, _, _ := wlanOpenHandle.Call(2, 0, uintptr(unsafe.Pointer(&version)), uintptr(unsafe.Pointer(&handle)))
	if result != 0 || handle == 0 {
		return ""
	}
	defer wlanCloseHandle.Call(handle, 0)
	var list uintptr
	result, _, _ = wlanEnumInterfaces.Call(handle, 0, uintptr(unsafe.Pointer(&list)))
	if result != 0 || list == 0 {
		return ""
	}
	defer wlanFreeMemory.Call(list)
	count := *(*uint32)(unsafe.Pointer(list))
	if count > 32 {
		return ""
	}
	base := list + 8 // WLAN_INTERFACE_INFO_LIST header: count, current index.
	for i := uint32(0); i < count; i++ {
		info := (*wlanInterfaceInfo)(unsafe.Pointer(base + uintptr(i)*unsafe.Sizeof(wlanInterfaceInfo{})))
		if info.state != 1 { // wlan_interface_state_connected
			continue
		}
		var size uint32
		var data uintptr
		var valueType uint32
		result, _, _ = wlanQueryInterface.Call(handle, uintptr(unsafe.Pointer(&info.guid)), 7, 0,
			uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&valueType)))
		if result != 0 || data == 0 {
			continue
		}
		minimum := unsafe.Offsetof(wlanConnection{}.association) + unsafe.Sizeof(wlanSSID{})
		if uintptr(size) >= minimum {
			connection := (*wlanConnection)(unsafe.Pointer(data))
			ssid := connection.association.ssid
			if ssid.length > 0 && ssid.length <= uint32(len(ssid.bytes)) {
				name := string(ssid.bytes[:ssid.length])
				wlanFreeMemory.Call(data)
				return name
			}
		}
		wlanFreeMemory.Call(data)
	}
	return ""
}

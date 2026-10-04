//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	wlanAPI            = syscall.NewLazyDLL("wlanapi.dll")
	wlanOpenHandle     = wlanAPI.NewProc("WlanOpenHandle")
	wlanCloseHandle    = wlanAPI.NewProc("WlanCloseHandle")
	wlanEnumInterfaces = wlanAPI.NewProc("WlanEnumInterfaces")
	wlanQueryInterface = wlanAPI.NewProc("WlanQueryInterface")
	wlanFreeMemory     = wlanAPI.NewProc("WlanFreeMemory")
)

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

//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

var (
	getIPForwardTable = ipHelper.NewProc("GetIpForwardTable")
	getIPNetTable     = ipHelper.NewProc("GetIpNetTable")
)

// gatewayFingerprint reads only the Windows IPv4 route and neighbor tables.
// It never probes or changes the network. The result belongs to ifIndex, not
// necessarily to the operating system's preferred default route.
func gatewayFingerprint(ifIndex int) (string, string, error) {
	routes, err := readIPHelperTable(getIPForwardTable)
	if err != nil {
		return "", "", err
	}
	const routeSize = 56 // MIB_IPFORWARDROW: 14 DWORDs.
	count, err := tableCount(routes, routeSize)
	if err != nil {
		return "", "", err
	}
	var gateway net.IP
	bestMetric := ^uint32(0)
	for i := 0; i < count; i++ {
		row := routes[4+i*routeSize:]
		if binary.LittleEndian.Uint32(row[0:4]) != 0 || binary.LittleEndian.Uint32(row[16:20]) != uint32(ifIndex) {
			continue
		}
		metric := binary.LittleEndian.Uint32(row[36:40])
		if gateway == nil || metric < bestMetric {
			bestMetric = metric
			gateway = net.IPv4(row[12], row[13], row[14], row[15])
		}
	}
	if gateway == nil || gateway.Equal(net.IPv4zero) {
		return "", "", fmt.Errorf("所选网卡没有 IPv4 默认网关")
	}
	neighbors, err := readIPHelperTable(getIPNetTable)
	if err != nil {
		return gateway.String(), "", err
	}
	const neighborSize = 24 // MIB_IPNETROW: index, length, MAC[8], IPv4, type.
	count, err = tableCount(neighbors, neighborSize)
	if err != nil {
		return gateway.String(), "", err
	}
	wantIP := gateway.To4()
	for i := 0; i < count; i++ {
		row := neighbors[4+i*neighborSize:]
		if binary.LittleEndian.Uint32(row[0:4]) != uint32(ifIndex) || !bytes.Equal(row[16:20], wantIP) {
			continue
		}
		if binary.LittleEndian.Uint32(row[4:8]) != 6 || binary.LittleEndian.Uint32(row[20:24]) == 2 {
			continue
		}
		mac := net.HardwareAddr(row[8:14]).String()
		return gateway.String(), mac, nil
	}
	return gateway.String(), "", fmt.Errorf("默认网关尚无有效邻居记录")
}

func readIPHelperTable(proc *syscall.LazyProc) ([]byte, error) {
	size := uint32(4096)
	for attempt := 0; attempt < 3; attempt++ {
		if size < 4 || size > 1<<20 {
			return nil, fmt.Errorf("IP Helper 表大小异常：%d", size)
		}
		buffer := make([]byte, size)
		code, _, _ := proc.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0)
		if code == 0 {
			if size > uint32(len(buffer)) {
				return nil, fmt.Errorf("IP Helper 返回了超出缓冲区的长度")
			}
			return buffer[:size], nil
		}
		if code != 122 { // ERROR_INSUFFICIENT_BUFFER
			return nil, fmt.Errorf("读取 Windows IP Helper 表失败：错误码 %d", code)
		}
	}
	return nil, fmt.Errorf("Windows IP Helper 表不断变化")
}

func tableCount(table []byte, rowSize int) (int, error) {
	if len(table) < 4 {
		return 0, fmt.Errorf("Windows IP Helper 表为空")
	}
	count := binary.LittleEndian.Uint32(table[:4])
	if uint64(count)*uint64(rowSize) > uint64(len(table)-4) {
		return 0, fmt.Errorf("Windows IP Helper 表长度异常")
	}
	return int(count), nil
}

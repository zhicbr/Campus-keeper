//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

type iconKind int

const (
	iconCampusOnline iconKind = iota
	iconExternalOnline
	iconConnecting
	iconOffline
)

var (
	gdi32              = syscall.NewLazyDLL("gdi32.dll")
	createDIBSection   = gdi32.NewProc("CreateDIBSection")
	createBitmap       = gdi32.NewProc("CreateBitmap")
	deleteObject       = gdi32.NewProc("DeleteObject")
	createIconIndirect = user32.NewProc("CreateIconIndirect")
	destroyIcon        = user32.NewProc("DestroyIcon")
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [1]uint32
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

func rgba(r, g, b, a uint8) uint32 {
	// Little-endian in memory: B, G, R, A
	return uint32(b) | (uint32(g) << 8) | (uint32(r) << 16) | (uint32(a) << 24)
}

func createShieldIcon(kind iconKind) uintptr {
	const w, h = 16, 16
	var (
		fillColor   uint32
		borderColor uint32
		white       = rgba(255, 255, 255, 255)
	)

	switch kind {
	case iconCampusOnline:
		fillColor = rgba(16, 185, 129, 255) // Emerald green #10B981
		borderColor = rgba(4, 120, 87, 255) // #047857
	case iconExternalOnline:
		fillColor = rgba(14, 165, 233, 255)  // Sky blue #0EA5E9
		borderColor = rgba(3, 105, 161, 255) // #0369A1
	case iconConnecting:
		fillColor = rgba(245, 158, 11, 255) // Amber gold #F59E0B
		borderColor = rgba(180, 83, 9, 255) // #B45309
	default:
		fillColor = rgba(100, 116, 139, 255) // Slate gray #64748B
		borderColor = rgba(51, 65, 85, 255)  // #334155
	}

	pixels := make([]uint32, w*h)

	// Shield geometry table: [row] = (minX, maxX) inclusive
	shieldRows := [16][2]int{
		0:  {-1, -1},
		1:  {3, 12},
		2:  {2, 13},
		3:  {2, 13},
		4:  {2, 13},
		5:  {2, 13},
		6:  {2, 13},
		7:  {2, 13},
		8:  {2, 13},
		9:  {3, 12},
		10: {4, 11},
		11: {5, 10},
		12: {6, 9},
		13: {7, 8},
		14: {-1, -1},
		15: {-1, -1},
	}

	// 1. Draw shield body and border
	for y := 0; y < h; y++ {
		minX, maxX := shieldRows[y][0], shieldRows[y][1]
		if minX < 0 {
			continue
		}
		for x := minX; x <= maxX; x++ {
			isBorder := (x == minX || x == maxX || y == 1 || y == 13)
			if !isBorder && y > 1 && y < 13 {
				prevMin, prevMax := shieldRows[y-1][0], shieldRows[y-1][1]
				nextMin, nextMax := shieldRows[y+1][0], shieldRows[y+1][1]
				if x <= prevMin || x >= prevMax || x <= nextMin || x >= nextMax {
					isBorder = true
				}
			}
			idx := y*w + x
			if isBorder {
				pixels[idx] = borderColor
			} else {
				pixels[idx] = fillColor
			}
		}
	}

	// 2. Draw interior symbol in white
	setPixel := func(x, y int) {
		if x >= 0 && x < w && y >= 0 && y < h {
			pixels[y*w+x] = white
		}
	}

	switch kind {
	case iconCampusOnline:
		// Crisp checkmark ✓
		setPixel(4, 7)
		setPixel(5, 8)
		setPixel(6, 9)
		setPixel(7, 8)
		setPixel(8, 7)
		setPixel(9, 6)
		setPixel(10, 5)
		setPixel(11, 4)
		// 1px thickness
		setPixel(5, 9)
		setPixel(6, 10)
		setPixel(7, 9)
		setPixel(8, 8)
		setPixel(9, 7)
		setPixel(10, 6)

	case iconExternalOnline:
		// Globe / Wi-Fi wave symbol
		for x := 5; x <= 10; x++ {
			setPixel(x, 7)
		}
		for y := 5; y <= 9; y++ {
			setPixel(7, y)
			setPixel(8, y)
		}
		setPixel(6, 5)
		setPixel(9, 5)
		setPixel(6, 9)
		setPixel(9, 9)

	case iconConnecting:
		// Pulse dots
		setPixel(4, 7)
		setPixel(5, 7)
		setPixel(4, 8)
		setPixel(5, 8)

		setPixel(7, 7)
		setPixel(8, 7)
		setPixel(7, 8)
		setPixel(8, 8)

		setPixel(10, 7)
		setPixel(11, 7)
		setPixel(10, 8)
		setPixel(11, 8)

	case iconOffline:
		// Clean horizontal slash / dash
		for x := 5; x <= 10; x++ {
			setPixel(x, 7)
			setPixel(x, 8)
		}
	}

	// 3. Create Windows DIB and HICON
	bmi := bitmapInfo{
		bmiHeader: bitmapInfoHeader{
			biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			biWidth:       w,
			biHeight:      -h, // top-down
			biPlanes:      1,
			biBitCount:    32,
			biCompression: 0,
		},
	}

	var bits unsafe.Pointer
	hColor, _, _ := createDIBSection.Call(0, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hColor == 0 || bits == nil {
		return 0
	}
	defer deleteObject.Call(hColor)

	copy(unsafe.Slice((*uint32)(bits), w*h), pixels)

	hMask, _, _ := createBitmap.Call(w, h, 1, 1, 0)
	if hMask == 0 {
		return 0
	}
	defer deleteObject.Call(hMask)

	ii := iconInfo{
		fIcon:    1,
		hbmMask:  hMask,
		hbmColor: hColor,
	}

	hIcon, _, _ := createIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return hIcon
}

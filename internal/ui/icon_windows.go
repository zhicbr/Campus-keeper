//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

type IconKind int

const (
	IconCampusOnline IconKind = iota
	IconExternalOnline
	IconConnecting
	IconOffline
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

// A campus link may be associated without actually reaching the internet.
func iconKindForState(state string, isCampus bool) IconKind {
	switch state {
	case "online":
		if isCampus {
			return IconCampusOnline
		}
		return IconExternalOnline
	case "", "checking", "wifi-connecting", "wifi-wait-ip", "authenticating", "retry-wait":
		return IconConnecting
	default:
		return IconOffline
	}
}

func CreateShieldIcon(kind IconKind) uintptr {
	const w, h = 16, 16
	var (
		fillColor   uint32
		borderColor uint32
		white       = rgba(255, 255, 255, 255)
	)

	switch kind {
	case IconCampusOnline:
		fillColor = rgba(16, 185, 129, 255) // Emerald green #10B981
		borderColor = rgba(4, 120, 87, 255) // #047857
	case IconExternalOnline:
		fillColor = rgba(14, 165, 233, 255)  // Sky blue #0EA5E9
		borderColor = rgba(3, 105, 161, 255) // #0369A1
	case IconConnecting:
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

	// 1. Fill base shield
	for y := 0; y < h; y++ {
		minX, maxX := shieldRows[y][0], shieldRows[y][1]
		if minX == -1 {
			continue
		}
		for x := minX; x <= maxX; x++ {
			if x == minX || x == maxX || y == 1 || y == 13 {
				pixels[y*w+x] = borderColor
			} else {
				pixels[y*w+x] = fillColor
			}
		}
	}

	// 2. Draw glyphs
	switch kind {
	case IconCampusOnline:
		// Checkmark coordinates
		checkCoords := [][2]int{
			{5, 7}, {6, 8}, {7, 9}, {8, 8}, {9, 7}, {10, 6}, {10, 5},
		}
		for _, pt := range checkCoords {
			pixels[pt[1]*w+pt[0]] = white
		}

	case IconExternalOnline:
		// Stylized 'E' or small globe dot in center
		coords := [][2]int{
			{6, 5}, {7, 5}, {8, 5}, {9, 5},
			{6, 6},
			{6, 7}, {7, 7}, {8, 7},
			{6, 8},
			{6, 9}, {7, 9}, {8, 9}, {9, 9},
		}
		for _, pt := range coords {
			pixels[pt[1]*w+pt[0]] = white
		}

	case IconConnecting:
		// Horizontal ellipsis (dots)
		coords := [][2]int{
			{4, 7}, {5, 7},
			{7, 7}, {8, 7},
			{10, 7}, {11, 7},
		}
		for _, pt := range coords {
			pixels[pt[1]*w+pt[0]] = white
		}

	default: // iconOffline
		// 'X' glyph
		coords := [][2]int{
			{5, 5}, {10, 5},
			{6, 6}, {9, 6},
			{7, 7}, {8, 7},
			{6, 8}, {9, 8},
			{5, 9}, {10, 9},
		}
		for _, pt := range coords {
			pixels[pt[1]*w+pt[0]] = white
		}
	}

	// 3. Create GDI Bitmaps
	var bmi bitmapInfo
	bmi.bmiHeader.biSize = uint32(unsafe.Sizeof(bmi.bmiHeader))
	bmi.bmiHeader.biWidth = w
	bmi.bmiHeader.biHeight = -h // Top-down DIB
	bmi.bmiHeader.biPlanes = 1
	bmi.bmiHeader.biBitCount = 32
	bmi.bmiHeader.biCompression = 0 // BI_RGB

	var pBits *uint32
	hbmColor, _, _ := createDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&bmi)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&pBits)),
		0,
		0,
	)
	if hbmColor == 0 || pBits == nil {
		return 0
	}
	defer deleteObject.Call(hbmColor)

	// Copy pixels to DIBSection
	destSlice := unsafe.Slice(pBits, w*h)
	copy(destSlice, pixels)

	// Create 1-bit monochrome mask (0 = opaque for color bitmap with alpha on Windows XP+)
	maskBits := make([]byte, (w+15)/16*2*h)
	for y := 0; y < h; y++ {
		minX, maxX := shieldRows[y][0], shieldRows[y][1]
		for x := 0; x < w; x++ {
			byteIdx := y*((w+15)/16*2) + (x / 8)
			bit := byte(1 << (7 - (x % 8)))
			if minX != -1 && x >= minX && x <= maxX {
				// 0 = transparent mask (display color)
			} else {
				// 1 = transparent pixel
				maskBits[byteIdx] |= bit
			}
		}
	}

	hbmMask, _, _ := createBitmap.Call(
		uintptr(w),
		uintptr(h),
		1,
		1,
		uintptr(unsafe.Pointer(&maskBits[0])),
	)
	if hbmMask == 0 {
		return 0
	}
	defer deleteObject.Call(hbmMask)

	ii := iconInfo{
		fIcon:    1,
		xHotspot: 0,
		yHotspot: 0,
		hbmMask:  hbmMask,
		hbmColor: hbmColor,
	}

	hIcon, _, _ := createIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return hIcon
}

//go:build windows

package config

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func EncryptData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot encrypt empty data")
	}
	var inBlob, outBlob dataBlob
	inBlob.cbData = uint32(len(data))
	inBlob.pbData = &data[0]

	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0, 1, // CRYPTPROTECT_UI_FORBIDDEN = 1
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))

	res := make([]byte, outBlob.cbData)
	copy(res, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return res, nil
}

func DecryptData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot decrypt empty data")
	}
	var inBlob, outBlob dataBlob
	inBlob.cbData = uint32(len(data))
	inBlob.pbData = &data[0]

	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0, 1, // CRYPTPROTECT_UI_FORBIDDEN = 1
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))

	res := make([]byte, outBlob.cbData)
	copy(res, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return res, nil
}

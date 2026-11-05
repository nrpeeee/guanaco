package main

import (
	"errors"
	"syscall"
	"unsafe"
)

// DPAPI: the blob can only be decrypted by the same Windows user on the same
// machine. No key material is stored by Guanaco at all.

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procProtectData   = crypt32.NewProc("CryptProtectData")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree     = kernel32.NewProc("LocalFree")
	dpapiEntropy      = []byte("Guanaco-credentials-v1")
)

const cryptprotectUIForbidden = 0x1

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

func seal(plain []byte, _ string) ([]byte, error) {
	var out dataBlob
	r, _, err := procProtectData.Call(
		uintptr(unsafe.Pointer(newBlob(plain))), 0,
		uintptr(unsafe.Pointer(newBlob(dpapiEntropy))), 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, errors.New("CryptProtectData: " + err.Error())
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unseal(sealed []byte, _ string) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, errors.New("empty credentials file")
	}
	var out dataBlob
	r, _, err := procUnprotectData.Call(
		uintptr(unsafe.Pointer(newBlob(sealed))), 0,
		uintptr(unsafe.Pointer(newBlob(dpapiEntropy))), 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, errors.New("CryptUnprotectData: " + err.Error())
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func credProtection(string) string {
	return "Encrypted with Windows DPAPI — only your Windows account on this PC can decrypt it."
}

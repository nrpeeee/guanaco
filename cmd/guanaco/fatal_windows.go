package main

import (
	"syscall"
	"unsafe"
)

// A -H=windowsgui build has no console, so a failed start used to look like
// "double-click does nothing". This surfaces the reason before we exit.

var procMessageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

const mbIconError = 0x10

func notifyFatal(msg string) {
	text, err := syscall.UTF16PtrFromString(msg)
	if err != nil {
		return
	}
	caption, _ := syscall.UTF16PtrFromString("Guanaco")
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(caption)),
		mbIconError)
}

//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// copyToClipboard writes s to the Windows clipboard so the customer
// doesn't have to manually select-and-Ctrl-C the device ID before
// pasting it into WeChat.  This is best-effort: if the clipboard is
// locked by another process or we run in Session 0 (services), we
// silently bail.  Customer-facing UX must not regress just because
// the clipboard was momentarily busy.
//
// Implementation notes:
//   - We allocate via GlobalAlloc with GMEM_MOVEABLE; SetClipboardData
//     takes ownership of that handle, so we must NOT GlobalFree on
//     success.
//   - We write UTF-16 (CF_UNICODETEXT = 13) because device IDs are
//     ASCII today but Hostname banners may grow Chinese tomorrow.
//
//go:nocheckptr
func copyToClipboard(s string) {
	const (
		CF_UNICODETEXT = 13
		GMEM_MOVEABLE  = 0x0002
	)
	user32 := syscall.NewLazyDLL("user32.dll")
	kernel32 := syscall.NewLazyDLL("kernel32.dll")

	openClipboard := user32.NewProc("OpenClipboard")
	emptyClipboard := user32.NewProc("EmptyClipboard")
	setClipboardData := user32.NewProc("SetClipboardData")
	closeClipboard := user32.NewProc("CloseClipboard")
	globalAlloc := kernel32.NewProc("GlobalAlloc")
	globalLock := kernel32.NewProc("GlobalLock")
	globalUnlock := kernel32.NewProc("GlobalUnlock")
	globalFree := kernel32.NewProc("GlobalFree")

	utf16, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	size := uintptr(len(utf16) * 2)

	hMem, _, _ := globalAlloc.Call(GMEM_MOVEABLE, size)
	if hMem == 0 {
		return
	}
	freed := false
	defer func() {
		if !freed {
			globalFree.Call(hMem)
		}
	}()

	pMem, _, _ := globalLock.Call(hMem)
	if pMem == 0 {
		return
	}
	// Why memmove via RtlMoveMemory rather than unsafe.Slice on the
	// uintptr: vet (rightly) flags `unsafe.Pointer(uintptr-from-syscall)`
	// because if the uintptr came from a Go-managed allocation it could
	// have moved.  GlobalAlloc memory is unmovable but vet can't prove
	// that.  Going through RtlMoveMemory keeps the uintptr opaque to
	// Go and dodges the lint while also being shorter.
	rtlMoveMemory := kernel32.NewProc("RtlMoveMemory")
	rtlMoveMemory.Call(pMem, uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)*2))
	globalUnlock.Call(hMem)

	r1, _, _ := openClipboard.Call(0)
	if r1 == 0 {
		return
	}
	defer closeClipboard.Call()

	emptyClipboard.Call()
	r1, _, _ = setClipboardData.Call(CF_UNICODETEXT, hMem)
	if r1 != 0 {
		// Clipboard owns the handle now; don't free it.
		freed = true
	}
}

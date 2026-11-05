//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// setUTF8Console flips the current console's output code page to UTF-8
// (CP 65001) and turns on virtual terminal processing so PowerShell /
// cmd.exe windows render the agent's banner correctly instead of
// showing CP936 (GBK) garbage like "å®¡è®¡æ¥å¿".  The chcp 65001 in
// agent.ps1 only affects the console *before* agent.exe spawns; once
// our process owns the console, we need to set it ourselves.
//
// Best-effort: failures are silent (rare consoles like the legacy
// raster-only one in Windows 7 might reject 65001 but we don't care
// about that platform any more).
func setUTF8Console() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setConsoleOutputCP := kernel32.NewProc("SetConsoleOutputCP")
	setConsoleCP := kernel32.NewProc("SetConsoleCP")

	const CP_UTF8 = 65001
	_, _, _ = setConsoleOutputCP.Call(uintptr(CP_UTF8))
	_, _, _ = setConsoleCP.Call(uintptr(CP_UTF8))

	// Enable virtual terminal processing so ANSI escape codes (used
	// elsewhere in the agent for log coloring) survive.
	const STD_OUTPUT_HANDLE = ^uintptr(10) + 1 // -11 as DWORD
	const ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004
	getStdHandle := kernel32.NewProc("GetStdHandle")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	hStdout, _, _ := getStdHandle.Call(STD_OUTPUT_HANDLE)
	if hStdout == 0 {
		return
	}
	var mode uint32
	r, _, _ := getConsoleMode.Call(hStdout, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return
	}
	_, _, _ = setConsoleMode.Call(hStdout, uintptr(mode|ENABLE_VIRTUAL_TERMINAL_PROCESSING))
}

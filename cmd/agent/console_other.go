//go:build !windows

package main

// setUTF8Console is a Windows-specific no-op on other platforms.
// macOS / Linux terminals are UTF-8 by default.
func setUTF8Console() {}

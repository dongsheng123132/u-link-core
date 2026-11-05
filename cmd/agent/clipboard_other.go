//go:build !windows

package main

// copyToClipboard is a Windows-specific UX helper; on macOS/Linux we
// don't bother (operators on those platforms typically have working
// pbcopy/xclip workflows already, and the agent banner stays visible).
func copyToClipboard(string) {}

//go:build windows

// Package winhide keeps child processes from opening a console window.
//
// u-link.exe is built with -H windowsgui, so the parent owns no console.
// Windows therefore allocates a brand-new console for every console child
// (cmd.exe, powershell.exe, reg.exe, clip.exe), which shows up as a black
// window that pops and vanishes on the controlled machine — the "terminal
// keeps flashing" report from customers. CREATE_NO_WINDOW + SW_HIDE stops
// the allocation; output still flows through the pipes as before.
package winhide

import (
	"os/exec"
	"syscall"
)

// createNoWindow is Win32 CREATE_NO_WINDOW. The syscall package does not
// export it (golang.org/x/sys/windows does, but we avoid the dependency).
const createNoWindow = 0x08000000

// Apply marks cmd as windowless and returns it for call chaining. Safe to
// call on a cmd that already has SysProcAttr (e.g. CmdLine override):
// existing flags are preserved.
func Apply(cmd *exec.Cmd) *exec.Cmd {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	return cmd
}

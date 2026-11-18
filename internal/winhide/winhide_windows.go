//go:build windows

// Package winhide keeps child processes from opening a console window.
//
// The desktop app is built with -H windowsgui and typically has no console.
// Starting console children from it can open a visible terminal window for
// each remote command. CREATE_NO_WINDOW and HideWindow prevent this for the
// processes using this helper while retaining redirected standard streams.
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

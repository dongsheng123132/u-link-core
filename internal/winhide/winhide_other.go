//go:build !windows

// Package winhide is a no-op off Windows; console windows are a Win32
// concept. The stub keeps `go build ./...` working on macOS/Linux, where
// only the POSIX executors are compiled.
package winhide

import "os/exec"

// Apply returns cmd unchanged outside Windows.
func Apply(cmd *exec.Cmd) *exec.Cmd { return cmd }

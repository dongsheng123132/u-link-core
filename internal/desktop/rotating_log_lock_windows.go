//go:build windows

package desktop

import (
	"os"

	"golang.org/x/sys/windows"
)

func acquireLogLock(f *os.File) (func() error, error) {
	handle := windows.Handle(f.Fd())
	overlapped := &windows.Overlapped{}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(handle, flags, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() error { return windows.UnlockFileEx(handle, 0, 1, 0, overlapped) }, nil
}

//go:build !darwin && !linux && !windows

package desktop

import (
	"fmt"
	"os"
)

func acquireLogLock(_ *os.File) (func() error, error) {
	return nil, fmt.Errorf("log writer lock is unsupported on this platform")
}

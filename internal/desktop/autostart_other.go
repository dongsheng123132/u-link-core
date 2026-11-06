//go:build !darwin && !windows

package desktop

import "fmt"

func platformAutostartSupported() bool { return false }

func platformAutostartEnabled() bool { return false }

func setAutostart(enabled bool) error {
	return fmt.Errorf("当前平台暂不支持开机自启")
}

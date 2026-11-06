//go:build !darwin && !windows

package desktop

import "fmt"

func setClipboard(text string) error {
	return fmt.Errorf("当前平台不支持剪贴板")
}

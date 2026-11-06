//go:build !darwin

package desktop

import "fmt"

// InstallSelf is macOS-only; Windows uses install-link-windows.ps1.
func InstallSelf() error {
	return fmt.Errorf("--install-app 仅支持 macOS；Windows 请运行 scripts/install-link-windows.ps1")
}

func installSelfBundle() (string, error) {
	return "", fmt.Errorf("--install-app 仅支持 macOS")
}

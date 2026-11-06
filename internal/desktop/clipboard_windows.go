//go:build windows

package desktop

import (
	"os/exec"
	"strings"
)

func setClipboard(text string) error {
	cmd := exec.Command("cmd", "/c", "clip")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

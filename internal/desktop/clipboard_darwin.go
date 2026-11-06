//go:build darwin

package desktop

import (
	"os/exec"
	"strings"
)

func setClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

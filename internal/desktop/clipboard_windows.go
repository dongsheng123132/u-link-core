//go:build windows

package desktop

import (
	"os/exec"
	"strings"

	"github.com/dongsheng123132/remote-agent/internal/winhide"
)

func setClipboard(text string) error {
	cmd := winhide.Apply(exec.Command("cmd", "/c", "clip"))
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

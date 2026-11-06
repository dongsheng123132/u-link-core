//go:build windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	autostartRunKey    = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	autostartValueName = "ULink"
)

func platformAutostartSupported() bool { return true }

func platformAutostartEnabled() bool {
	out, err := exec.Command("reg", "query", autostartRunKey, "/v", autostartValueName).CombinedOutput()
	return err == nil && strings.Contains(string(out), autostartValueName)
}

// setAutostart uses HKCU\...\Run: no admin rights, no service install,
// and it survives reboots.  Task Scheduler would resist AV heuristics
// slightly better but needs a different error surface; Run is the MVP
// tradeoff (documented in docs/DESKTOP-APP.md §5).
func setAutostart(enabled bool) error {
	if !enabled {
		_ = exec.Command("reg", "delete", autostartRunKey, "/v", autostartValueName, "/f").Run()
		if platformAutostartEnabled() {
			return fmt.Errorf("清除启动项失败，请手动检查注册表 HKCU\\...\\Run")
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件失败: %w", err)
	}
	// The Run value must be a quoted command line so paths with spaces
	// (C:\Program Files\...) survive.
	quoted := `"` + exe + `"`
	out, err := exec.Command("reg", "add", autostartRunKey,
		"/v", autostartValueName, "/t", "REG_SZ", "/d", quoted, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("写入启动项失败: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

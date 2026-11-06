//go:build darwin

package desktop

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ensureMenuBarVisibleOnce works around a macOS 26 (Tahoe) quirk:
// a freshly created NSStatusItem is not laid out until ControlCenter
// re-runs its menu bar pass, so users install U-Link and "don't see it"
// even though the process is running (our own first-run log showed
// "托盘已就绪" while nothing appeared).
//
// We do this exactly once per install (marker file), only on macOS >= 26,
// and a few seconds after startup so the status item exists by then.
func ensureMenuBarVisibleOnce(configDir string) {
	if configDir == "" {
		return
	}
	marker := filepath.Join(configDir, ".menubar-refreshed-v2")
	if _, err := os.Stat(marker); err == nil {
		return
	}

	version := macOSMajorVersion()
	if version < 26 {
		_ = os.WriteFile(marker, []byte("not-needed\n"), 0o600)
		return
	}

	// Two passes: the first one registers the item, the second one picks
	// it up after ControlCenter has rebuilt its layout.  Killing too
	// early (4s) loses the item until the next ControlCenter restart.
	for attempt, wait := range []time.Duration{10 * time.Second, 20 * time.Second} {
		time.Sleep(wait)
		if err := exec.Command("killall", "ControlCenter").Run(); err != nil {
			log.Printf("macOS 26 菜单栏刷新第 %d 次失败（可手动执行 killall ControlCenter）: %v", attempt+1, err)
		} else {
			log.Printf("macOS 26：第 %d 次刷新 ControlCenter", attempt+1)
		}
	}
	_ = os.WriteFile(marker, []byte("refreshed\n"), 0o600)
	log.Printf("macOS 26：菜单栏刷新完成，应出现「绿点 + U-Link」")
}

func macOSMajorVersion() int {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return 0
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ".")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}

//go:build darwin

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "org.uclaw.u-link"

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func platformAutostartSupported() bool { return true }

func platformAutostartEnabled() bool {
	path, err := launchAgentPath()
	if err != nil {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	// A plist on disk is not proof it is loaded; ask launchd.
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)
	return exec.Command("launchctl", "print", target).Run() == nil
}

// setAutostart writes/removes the LaunchAgent for the running binary.
func setAutostart(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件失败: %w", err)
	}
	return setAutostartFor(exe, enabled)
}

// setAutostartFor is setAutostart for an explicit binary path, used by
// --install-app so the LaunchAgent points at /Applications/U-Link.app
// rather than the build directory it was installed from.
//
// KeepAlive is "restart on crash only" (SuccessfulExit=false) so the
// tray's quit action sticks instead of being resurrected by launchd.
func setAutostartFor(exe string, enabled bool) error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)

	if !enabled {
		// bootout first: if the job stays loaded after its plist is
		// gone, launchd prints "program not found" on every login.
		_ = exec.Command("launchctl", "bootout", target).Run()
		_ = exec.Command("launchctl", "unload", "-w", path).Run()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(launchAgentPlist(exe)), 0o644); err != nil {
		return err
	}

	guiDomain := fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("launchctl", "bootstrap", guiDomain, path).Run(); err != nil {
		// Older launchctl, or the job is already bootstrapped.
		if err2 := exec.Command("launchctl", "load", "-w", path).Run(); err2 != nil {
			return fmt.Errorf("launchctl bootstrap: %v；load: %v", err, err2)
		}
	}
	return nil
}

func launchAgentPlist(exe string) string {
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(exe)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>ProcessType</key>
    <string>Interactive</string>
    <key>LimitLoadToSessionType</key>
    <string>Aqua</string>
</dict>
</plist>
`, launchAgentLabel, escaped)
}

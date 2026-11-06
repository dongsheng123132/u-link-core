//go:build darwin

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const bundleIdentifier = "org.uclaw.u-link"
const applicationsBundle = "/Applications/U-Link.app"

// InstallSelf copies the running .app bundle into /Applications (falling
// back to ~/Applications if /Applications is not writable), points the
// LaunchAgent at the installed copy, stops the old instance and launches
// the installed one.
//
// It is meant to be called from a LaunchServices-started instance:
//
//	open -n build/U-Link.app --args --install-app
//
// A terminal-launched binary can also call it, provided the user has
// permission to write to /Applications (or ~/Applications as a fallback).
func InstallSelf() error {
	bundle, err := currentBundlePath()
	if err != nil {
		return err
	}
	fmt.Printf("当前 bundle: %s\n", bundle)

	dest, err := installSelfBundle()
	if err != nil {
		return err
	}
	installedExe := filepath.Join(dest, "Contents", "MacOS", "u-link")
	stopOtherInstances()
	time.Sleep(1200 * time.Millisecond)
	if err := exec.Command("open", "-n", dest).Run(); err != nil {
		return fmt.Errorf("启动安装后的 app 失败: %w", err)
	}
	fmt.Printf("已安装并启动: %s\n（开机自启已指向 %s）\n", dest, installedExe)

	// macOS 26 (Tahoe) bug: a freshly created NSStatusItem is not laid
	// out until ControlCenter re-runs its menu bar pass.  Restarting
	// ControlCenter makes the new item visible immediately; without it
	// users report "I installed U-Link but don't see it".
	time.Sleep(1500 * time.Millisecond)
	_ = exec.Command("killall", "ControlCenter").Run()
	fmt.Println("已刷新菜单栏（macOS 26 需要重启 ControlCenter 才会显示新状态项）")
	fmt.Println("菜单栏应出现「U-Link」文字 + 绿点；点击即可打开控制台。")
	return nil
}

// installSelfBundle is the side-effect-free part shared by the CLI flag
// and the local API: copy the running bundle and point autostart at it.
func installSelfBundle() (string, error) {
	bundle, err := currentBundlePath()
	if err != nil {
		return "", err
	}
	dest := applicationsBundle
	if samePath(bundle, dest) {
		fmt.Printf("已在 %s，无需复制\n", dest)
	} else if err := copyBundle(bundle, dest); err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("/Applications 安装失败: %v；且无法确定主目录", err)
		}
		alt := filepath.Join(home, "Applications", "U-Link.app")
		fmt.Printf("/Applications 不可写（%v），改到 %s\n", err, alt)
		if err2 := os.MkdirAll(filepath.Dir(alt), 0o755); err2 != nil {
			return "", err2
		}
		if err2 := copyBundle(bundle, alt); err2 != nil {
			return "", fmt.Errorf("/Applications: %v；%s: %v", err, alt, err2)
		}
		dest = alt
	}

	installedExe := filepath.Join(dest, "Contents", "MacOS", "u-link")
	if err := setAutostartFor(installedExe, true); err != nil {
		return dest, fmt.Errorf("安装成功但写开机自启失败: %w", err)
	}
	return dest, nil
}

// currentBundlePath returns the .app bundle this executable lives in.
func currentBundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	const marker = ".app/Contents/MacOS/"
	idx := strings.Index(exe, marker)
	if idx < 0 {
		return "", fmt.Errorf("当前可执行文件不在 .app bundle 内: %s（请用 .app 启动后再安装）", exe)
	}
	return exe[:idx+len(".app")], nil
}

func samePath(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea != nil || eb != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// copyBundle replaces dst with a copy of src.  It refuses to delete a
// destination that is not our own bundle, so a typo can never wipe an
// unrelated app.
func copyBundle(src, dst string) error {
	if info, err := os.Stat(dst); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("目标已存在且不是目录: %s", dst)
		}
		if id := bundleIDOf(dst); id != bundleIdentifier {
			return fmt.Errorf("目标已存在且不是 U-Link（CFBundleIdentifier=%q）: %s", id, dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("移除旧版本失败: %w", err)
		}
	}
	out, err := exec.Command("ditto", src, dst).CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dst)
		return fmt.Errorf("ditto 复制失败: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	// Keep the ad-hoc signature valid after copying.
	_ = exec.Command("codesign", "--force", "--sign", "-", dst).Run()
	if err := exec.Command("codesign", "--verify", "--strict", dst).Run(); err != nil {
		fmt.Printf("警告：安装后签名校验未通过（可能仍能运行）: %v\n", err)
	}
	return nil
}

func bundleIDOf(bundle string) string {
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	out, err := exec.Command("plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plist).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// stopOtherInstances terminates every other U-Link process (the old
// build-directory instance) but never this installer process itself.
func stopOtherInstances() {
	self := os.Getpid()
	out, err := exec.Command("pgrep", "-f", "U-Link.app/Contents/MacOS/u-link").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, perr := strconv.Atoi(strings.TrimSpace(line))
		if perr != nil || pid <= 1 || pid == self {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

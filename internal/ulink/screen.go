package ulink

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ScreenResult describes a captured remote frame.
type ScreenResult struct {
	Device     string `json:"device"`
	OS         string `json:"os"`
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path"`
	Bytes      int64  `json:"bytes"`
}

// DeviceOS looks up a device's operating system through the relay.
func DeviceOS(cfg *Config, device string) (string, error) {
	devices, err := ListDevices(cfg)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if d.ID == device {
			return d.OS, nil
		}
	}
	return "", &Err{Code: "device_not_found", Message: "device not found: " + device, Hint: "run `ulink devices`"}
}

// CaptureScreen grabs one frame from a device and downloads it.
//
// It reuses the existing exec + file-pull channels: the agent runs a
// platform-native capture command, prints the file path, and the controller
// pulls the file.  No new wire protocol is needed, and the same recipe works
// on macOS, Windows and Linux.
func CaptureScreen(cfg *Config, device, localPath string) (*ScreenResult, error) {
	osName, err := DeviceOS(cfg, device)
	if err != nil {
		return nil, err
	}
	stamp := time.Now().UnixNano()
	var command, shell, remoteHint string

	switch osName {
	case "darwin":
		remoteHint = fmt.Sprintf("ulink-screen-%d.png", stamp)
		command = fmt.Sprintf(`p="$HOME/%s"; screencapture -x -t png "$p" 2>&1; echo "$p"`, remoteHint)
	case "windows":
		remoteHint = fmt.Sprintf("ulink-screen-%d.png", stamp)
		ps := fmt.Sprintf(`$p = Join-Path $env:TEMP '%s'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$b = [System.Windows.Forms.SystemInformation]::VirtualScreen
$bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($b.Location, [System.Drawing.Point]::Empty, $b.Size)
$bmp.Save($p, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose()
Write-Output $p`, remoteHint)
		command = ps
		shell = "powershell"
	default: // linux
		remoteHint = fmt.Sprintf("ulink-screen-%d.png", stamp)
		command = fmt.Sprintf(`p="$HOME/%s"; import -window root "$p" 2>/dev/null || gnome-screenshot -f "$p" 2>/dev/null || scrot "$p" 2>/dev/null; echo "$p"`, remoteHint)
	}

	res, err := Run(cfg, NewSessionID(), device, command, shell)
	if err != nil {
		return nil, err
	}
	remotePath := lastLine(res.Stdout)
	if remotePath == "" && strings.TrimSpace(res.Stderr) != "" {
		return nil, &Err{
			Code:    "capture_failed",
			Message: "remote capture failed",
			Hint:    strings.TrimSpace(res.Stderr),
		}
	}
	if remotePath == "" {
		return nil, &Err{
			Code:    "capture_failed",
			Message: "remote capture produced no file path",
			Hint:    "stdout was: " + strings.TrimSpace(res.Stdout),
		}
	}
	remotePath = strings.Trim(remotePath, `"' `)

	if localPath == "" {
		localPath = fmt.Sprintf("ulink-screen-%s-%d.png", device, time.Now().Unix())
	}
	if dir := filepath.Dir(localPath); dir != "" && dir != "." {
		_ = mkdirAll(dir)
	}

	n, err := pullFile(cfg, device, remotePath, localPath)
	if err != nil {
		return nil, ClassifyError(err)
	}
	// Best-effort cleanup of the temporary frame on the remote side.
	cleanupScreen(cfg, device, remotePath, osName)

	return &ScreenResult{
		Device:     device,
		OS:         osName,
		RemotePath: remotePath,
		LocalPath:  localPath,
		Bytes:      n,
	}, nil
}

// TypeText pastes text into the focused window of a remote device.
//
// The text is delivered through the clipboard (base64-encoded, so no quoting
// hazards) and pasted with the platform's standard paste shortcut.  macOS
// needs the agent to hold the Accessibility permission; Windows needs the
// interactive session; Linux needs xclip/xdotool.
func TypeText(cfg *Config, device, text string) error {
	osName, err := DeviceOS(cfg, device)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return &Err{Code: "usage", Message: "text is required", Hint: "usage: ulink type <device> --text \"...\""}
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(text))

	var command, shell string
	switch osName {
	case "darwin":
		command = fmt.Sprintf("printf %%s %s | base64 -D | pbcopy; osascript -e 'tell application \"System Events\" to keystroke \"v\" using command down'", b64)
	case "windows":
		shell = "powershell"
		command = fmt.Sprintf(`$t = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
Set-Clipboard -Value $t
Start-Sleep -Milliseconds 250
(New-Object -ComObject WScript.Shell).SendKeys('^v')`, b64)
	default:
		command = fmt.Sprintf("(printf %%s %s | base64 -d | xclip -selection clipboard 2>/dev/null || printf %%s %s | base64 -d | xsel --clipboard 2>/dev/null); xdotool key ctrl+v 2>/dev/null || xdotool type %s",
			b64, b64, b64)
	}
	_, err = Run(cfg, NewSessionID(), device, command, shell)
	return err
}

func cleanupScreen(cfg *Config, device, remotePath, osName string) {
	var cmd string
	switch osName {
	case "windows":
		cmd = fmt.Sprintf("Remove-Item -LiteralPath '%s' -ErrorAction SilentlyContinue", remotePath)
	default:
		cmd = fmt.Sprintf("rm -f %s", remotePath)
	}
	shell := ""
	if osName == "windows" {
		shell = "powershell"
	}
	_, _ = Run(cfg, NewSessionID(), device, cmd, shell)
}

func lastLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if v := strings.TrimSpace(lines[i]); v != "" {
			return v
		}
	}
	return ""
}

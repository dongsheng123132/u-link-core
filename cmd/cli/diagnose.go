package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/dongsheng123132/remote-agent/internal/cli"
	"github.com/spf13/cobra"
)

// diagnoseCmd runs a curated battery of probes against a remote
// device and prints a markdown report.  Born out of the May 2026
// Win7 customer case where the operator (and Claude) burned ~10
// minutes typing one-off `exec` commands to learn:
//   - OS version + Service Pack
//   - CPU model + RAM
//   - Disk layout (incl. whether the U-Claw USB stick was inserted)
//   - Whether Node.js was already installed somewhere
//   - Whether OpenClaw / Hermes / ClawX processes were running
//   - Whether port 18789 was already bound
//
// Now it's one command: `remote-agent diagnose <id>`.  Output is
// markdown so it pastes cleanly into WeChat / Lark.
//
// Why one combined command line vs many: each `exec` round-trips
// through the relay (~80-300 ms) and Win7's bitsadmin/cmd are slow
// to spawn.  Joining probes into a single cmd.exe with `&` chains
// shaves the diagnose run from ~10s to ~2s on typical home broadband.
func diagnoseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnose <device-id>",
		Short: "One-shot health check on a remote device (markdown report)",
		Long: `Collect OS / hardware / disk / process / port info from a
remote device and print a markdown report.

Useful as the first thing to run when a customer reports a problem —
saves 10+ rounds of one-off exec commands gathering the same context.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			deviceID := args[0]
			c := newClient()
			defer c.Close()

			fmt.Printf("# Diagnostic Report — %s\n\n", deviceID)

			// First decide platform via list (cheap; doesn't leave the
			// relay) so we can pick the right command set.
			devices, err := c.ListDevices()
			if err != nil {
				fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
				os.Exit(1)
			}
			os_ := ""
			hostname := ""
			version := ""
			for _, d := range devices {
				if d.ID == deviceID {
					os_ = d.OS
					hostname = d.Hostname
					version = d.Version
					break
				}
			}
			if os_ == "" {
				fmt.Fprintf(os.Stderr, "device %s not online\n", deviceID)
				os.Exit(1)
			}

			fmt.Printf("- Device ID: `%s`\n", deviceID)
			fmt.Printf("- Hostname: `%s`\n", hostname)
			fmt.Printf("- Agent OS / version: `%s` / `%s`\n\n", os_, valueOr(version, "-"))

			switch os_ {
			case "windows":
				diagnoseWindows(c, deviceID)
			case "linux", "darwin":
				diagnoseUnix(c, deviceID)
			default:
				fmt.Printf("Unknown OS `%s`; running unix-style probes as a guess.\n", os_)
				diagnoseUnix(c, deviceID)
			}
		},
	}
}

// section runs a single labeled probe and prints it as a markdown
// section.  Errors are reported inline, never fatal — diagnose's job
// is to keep going and capture as much as possible.
func section(c *cli.Client, deviceID, title, command string) {
	fmt.Printf("## %s\n\n", title)
	fmt.Printf("```\n")
	res, err := c.Exec(deviceID, command)
	if err != nil {
		fmt.Printf("(error: %v)\n", err)
	} else {
		out := strings.TrimRight(res.Stdout+res.Stderr, "\r\n ")
		if out == "" {
			out = "(no output)"
		}
		fmt.Println(out)
	}
	fmt.Printf("```\n\n")
}

// diagnoseWindows emits a Windows-specific battery.  Each command
// is a single cmd.exe line so it round-trips just once.  We avoid
// PowerShell because:
//   - Win7 ships PS 2.0 which lacks half the cmdlets we'd want.
//   - Some 360 / 腾讯电脑管家 builds intercept powershell.exe but not
//     cmd.exe, so cmd is the lower-friction default.
func diagnoseWindows(c *cli.Client, deviceID string) {
	section(c, deviceID, "OS / Service Pack",
		`ver & echo --- & systeminfo | findstr /B /C:"OS Name" /C:"OS Version" /C:"System Type"`)

	section(c, deviceID, "CPU / RAM",
		`wmic cpu get name & echo --- & systeminfo | findstr /C:"Total Physical Memory"`)

	section(c, deviceID, "Disk Layout (incl. removable / U-Claw USB)",
		`wmic logicaldisk get caption,volumename,filesystem,freespace,size`)

	section(c, deviceID, "Existing Node.js installs",
		`where node 2>nul & if exist "%LOCALAPPDATA%\nvm\nvm.exe" %LOCALAPPDATA%\nvm\nvm.exe list 2>nul & if exist "C:\Program Files\nodejs\node.exe" "C:\Program Files\nodejs\node.exe" -v 2>nul`)

	section(c, deviceID, "OpenClaw / Hermes / ClawX processes",
		`tasklist /FI "IMAGENAME eq node.exe" /FO TABLE & echo --- & tasklist /FI "WINDOWTITLE eq *U-Claw*" 2>nul & echo --- & tasklist | findstr /I "U-Claw Hermes openclaw electron"`)

	section(c, deviceID, "Port 18789 / 18791 already bound?",
		`netstat -an | findstr "18789 18791 18788"`)

	section(c, deviceID, "Defender / 360 / 腾讯电脑管家 status",
		`tasklist | findstr /I "MsMpEng 360 360safe 360tray QQPCRTP QQPCTray Tencent"`)

	section(c, deviceID, "User profile dirs (where %APPDATA% etc resolve to)",
		`echo USERPROFILE=%USERPROFILE% & echo APPDATA=%APPDATA% & echo LOCALAPPDATA=%LOCALAPPDATA% & echo TEMP=%TEMP%`)
}

// diagnoseUnix is the macOS / Linux equivalent.  Less battle-tested
// than the Windows path because most U-Claw customers are on Windows
// today, but useful for the operator's own dogfood machines.
func diagnoseUnix(c *cli.Client, deviceID string) {
	section(c, deviceID, "OS / Kernel",
		`uname -a; echo ---; (sw_vers 2>/dev/null || cat /etc/os-release 2>/dev/null)`)

	section(c, deviceID, "CPU / RAM",
		`echo CPU:; (sysctl -n machdep.cpu.brand_string 2>/dev/null || grep 'model name' /proc/cpuinfo | head -1); echo RAM:; (sysctl hw.memsize 2>/dev/null || grep MemTotal /proc/meminfo)`)

	section(c, deviceID, "Disk usage",
		`df -h | head -10`)

	section(c, deviceID, "Existing Node.js installs",
		`which node; node -v 2>/dev/null; ls ~/.nvm/versions/node/ 2>/dev/null`)

	section(c, deviceID, "OpenClaw / Hermes / ClawX processes",
		`ps aux | grep -E '(openclaw|U-Claw|Hermes|electron)' | grep -v grep | head -20`)

	section(c, deviceID, "Port 18789 / 18791 already bound?",
		`(lsof -i :18789 2>/dev/null; lsof -i :18791 2>/dev/null; lsof -i :18788 2>/dev/null) | head -10`)
}

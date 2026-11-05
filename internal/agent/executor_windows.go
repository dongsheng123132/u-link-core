//go:build windows

package agent

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
)

// buildCmdShell returns an *exec.Cmd that runs `command` under the
// requested shell.  Default is cmd.exe to preserve v1 behaviour: a lot
// of existing callers pass cmd-shell syntax (`echo %VAR%`, `>` redirect,
// `&` chaining) and switching the default would silently break them.
//
// `--shell powershell` is opt-in for the case where cmd.exe is being
// intercepted by 360 / 腾讯电脑管家 / similar consumer security tools
// — those tools watch cmd.exe spawns specifically, and a PowerShell
// runner sidesteps them.  PowerShell starts ~300 ms slower but is the
// only reliable option on locked-down home machines.
//
// Supported shells:
//
//	"" / "cmd" (default) — `cmd /S /C "<command>"` via SysProcAttr.CmdLine
//	    to bypass Go's EscapeArg.  Original v1 path.
//	"powershell" — `powershell.exe -NoProfile -NonInteractive -Command -`
//	    with the script piped in via stdin.  Avoids every quoting hazard
//	    cmd.exe imposes.
func buildCmdShell(ctx context.Context, command, shell string) *exec.Cmd {
	switch strings.ToLower(shell) {
	case "powershell", "pwsh", "ps":
		return buildCmdPowerShell(ctx, command)
	case "", "cmd":
		return buildCmdLegacy(ctx, command)
	default:
		// Unknown explicit shell — assume the caller knows what they're
		// doing and treat it as a literal executable path that takes
		// the command as a single argv arg.  Last-resort escape hatch.
		return exec.CommandContext(ctx, shell, "-c", command)
	}
}

// buildCmd preserves the v1 entry point so executor.go (and any future
// non-shell-aware caller) still compiles.  Routes to the platform
// default runner.
func buildCmd(ctx context.Context, command string) *exec.Cmd {
	return buildCmdShell(ctx, command, "")
}

// buildCmdPowerShell pipes the command into powershell.exe via stdin,
// which avoids every quoting hazard cmd.exe imposes on Go's
// CreateProcess wrapper.  -NoProfile skips $PROFILE startup (slow on
// some machines, surprising side effects), -NonInteractive prevents
// prompts that would hang in a headless agent.
//
// Known caveat: on Chinese-locale Windows, PowerShell defaults to
// CP936 (GBK) for stdout and we get mojibake when the controller
// renders the result.  We tried prepending an `$OutputEncoding =
// [Text.Encoding]::UTF8` preamble but it didn't fully solve the
// problem (some commands honor it, some don't, and the buffered exec
// path piped through Go's stdoutPipe still bytewise mismatched).  For
// now we leave the encoding alone — operator can prefix their own
// `chcp 65001 | Out-Null` when chinese output matters.
func buildCmdPowerShell(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-Command", "-")
	cmd.Stdin = strings.NewReader(command)
	return cmd
}

// buildCmdLegacy is the v1 cmd-based runner, kept available for
// `--shell cmd` callers.  See package comment for why we no longer
// default to it.
func buildCmdLegacy(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd /S /C "` + command + `"`,
	}
	return cmd
}

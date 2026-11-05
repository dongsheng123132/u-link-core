//go:build !windows

package agent

import (
	"context"
	"os/exec"
)

// buildCmdShell builds an sh/bash invocation for unix-like systems.
// shell selects the runner: "" / "sh" → /bin/sh -c, "bash" → /bin/bash -c.
// Anything else is treated as a literal interpreter path taking -c.
func buildCmdShell(ctx context.Context, command, shell string) *exec.Cmd {
	switch shell {
	case "", "sh":
		return exec.CommandContext(ctx, "sh", "-c", command)
	case "bash":
		return exec.CommandContext(ctx, "bash", "-c", command)
	default:
		return exec.CommandContext(ctx, shell, "-c", command)
	}
}

// buildCmd is the legacy v1 entry point.  Routes to the platform default.
func buildCmd(ctx context.Context, command string) *exec.Cmd {
	return buildCmdShell(ctx, command, "")
}

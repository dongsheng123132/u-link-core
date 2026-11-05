package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestExecuteEcho — sanity check that simple commands work on every platform.
func TestExecuteEcho(t *testing.T) {
	r := Execute("echo hello")
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "hello") {
		t.Fatalf("stdout=%q want contain 'hello'", r.Stdout)
	}
}

// TestExecuteWithDoubleQuotes — the regression: when a command contains "
// the old executor wrapped the whole thing in another pair of quotes,
// turning `cmd /C dir "path"` into `cmd /C ""cmd /C dir "path"""`,
// which made cmd treat the entire line as one filename.
func TestExecuteWithDoubleQuotes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific quoting regression")
	}
	// Pass a path that needs quoting.
	r := Execute(`dir /B "C:\Windows\System32\drivers"`)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	if r.Stdout == "" {
		t.Fatalf("expected listing of drivers dir, got empty")
	}
}

// TestExecutePowershellWithQuotes — covers the case where the command
// is `powershell -c "<script>"`, which is what most callers actually send.
func TestExecutePowershellWithQuotes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := Execute(`powershell -NoProfile -Command "Write-Output 42"`)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "42") {
		t.Fatalf("stdout=%q want contain '42'", r.Stdout)
	}
}

// TestExecuteShellOperators — verify cmd shell metacharacters survive
// (& chains commands, | pipes). These were never broken but guard
// against regressions in the rewrite.
func TestExecuteShellOperators(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := Execute("echo a & echo b")
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "a") || !strings.Contains(r.Stdout, "b") {
		t.Fatalf("stdout=%q want both 'a' and 'b'", r.Stdout)
	}
}

// TestExecuteEnvVar — `%APPDATA%` style env vars must still expand.
// cmd.exe expands them itself; we just need to not break that.
func TestExecuteEnvVar(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := Execute(`echo %SYSTEMROOT%`)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(strings.ToLower(r.Stdout), "windows") {
		t.Fatalf("stdout=%q want SYSTEMROOT expanded", r.Stdout)
	}
}

// TestExecuteNonZeroExit — make sure we propagate exit codes correctly.
func TestExecuteNonZeroExit(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "exit /B 7"
	} else {
		cmd = "exit 7"
	}
	r := Execute(cmd)
	if r.ExitCode != 7 {
		t.Fatalf("got exit=%d want 7", r.ExitCode)
	}
}

// TestExecutePathWithSpacesAndQuotes — mirrors a real call we make:
// pass an explicit path with spaces wrapped in quotes.
func TestExecutePathWithSpacesAndQuotes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := Execute(`type "C:\Windows\System32\drivers\etc\hosts"`)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	// hosts file always contains "localhost" line (commented or not).
	if !strings.Contains(strings.ToLower(r.Stdout), "localhost") {
		t.Fatalf("stdout=%q want contain 'localhost'", r.Stdout)
	}
}

// TestExecuteNestedQuotes — powershell -c with quoted arg containing
// quoted strings. Common shape for our remote diagnostics.
func TestExecuteNestedQuotes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := Execute(`powershell -NoProfile -Command "& { Write-Output 'hello world' }"`)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "hello world") {
		t.Fatalf("stdout=%q want 'hello world'", r.Stdout)
	}
}

// TestExecuteRedirect — `>` redirect must still work (shell metachar).
func TestExecuteRedirect(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := Execute(`echo redirect-test > %TEMP%\remote-agent-redirect.txt && type %TEMP%\remote-agent-redirect.txt && del %TEMP%\remote-agent-redirect.txt`)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "redirect-test") {
		t.Fatalf("stdout=%q want 'redirect-test'", r.Stdout)
	}
}

// TestExecutePowerShellRunner — opt-in powershell runner used when
// cmd.exe is being intercepted by consumer security software.  Verifies
// the stdin-piped command path doesn't trip on quotes or env vars.
func TestExecutePowerShellRunner(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific")
	}
	r := ExecuteWithShell(`Write-Output "hello $env:SystemRoot"`, "powershell")
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	if !strings.Contains(strings.ToLower(r.Stdout), "windows") {
		t.Fatalf("stdout=%q want $env:SystemRoot expanded", r.Stdout)
	}
}

// TestGenerateStableDeviceID — same hostname must always produce the
// same ID across calls; different hostnames must (almost certainly)
// produce different IDs.  Empty hostname falls back to UUID-prefixed.
func TestGenerateStableDeviceID(t *testing.T) {
	a1 := generateStableDeviceID("Desktop-Andy")
	a2 := generateStableDeviceID("Desktop-Andy")
	b := generateStableDeviceID("CLAW-WORKSTATION")
	empty := generateStableDeviceID("")

	if a1 != a2 {
		t.Fatalf("same hostname should produce same id: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Fatalf("different hostnames produced same id: %q", a1)
	}
	if !strings.HasPrefix(a1, "pc-") || len(a1) != 7 {
		t.Fatalf("expected pc-XXXX format, got %q", a1)
	}
	if !strings.HasPrefix(empty, "pc-") {
		t.Fatalf("empty hostname fallback should still start with pc-, got %q", empty)
	}
}

// TestRunStream — streaming runner emits chunks as they arrive and
// reports the exit code through the result channel.
func TestRunStream(t *testing.T) {
	var script string
	var shell string
	if runtime.GOOS == "windows" {
		script = `1..3 | ForEach-Object { Write-Output "line$_"; Start-Sleep -Milliseconds 50 }`
		shell = "powershell"
	} else {
		script = `for i in 1 2 3; do echo "line$i"; sleep 0.05; done`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, chunks, result, err := RunStream(ctx, script, shell)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var got []byte
	for c := range chunks {
		got = append(got, c.Data...)
	}
	exit := <-result

	if exit != 0 {
		t.Fatalf("exit=%d output=%q", exit, got)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(string(got), "line") {
			t.Fatalf("output=%q missing line%d", got, i)
		}
	}
}

package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

const defaultTimeout = 5 * time.Minute

// ExecResult holds the result of a buffered (non-streaming) command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ChunkKind labels which pipe a streaming chunk came from.
type ChunkKind int

const (
	ChunkStdout ChunkKind = iota
	ChunkStderr
)

// Chunk is one fragment of streaming output.
type Chunk struct {
	Kind ChunkKind
	Data []byte
}

// ErrCancelled is returned by RunStream when the supplied context is
// cancelled before the command exits.  Callers should map it onto
// protocol.ExitCodeCancelled when serializing back to the controller.
var ErrCancelled = errors.New("exec cancelled")

// Execute runs a shell command and returns the buffered result.
// Preserved for the legacy non-streaming TypeExec path.
func Execute(command string) ExecResult {
	return ExecuteWithShell(command, "")
}

// ExecuteWithShell is like Execute but allows the caller to pick the
// runner (powershell, cmd, sh).  Empty shell = platform default.
func ExecuteWithShell(command, shell string) ExecResult {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cmd := buildCmdShell(ctx, command, shell)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := classifyExitErr(err, ctx, &stderr)

	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

// RunStream starts a command and emits Chunks on the returned channel
// as data arrives on stdout/stderr.  When the channel closes, the exit
// code is delivered via the result channel.  ctx cancellation kills the
// process; the returned PID lets the caller cross-reference an external
// kill request.
//
// The shell parameter selects the runner; empty falls back to the
// platform default (powershell on Windows, sh on unix).
func RunStream(ctx context.Context, command, shell string) (
	pid int,
	chunks <-chan Chunk,
	result <-chan int,
	err error,
) {
	cmd := buildCmdShell(ctx, command, shell)

	stdoutPipe, perr := cmd.StdoutPipe()
	if perr != nil {
		return 0, nil, nil, perr
	}
	stderrPipe, perr := cmd.StderrPipe()
	if perr != nil {
		return 0, nil, nil, perr
	}

	if err := cmd.Start(); err != nil {
		return 0, nil, nil, err
	}

	chunkCh := make(chan Chunk, 32)
	resultCh := make(chan int, 1)

	var wg sync.WaitGroup
	wg.Add(2)
	go pumpPipe(stdoutPipe, ChunkStdout, chunkCh, &wg)
	go pumpPipe(stderrPipe, ChunkStderr, chunkCh, &wg)

	go func() {
		wg.Wait()
		close(chunkCh)

		err := cmd.Wait()
		exitCode := classifyExitErr(err, ctx, nil)
		resultCh <- exitCode
		close(resultCh)
	}()

	return cmd.Process.Pid, chunkCh, resultCh, nil
}

// pumpPipe forwards a single pipe into the chunk channel until EOF.
func pumpPipe(r io.ReadCloser, kind ChunkKind, out chan<- Chunk, wg *sync.WaitGroup) {
	defer wg.Done()
	defer r.Close()
	buf := make([]byte, 16*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			out <- Chunk{Kind: kind, Data: cp}
		}
		if err != nil {
			return
		}
	}
}

// classifyExitErr converts a *exec.ExitError, a context deadline, or
// a nil err into a single int exit code, optionally appending a timeout
// message to a stderr buffer (only used by buffered Execute).
func classifyExitErr(err error, ctx context.Context, stderrBuf *bytes.Buffer) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		if stderrBuf != nil {
			stderrBuf.WriteString("command timed out after " + defaultTimeout.String())
		}
		return -1
	}
	if stderrBuf != nil {
		stderrBuf.WriteString(err.Error())
	}
	return -1
}

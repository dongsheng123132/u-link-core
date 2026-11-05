package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	"github.com/dongsheng123132/remote-agent/internal/cli"
	"github.com/dongsheng123132/remote-agent/internal/version"
	"github.com/spf13/cobra"
)

var (
	serverAddr string
	token      string
	tlsPin     string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "remote-agent",
		Short: "Remote headless ops CLI tool",
	}

	defaultServer := os.Getenv("REMOTE_AGENT_SERVER")
	if defaultServer == "" {
		defaultServer = "ws://localhost:8900"
	}
	rootCmd.PersistentFlags().StringVar(&serverAddr, "server", defaultServer, "relay server address (env: REMOTE_AGENT_SERVER)")
	rootCmd.PersistentFlags().StringVar(&token, "token", "", "controller auth token (env: REMOTE_AGENT_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&tlsPin, "tls-pin", os.Getenv("ULINK_TLS_PIN"), "relay TLS cert SHA-256 fingerprint")

	rootCmd.AddCommand(listCmd(), execCmd(), shellCmd(), pushCmd(), pullCmd(), versionCmd(), upgradeCmd(), watchCmd(), diagnoseCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newClient() *cli.Client {
	// Prefer env var to avoid token in shell history / process list
	if token == "" {
		token = os.Getenv("REMOTE_AGENT_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: token required. Set REMOTE_AGENT_TOKEN env or use --token flag")
		os.Exit(1)
	}
	wsURL := fmt.Sprintf("%s/ws/controller", serverAddr)
	c := cli.NewClient(wsURL, token)
	if tlsPin != "" {
		c.SetTLSPin(tlsPin)
	}
	if err := c.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return c
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI build version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.Version)
		},
	}
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List online devices",
		Run: func(cmd *cobra.Command, args []string) {
			c := newClient()
			defer c.Close()

			devices, err := c.ListDevices()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			if len(devices) == 0 {
				fmt.Println("No devices online.")
				return
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tHOSTNAME\tOS\tVERSION\tSTATUS")
			for _, d := range devices {
				status := "offline"
				if d.Online {
					status = "online"
				}
				ver := d.Version
				if ver == "" {
					ver = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Hostname, d.OS, ver, status)
			}
			w.Flush()
		},
	}
}

func execCmd() *cobra.Command {
	var (
		shell  string
		stream bool
	)
	cmd := &cobra.Command{
		Use:   "exec <device-id> <command>",
		Short: "Execute command on remote device",
		Long: `Execute a command on a remote device.

By default the command runs in the device's default shell — cmd.exe on
Windows (preserving v1 behaviour for callers passing cmd syntax like
echo %VAR%, & chaining, > redirect), sh on Linux/macOS.

Use --shell powershell on Windows when cmd.exe is being intercepted by
360 / 腾讯电脑管家 / similar consumer security tools.  Symptoms:
"exit code 127" with no command output.  PowerShell starts ~300 ms
slower but is the only path that consistently works on locked-down
home machines.

Use --stream to receive output incrementally as it arrives, rather
than waiting for the command to finish.  Useful for long-running
commands and AI-driven debug loops that want to react to partial
output.  Press Ctrl+C to send a TypeExecCancel and kill the remote
process.`,
		Args: cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			deviceID := args[0]
			command := strings.Join(args[1:], " ")

			c := newClient()
			defer c.Close()

			opts := cli.ExecOptions{Shell: shell}

			if !stream {
				result, err := c.ExecOpts(deviceID, command, opts)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				if result.Stdout != "" {
					fmt.Print(result.Stdout)
				}
				if result.Stderr != "" {
					fmt.Fprint(os.Stderr, result.Stderr)
				}
				os.Exit(result.ExitCode)
			}

			_, events, cancel, err := c.ExecStream(deviceID, command, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			// Forward Ctrl+C to the remote process via TypeExecCancel.
			// The first SIGINT triggers a clean cancel; a second within
			// 2s falls through to a hard exit so a hung agent doesn't
			// trap the operator on their own terminal.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt)
			go func() {
				<-sig
				fmt.Fprintln(os.Stderr, "\n  cancelling remote exec... (press Ctrl+C again to force-quit local CLI)")
				_ = cancel()
				<-sig
				fmt.Fprintln(os.Stderr, "  force-quit")
				os.Exit(130)
			}()
			defer signal.Stop(sig)

			exit := 0
			for ev := range events {
				if ev.Stdout != "" {
					fmt.Print(ev.Stdout)
				}
				if ev.Stderr != "" {
					fmt.Fprint(os.Stderr, ev.Stderr)
				}
				if ev.Err != nil {
					fmt.Fprintf(os.Stderr, "stream error: %v\n", ev.Err)
					os.Exit(1)
				}
				if ev.Final {
					exit = ev.ExitCode
				}
			}
			os.Exit(exit)
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "", "shell to run the command in (cmd|powershell on Windows; sh|bash on unix). Empty = platform default.")
	cmd.Flags().BoolVar(&stream, "stream", false, "stream output incrementally instead of waiting for the command to finish")
	return cmd
}

func pullCmd() *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "pull <device-id> <remote-path> <local-path>",
		Short: "Download a file from a remote device (max 1 GiB)",
		Long: `Download a file from a remote device, streaming chunks back over
the existing relay connection.  The remote agent reads the file in 1 MiB
chunks, so total transfer time scales linearly with file size; no need
to chain "exec cat > /tmp/x" + push the way v1 required.

Progress is printed to stderr by default (one line per chunk, overwriting
itself) so stdout stays clean for piping the file size on completion.
Pass --quiet to suppress progress entirely.`,
		Args: cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			deviceID := args[0]
			remotePath := args[1]
			localPath := args[2]

			c := newClient()
			defer c.Close()

			var progress cli.PullProgress
			if !quiet && isTerminal(os.Stderr) {
				progress = func(written, total int64) {
					if total > 0 {
						pct := float64(written) / float64(total) * 100
						fmt.Fprintf(os.Stderr, "\r  pulling: %s / %s (%.0f%%)   ",
							humanBytes(written), humanBytes(total), pct)
					} else {
						fmt.Fprintf(os.Stderr, "\r  pulling: %s   ", humanBytes(written))
					}
				}
			}

			n, err := c.PullFileWithProgress(deviceID, remotePath, localPath, progress)
			if progress != nil {
				fmt.Fprint(os.Stderr, "\r")
				fmt.Fprint(os.Stderr, strings.Repeat(" ", 60))
				fmt.Fprint(os.Stderr, "\r")
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Pulled %s to %s\n", humanBytes(n), localPath)
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress per-chunk progress output")
	return cmd
}

// humanBytes formats a byte count as a short human-readable string.
// 1024-based units to match Windows Explorer / ls -lh.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// isTerminal reports whether the given file descriptor is connected to
// a TTY (and thus capable of \r-based line rewriting).  Pipes and
// redirects fall through to plain non-progress output, which keeps
// CI / log-capture scenarios readable.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push <device-id> <local-file> <remote-path>",
		Short: "Push a file to remote device (max 32 MiB)",
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			deviceID := args[0]
			localFile := args[1]
			remotePath := args[2]

			c := newClient()
			defer c.Close()

			result, err := c.PushFile(deviceID, localFile, remotePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			if result.Success {
				fmt.Println(result.Stdout)
			} else {
				fmt.Fprintf(os.Stderr, "Push failed: %s\n", result.Error)
				os.Exit(1)
			}
		},
	}
}

func upgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade <device-id> <local-agent-binary>",
		Short: "[EXPERIMENTAL] Push a new agent binary and atomically restart it (Windows only, untested end-to-end)",
		Long: `EXPERIMENTAL — verify on a throwaway Windows VM before pointing at a customer.

Pushes the given local agent binary to the device, then runs a
replacement script that:
  - finds the currently running agent process and gets its image path
  - kills it (taskkill /F /IM agent.exe)
  - moves the new binary into place (overwriting the old one)
  - relaunches it detached

Known caveats:
  - Windows-only. The locate command and swap script are cmd.exe / Windows specific.
  - Hardcoded process name 'agent.exe'. If the customer renamed it, taskkill misses.
  - The relaunched agent loses its original argv (no -id, no -server, no -token).
    Only works if those came from environment variables or the agent uses defaults.
  - 2-second sleep before kill is a best guess; on slow machines the controller
    may see the response before the kill happens, on fast machines the kill may
    race the response.

Until we've burned a test machine to verify all of the above, treat this as
a debugging aid, not a production tool.`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			deviceID := args[0]
			localBin := args[1]

			c := newClient()
			defer c.Close()

			// Step 1: locate the running agent on the remote device.
			fmt.Println("locating agent process on remote...")
			locate, err := c.Exec(deviceID, locateAgentCommand())
			if err != nil {
				fmt.Fprintf(os.Stderr, "locate failed: %v\n", err)
				os.Exit(1)
			}
			currentPath := strings.TrimSpace(locate.Stdout)
			if currentPath == "" {
				fmt.Fprintln(os.Stderr, "could not find running agent on the remote — is it actually running?")
				os.Exit(1)
			}
			fmt.Printf("remote agent path: %s\n", currentPath)

			// Step 2: push new binary to a temp path.
			tmpPath := tempUpgradePath(deviceID, currentPath)
			fmt.Printf("pushing new binary to %s...\n", tmpPath)
			pushRes, err := c.PushFile(deviceID, localBin, tmpPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "push failed: %v\n", err)
				os.Exit(1)
			}
			if !pushRes.Success {
				fmt.Fprintf(os.Stderr, "push rejected: %s\n", pushRes.Error)
				os.Exit(1)
			}

			// Step 3: launch the swap script. It detaches from the agent
			// process, sleeps briefly, then kills + replaces + relaunches.
			fmt.Println("triggering atomic swap on remote...")
			swap, err := c.Exec(deviceID, swapCommand(currentPath, tmpPath))
			if err != nil {
				fmt.Fprintf(os.Stderr, "swap dispatch failed: %v\n", err)
				os.Exit(1)
			}
			// We expect the agent to die mid-exec; the result we see here
			// is best-effort. Don't bail just because exit code is nonzero.
			if swap.Stdout != "" {
				fmt.Print(swap.Stdout)
			}
			fmt.Println("upgrade dispatched; agent should reconnect with the new version within ~10s")
			fmt.Println("run 'remote-agent list' to verify the version column updated")
		},
	}
}

// locateAgentCommand returns a command that prints the absolute path of
// the running agent process. Falls through to nothing if not found.
//
// We can't tell the agent's OS at the controller side without first
// listing devices, so we send a single command line that works on both
// cmd.exe (Windows) and sh (mac/linux). The trick: cmd.exe runs the
// "powershell" branch and ignores the rest because of how `&&`/`;` are
// parsed differently — easier to just send platform-specific upgrades.
//
// Instead, we ship two separate command paths and let the CLI pick.
// But the CLI doesn't know the OS yet at this point; the simplest
// portable approach is to query OS via list before this function is
// called. For now we use a heuristic: try Windows first, fall back to
// unix. See pickedCommand below.
func locateAgentCommand() string {
	// powershell that finds the *first* process whose name starts with
	// "agent" and prints its full Path. On non-Windows this command
	// wouldn't run, but `upgrade` is only meaningful for Windows clients
	// today — Mac/Linux customers we'll add when we have any.
	return `powershell -NoProfile -Command "(Get-Process | Where-Object { $_.Path -ne $null -and $_.ProcessName -like 'agent*' } | Select-Object -First 1).Path"`
}

// tempUpgradePath returns a remote temp path for the new binary that
// won't collide with the current one.
func tempUpgradePath(deviceID, currentPath string) string {
	// Place next to the current binary so move-across-volumes isn't
	// needed and ACLs are preserved.
	dir := currentPath
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '\\' || dir[i] == '/' {
			dir = dir[:i]
			break
		}
	}
	return dir + "\\agent.new.exe"
}

// swapCommand returns a Windows cmd that:
//  1. spawns a detached cmd that sleeps, kills the running agent,
//     replaces the file, and relaunches.
//  2. exits the current exec immediately so the agent's reply still
//     reaches the controller before the agent dies.
func swapCommand(currentPath, newPath string) string {
	// `start "" /B cmd /C "(swap script)"` — start with empty title and
	// /B so no new window. The script first waits 2s, kills agent.exe,
	// moves the new file, then relaunches.
	script := `timeout /T 2 /NOBREAK >nul && taskkill /F /IM agent.exe >nul 2>&1 && move /Y "` + newPath + `" "` + currentPath + `" && start "" "` + currentPath + `"`
	return `start "" /B cmd /C "` + script + `"`
}

func shellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <device-id>",
		Short: "Interactive remote shell",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			deviceID := args[0]

			c := newClient()
			defer c.Close()

			fmt.Printf("Connected to %s. Type 'exit' to quit.\n", deviceID)

			scanner := bufio.NewScanner(os.Stdin)
			for {
				fmt.Printf("%s> ", deviceID)
				if !scanner.Scan() {
					break
				}

				command := strings.TrimSpace(scanner.Text())
				if command == "" {
					continue
				}
				if command == "exit" || command == "quit" {
					break
				}

				result, err := c.Exec(deviceID, command)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					continue
				}

				if result.Stdout != "" {
					fmt.Print(result.Stdout)
				}
				if result.Stderr != "" {
					fmt.Fprint(os.Stderr, result.Stderr)
				}
				if result.ExitCode != 0 {
					fmt.Fprintf(os.Stderr, "(exit code: %d)\n", result.ExitCode)
				}
			}
		},
	}
}

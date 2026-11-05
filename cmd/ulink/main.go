// Command ulink is the CLI-first personal device fabric: pair your own
// machines, then run commands / keep sessions / move files / forward ports
// across Mac, Windows and Linux.  AI agents use the same JSON envelope or
// the built-in MCP server (`ulink mcp`).
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/ulink"
	"github.com/dongsheng123132/remote-agent/internal/version"
	"github.com/spf13/cobra"
)

var (
	flagRelay     string
	flagToken     string
	flagConfigDir string
	flagJSON      bool
	flagSession   string
)

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit %d", e.code)
}

func main() {
	root := &cobra.Command{
		Use:           "ulink",
		Short:         "Personal multi-device CLI: pair your own machines, run commands, sync code, forward ports",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagRelay, "relay", "", "relay URL (default: ULINK_RELAY or U-Link desktop config)")
	root.PersistentFlags().StringVar(&flagToken, "token", "", "controller token (default: ULINK_TOKEN or U-Link desktop config)")
	root.PersistentFlags().StringVar(&flagConfigDir, "config-dir", "", "U-Link CLI state dir (default ~/.ulink)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "machine-readable JSON envelope")
	root.PersistentFlags().StringVar(&flagSession, "session", "", "audit session id (default: random per process)")

	root.AddCommand(devicesCmd(), runCmd(), auditCmd(), doctorCmd(), pairCmd(), mcpCmd(), versionCmd(), screenCmd(), typeCmd())

	if err := root.Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.err != nil && !flagJSON {
				fmt.Fprintln(os.Stderr, "Error:", ee.err)
			}
			os.Exit(ee.code)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(ulink.ExitCode(err))
	}
}

func loadConfig() (*ulink.Config, error) {
	dir := flagConfigDir
	if dir == "" {
		d, err := ulink.HomeDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	cfg, err := ulink.LoadFrom(dir)
	if err != nil {
		return nil, err
	}
	if flagRelay != "" {
		cfg.Relay = flagRelay
	}
	if flagToken != "" {
		cfg.ControllerToken = flagToken
	}
	return cfg, nil
}

func sessionID() string {
	if flagSession != "" {
		return flagSession
	}
	return ulink.NewSessionID()
}

func emit(env ulink.Envelope) error {
	if flagJSON {
		return ulink.WriteJSON(os.Stdout, env)
	}
	if env.OK {
		return nil
	}
	if env.Error != nil {
		fmt.Fprintf(os.Stderr, "%s: %s", env.Error.Code, env.Error.Message)
		if env.Error.Hint != "" {
			fmt.Fprintf(os.Stderr, " (%s)", env.Error.Hint)
		}
		fmt.Fprintln(os.Stderr)
	}
	return nil
}

func emitError(env ulink.Envelope, err error) error {
	if err != nil && (env.Error == nil || env.Error.Message == "") {
		env.Error = ulink.ClassifyError(err)
	}
	_ = emit(env)
	if env.Error != nil {
		return &exitError{code: ulink.ExitCodeForCode(env.Error.Code)}
	}
	return &exitError{code: ulink.ExitGeneral}
}

func devicesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "devices",
		Short: "List online devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			devices, err := ulink.ListDevices(cfg)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{"devices": devices}))
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tHOSTNAME\tOS\tVERSION\tSTATUS\tKEY")
			for _, d := range devices {
				key := "-"
				if d.KeyFingerprint != "" {
					key = truncate(d.KeyFingerprint, 8)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\tonline\t%s\n", d.ID, d.Hostname, d.OS, d.Version, key)
			}
			return tw.Flush()
		},
	}
}

func runCmd() *cobra.Command {
	var shell, commandFlag string
	cmd := &cobra.Command{
		Use:   "run <device> -- <command...>",
		Short: "Run one command on a device (use -- before flags/commands)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			device := args[0]
			command := commandFlag
			if command == "" {
				dash := cmd.ArgsLenAtDash()
				if dash >= 0 {
					command = strings.Join(args[dash:], " ")
				} else if len(args) > 1 {
					command = strings.Join(args[1:], " ")
				}
			}
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			res, err := ulink.Run(cfg, sessionID(), device, command, shell)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				env := ulink.NewOK(res)
				if res.ExitCode != 0 {
					env.OK = false
					env.Error = &ulink.Err{
						Code:    "remote_failed",
						Message: fmt.Sprintf("remote command exited with code %d", res.ExitCode),
					}
				}
				_ = emit(env)
				if res.ExitCode != 0 {
					return &exitError{code: ulink.ExitRemote}
				}
				return nil
			}
			if res.Stdout != "" {
				fmt.Fprint(os.Stdout, res.Stdout)
			}
			if res.Stderr != "" {
				fmt.Fprint(os.Stderr, res.Stderr)
			}
			fmt.Fprintf(os.Stderr, "\n[exit_code=%d duration_ms=%d device=%s session=%s]\n",
				res.ExitCode, res.DurationMS, res.Device, res.Session)
			if res.ExitCode != 0 {
				return &exitError{code: ulink.ExitRemote}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "", "Windows shell: cmd or powershell")
	cmd.Flags().StringVar(&commandFlag, "command", "", "command string (alternative to `-- <command...>`)")
	return cmd
}

func auditCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Read the local audit log tail",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			entries, err := ulink.ReadAuditTail(cfg.AuditPath, limit)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{"entries": entries, "audit_path": cfg.AuditPath}))
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "TIME\tACTION\tDEVICE\tEXIT\tDURATION\tCOMMAND")
			for _, e := range entries {
				ts := e.Time.Local().Format("01-02 15:04:05")
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%dms\t%s\n",
					ts, e.Action, e.Device, e.ExitCode, e.DurationMS, truncate(e.Command, 80))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "number of audit entries")
	return cmd
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check config, identity and relay connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			id, _ := ulink.LoadIdentity(cfg.ConfigDir)
			trusted, _ := ulink.LoadTrusted(cfg.ConfigDir)
			report := map[string]any{
				"config_dir":   cfg.ConfigDir,
				"relay":        cfg.Relay,
				"token_set":    cfg.ControllerToken != "",
				"tls_pin_set":  cfg.TLSPin != "",
				"audit_path":   cfg.AuditPath,
				"identity":     id,
				"trusted":      len(trusted),
				"relay_ok":     false,
				"device_count": 0,
			}
			var pingErr error
			if err := cfg.EnsureConnectedConfig(); err == nil {
				if devices, err := ulink.ListDevices(cfg); err == nil {
					report["relay_ok"] = true
					report["device_count"] = len(devices)
				} else {
					pingErr = err
					report["relay_error"] = err.Error()
				}
			} else {
				pingErr = err
				report["relay_error"] = err.Error()
			}
			if flagJSON {
				_ = emit(ulink.NewOK(report))
			} else {
				fmt.Printf("config: %s\n", cfg.ConfigDir)
				fmt.Printf("relay : %s (token %s)\n", cfg.Relay, setWord(cfg.ControllerToken != ""))
				fmt.Printf("audit : %s\n", cfg.AuditPath)
				if id == nil {
					fmt.Println("identity: not created (run `ulink pair`)")
				} else {
					fmt.Printf("identity: %s  code=%s  fingerprint=%s\n", id.DeviceID, id.ShortCode, id.Fingerprint)
				}
				fmt.Printf("trusted: %d device(s)\n", len(trusted))
				if pingErr != nil {
					fmt.Printf("relay check: FAILED — %v\n", pingErr)
				} else {
					fmt.Printf("relay check: OK — %v device(s) online\n", report["device_count"])
				}
			}
			if pingErr != nil {
				return &exitError{code: ulink.ExitOffline, err: pingErr}
			}
			return nil
		},
	}
}

func pairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Device identity + relay pairing (init/issue/revoke)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create this machine's Ed25519 identity (idempotent)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			id, err := ulink.EnsureIdentity(cfg.ConfigDir)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(id))
			}
			fmt.Printf("device id : %s\n", id.DeviceID)
			fmt.Printf("short code: %s\n", id.ShortCode)
			fmt.Printf("fingerprint: %s\n", id.Fingerprint)
			fmt.Printf("public key : %s\n", id.PublicKey)
			fmt.Printf("state dir  : %s\n", cfg.ConfigDir)
			fmt.Println("\nv0.1: 这是本地身份脚手架；在另一台机器执行 `ulink pair trust <public key>` 先建立本地信任。relay 侧强制校验在 v0.2。")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show this identity and trusted keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			id, _ := ulink.LoadIdentity(cfg.ConfigDir)
			trusted, _ := ulink.LoadTrusted(cfg.ConfigDir)
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{"identity": id, "trusted": trusted}))
			}
			if id == nil {
				fmt.Println("no identity; run `ulink pair init`")
			} else {
				fmt.Printf("%s  code=%s\n%s\n", id.DeviceID, id.ShortCode, id.Fingerprint)
			}
			for _, t := range trusted {
				fmt.Printf("trusted: %s %s %s\n", t.Name, t.Fingerprint, t.AddedAt.Local().Format(time.RFC3339))
			}
			return nil
		},
	})
	var name string
	trust := &cobra.Command{
		Use:   "trust <base64-ed25519-public-key>",
		Short: "Trust a peer's raw base64 Ed25519 public key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			entry, err := ulink.TrustKey(cfg.ConfigDir, name, args[0])
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), "get the key from `ulink pair show` on the peer"), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(entry))
			}
			fmt.Printf("trusted %s (%s)\n", entry.Fingerprint, entry.Name)
			return nil
		},
	}
	trust.Flags().StringVar(&name, "name", "", "human label for the peer")
	cmd.AddCommand(trust)
	cmd.AddCommand(&cobra.Command{
		Use:   "issue",
		Short: "Ask the relay for a one-time pairing code (controller token required)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			code, expires, err := ulink.IssuePairCode(cfg)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{
					"code":       code,
					"expires_at": expires.UTC().Format(time.RFC3339),
				}))
			}
			fmt.Printf("pairing code: %s\nexpires at  : %s (local)\n", code, expires.Local().Format(time.RFC3339))
			fmt.Println("在目标机运行：ulink agent --pairing-code <code>（或 U-Link 设置里填 pairing_code）")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <device>",
		Short: "Revoke a device key and disconnect it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			if err := ulink.RevokeDevice(cfg, args[0]); err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{"device": args[0], "revoked": true}))
			}
			fmt.Printf("revoked %s\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "approve <code>",
		Short: "Approve an employee's enrollment code (controller)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			device, fingerprint, err := ulink.ApproveEnrollment(cfg, args[0])
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{"device": device, "key_fingerprint": fingerprint}))
			}
			fmt.Printf("approved %s\nkey: %s\n", device, fingerprint)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "code",
		Short: "Print this machine's 6-character pairing code",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			id, err := ulink.EnsureIdentity(cfg.ConfigDir)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			fmt.Println(id.ShortCode)
			return nil
		},
	})
	return cmd
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP stdio server exposing devices/run/audit",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			srv := &ulink.MCPServer{Cfg: cfg, Session: sessionID(), Out: os.Stdout, Err: os.Stderr}
			if err := srv.Serve(os.Stdin); err != nil {
				fmt.Fprintln(os.Stderr, "mcp:", err)
				return &exitError{code: ulink.ExitGeneral, err: err}
			}
			return nil
		},
	}
}

func screenCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "screenshot <device>",
		Short: "Capture one frame from a device and pull it locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			res, err := ulink.CaptureScreen(cfg, args[0], out)
			if err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(res))
			}
			fmt.Printf("saved %s (%d bytes, %s)\n", res.LocalPath, res.Bytes, res.OS)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "local output path (default ./ulink-screen-<device>-<ts>.png)")
	return cmd
}

func typeCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{
		Use:   "type <device>",
		Short: "Paste text into the focused window of a device (clipboard + paste)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return emitError(ulink.NewErr("usage", err.Error(), ""), err)
			}
			if err := ulink.TypeText(cfg, args[0], text); err != nil {
				return emitError(ulink.NewErr("", "", ""), err)
			}
			if flagJSON {
				return emit(ulink.NewOK(map[string]any{"device": args[0], "typed": len(text)}))
			}
			fmt.Printf("typed %d chars into %s\n", len(text), args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "text to paste into the focused window")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			if flagJSON {
				_ = ulink.WriteJSON(os.Stdout, ulink.NewOK(map[string]string{"version": version.Version}))
				return
			}
			fmt.Println(version.Version)
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func setWord(ok bool) string {
	if ok {
		return "set"
	}
	return "MISSING"
}

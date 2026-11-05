package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/spf13/cobra"
)

// watchCmd polls `list` on an interval and announces newly-online
// devices the first time they appear.  Solves a real operator pain
// point: today the operator has to refresh `list` manually after
// telling a customer "run this command", which means small windows
// of "is the agent up yet" anxiety.
//
// Implementation choices:
//   - Polling rather than a server push because (a) the relay protocol
//     doesn't yet have a controller-subscribe message, (b) polling once
//     every 3 seconds is cheap (<1KB JSON) and easy to reason about
//     across reconnects.
//   - We close+reopen the connection between polls so a stale ws (e.g.
//     killed by the relay's idle timeout) heals on the next tick
//     instead of silently losing devices.
//   - Bell character (\a) on new device is opt-out, not opt-in: most
//     operators *want* the audio cue when handling support requests
//     while focusing on something else.  Disable with --no-bell.
func watchCmd() *cobra.Command {
	var (
		interval     int
		noBell       bool
		hostnameOnly string
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch for newly-online devices and announce them",
		Long: `Polls the relay every few seconds and prints a notification
the first time a device appears online.  Useful when you've told a
customer "run this command" and want to know the moment they connect.

Press Ctrl+C to stop.

Filter to specific hostname patterns with --hostname (substring match,
case-insensitive).  Useful when running support for one customer and
you don't want a noisy console because someone else's device just
happened to come online.`,
		Run: func(cmd *cobra.Command, args []string) {
			seen := make(map[string]bool)
			first := true

			// Ctrl+C clean exit
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			done := make(chan struct{})
			go func() {
				<-sigCh
				fmt.Fprintln(os.Stderr, "\n  watch stopped.")
				close(done)
			}()

			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()

			poll := func() {
				c := newClient()
				defer c.Close()
				devices, err := c.ListDevices()
				if err != nil {
					fmt.Fprintf(os.Stderr, "  poll error: %v\n", err)
					return
				}
				announceDevices(devices, seen, first, noBell, hostnameOnly)
				first = false
			}

			poll()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					poll()
				}
			}
		},
	}
	cmd.Flags().IntVar(&interval, "interval", 3, "poll interval in seconds")
	cmd.Flags().BoolVar(&noBell, "no-bell", false, "disable terminal bell on new device")
	cmd.Flags().StringVar(&hostnameOnly, "hostname", "", "only announce devices whose hostname contains this substring (case-insensitive)")
	return cmd
}

// announceDevices prints fresh devices and updates the seen set.
// On the very first poll we print everything currently online without
// an alert sound — that's the baseline.  Only subsequent polls trigger
// the bell.
func announceDevices(devices []protocol.DeviceInfo, seen map[string]bool, first, noBell bool, hostFilter string) {
	for _, d := range devices {
		if !d.Online {
			continue
		}
		if hostFilter != "" && !strings.Contains(strings.ToLower(d.Hostname), strings.ToLower(hostFilter)) {
			continue
		}
		if seen[d.ID] {
			continue
		}
		seen[d.ID] = true

		ts := time.Now().Format("15:04:05")
		if first {
			// Already-online baseline; no bell.
			fmt.Printf("[%s] (already online) %s  %s  %s\n", ts, d.ID, d.Hostname, d.OS)
		} else {
			bell := ""
			if !noBell {
				bell = "\a"
			}
			fmt.Printf("%s\x1b[1;32m[%s] ✱ NEW DEVICE: %s\x1b[0m  hostname=%s  os=%s  version=%s\n",
				bell, ts, d.ID, d.Hostname, d.OS, valueOr(d.Version, "-"))
		}
	}
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

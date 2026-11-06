package desktop

import (
	"context"
	"strings"
	"time"

	cliclient "github.com/dongsheng123132/remote-agent/internal/cli"
	"github.com/dongsheng123132/remote-agent/internal/protocol"
)

// listDevices asks the relay which devices are online.  Each call uses a
// short-lived controller connection: the relay's list RPC is stateless,
// and keeping a long-lived controller socket around just to poll every
// 15s adds reconnect bookkeeping for no benefit.
func listDevices(ctx context.Context, cfg Config) ([]protocol.DeviceInfo, error) {
	c := cliclient.NewClient(controllerEndpoint(cfg.Relay), cfg.ControllerToken)
	c.SetBypassProxy(cfg.BypassProxy)
	if cfg.TLSPin != "" {
		c.SetTLSPin(cfg.TLSPin)
	}
	if err := c.Connect(); err != nil {
		return nil, err
	}
	defer c.Close()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.ListDevices()
}

// execStream is one in-flight streaming exec on a remote device.
type execStream struct {
	reqID  string
	events <-chan cliclient.StreamEvent
	close  func()
}

// startExecStream opens a controller connection, asks the target device
// to run command, and multiplexes the chunks onto a context-aware
// channel.  The caller must call close() when done.
func startExecStream(ctx context.Context, cfg Config, device, command, shell string) (*execStream, error) {
	c := cliclient.NewClient(controllerEndpoint(cfg.Relay), cfg.ControllerToken)
	c.SetBypassProxy(cfg.BypassProxy)
	if cfg.TLSPin != "" {
		c.SetTLSPin(cfg.TLSPin)
	}
	if err := c.Connect(); err != nil {
		return nil, err
	}

	reqID, raw, cancel, err := c.ExecStream(device, command, cliclient.ExecOptions{Shell: shell})
	if err != nil {
		c.Close()
		return nil, err
	}

	out := make(chan cliclient.StreamEvent, 64)
	go func() {
		defer close(out)
		defer c.Close()
		for {
			select {
			case ev, ok := <-raw:
				if !ok {
					return
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					_ = cancel()
					return
				}
			case <-ctx.Done():
				_ = cancel()
				return
			}
		}
	}()

	var closed bool
	closeFn := func() {
		if closed {
			return
		}
		closed = true
		_ = cancel()
		c.Close()
	}
	return &execStream{reqID: reqID, events: out, close: closeFn}, nil
}

// agentEndpoint / controllerEndpoint turn the configured relay base URL
// into the two protocol endpoints.  Users paste "wss://host:8900" (like
// the Tailscale-style address), not the full path.
func agentEndpoint(relay string) string {
	base := strings.TrimRight(strings.TrimSpace(relay), "/")
	if strings.HasSuffix(base, "/ws/agent") {
		return base
	}
	return base + "/ws/agent"
}

func controllerEndpoint(relay string) string {
	base := strings.TrimRight(strings.TrimSpace(relay), "/")
	if strings.HasSuffix(base, "/ws/controller") {
		return base
	}
	return base + "/ws/controller"
}

// execTimeout bounds a single dashboard command.  The agent itself has a
// 5 minute exec timeout; this only guards against a wedged connection.
const execTimeout = 6 * time.Minute

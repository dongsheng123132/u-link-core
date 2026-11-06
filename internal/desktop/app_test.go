package desktop

import (
	"strings"
	"testing"

	"github.com/dongsheng123132/remote-agent/internal/agent"
)

// A self-hosted install starts with no relay and no tokens.  Starting the
// agent in that state must report the problem instead of opening a client and
// retrying an empty URL forever.
func TestStartAgentWithoutRelayDoesNotDial(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Relay = ""
	a.cfg.AgentToken = ""

	a.startAgent()

	a.mu.RLock()
	state, errMsg := a.agentState, a.agentErr
	a.mu.RUnlock()

	if state != agent.ConnStateOffline {
		t.Fatalf("state = %q, want offline", state)
	}
	if !strings.Contains(errMsg, "未配置") {
		t.Fatalf("error = %q, want a configuration hint", errMsg)
	}
	a.agentMu.Lock()
	client := a.agentClient
	a.agentMu.Unlock()
	if client != nil {
		t.Fatal("must not create an agent client when unconfigured")
	}
}

func TestStartAgentWithoutAgentTokenDoesNotDial(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Relay = "wss://relay.example.com:8900"
	a.cfg.AgentToken = ""

	a.startAgent()

	a.agentMu.Lock()
	client := a.agentClient
	a.agentMu.Unlock()
	if client != nil {
		t.Fatal("must not create an agent client without an agent token")
	}
	if got := a.snapshotConfig().Relay; got != "wss://relay.example.com:8900" {
		t.Fatalf("relay should be untouched, got %q", got)
	}
}

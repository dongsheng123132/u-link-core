package desktop

import (
	"context"
	"strings"
	"testing"
)

func TestEnrollCommandUsesAgentTokenOnly(t *testing.T) {
	a := newTestApp(t)
	cmd := a.enrollCommand("win-office")
	if cmd == "" {
		t.Fatal("expected a command")
	}
	for _, want := range []string{
		"ulink-agent",
		"--server wss://relay.example.com:8900",
		"--token agent-secret",
		"--id win-office",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "controller-secret") {
		t.Fatalf("enrollment command leaked the controller token: %s", cmd)
	}
}

func TestEnrollCommandIncludesTLSPinOnlyWhenSet(t *testing.T) {
	a := newTestApp(t)
	if cmd := a.enrollCommand("m1"); strings.Contains(cmd, "--tls-pin") {
		t.Fatalf("pin must be omitted when unset: %s", cmd)
	}
	a.cfg.TLSPin = "AA:BB:CC"
	if cmd := a.enrollCommand("m1"); !strings.Contains(cmd, "--tls-pin AA:BB:CC") {
		t.Fatalf("pin must be passed through: %s", cmd)
	}
}

func TestEnrollCommandRequiresRelayAndAgentToken(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Relay = ""
	if cmd := a.enrollCommand("m1"); cmd != "" {
		t.Fatalf("expected no command without a relay, got %q", cmd)
	}
	a.cfg.Relay = "wss://relay.example.com:8900"
	a.cfg.AgentToken = ""
	if cmd := a.enrollCommand("m1"); cmd != "" {
		t.Fatalf("expected no command without an agent token, got %q", cmd)
	}
}

// With nothing configured, doctor must point at the missing configuration and
// must not attempt network calls against an empty relay.
func TestDoctorReportsMissingConfiguration(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Relay = ""
	a.cfg.AgentToken = ""
	a.cfg.ControllerToken = ""

	checks := a.doctor(context.Background())
	byID := map[string]DoctorCheck{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	if c, ok := byID["relay"]; !ok || c.Status != statusFail {
		t.Fatalf("relay check = %+v", c)
	}
	if c, ok := byID["agent_token"]; !ok || c.Status != statusFail {
		t.Fatalf("agent token check = %+v", c)
	}
	if c, ok := byID["controller_token"]; !ok || c.Status != statusWarn {
		t.Fatalf("controller token check = %+v", c)
	}
	if _, ok := byID["relay_tls"]; ok {
		t.Fatal("must not probe TLS when no relay is configured")
	}
	if _, ok := byID["controller"]; ok {
		t.Fatal("must not run controller RPC when no token is configured")
	}
	if c, ok := byID["local_port"]; !ok || c.Status != statusWarn {
		t.Fatalf("local port check = %+v", c)
	}
}

func TestDoctorReportsConfiguredRelayAndTokens(t *testing.T) {
	a := newTestApp(t)
	// Point at a loopback address that is not listening: the TLS check must
	// fail deterministically without reaching the network.
	a.cfg.Relay = "wss://127.0.0.1:1"
	a.cfg.ControllerToken = ""

	checks := a.doctor(context.Background())
	byID := map[string]DoctorCheck{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	if c := byID["relay"]; c.Status != statusOK {
		t.Fatalf("relay check = %+v", c)
	}
	if c := byID["agent_token"]; c.Status != statusOK {
		t.Fatalf("agent token check = %+v", c)
	}
	if c := byID["relay_tls"]; c.Status != statusFail {
		t.Fatalf("unreachable relay should fail the TLS check: %+v", c)
	}
	if c := byID["controller_token"]; c.Status != statusWarn {
		t.Fatalf("controller token check = %+v", c)
	}
}

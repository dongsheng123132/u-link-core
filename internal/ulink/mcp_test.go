package ulink

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPServeInitializeAndToolsList(t *testing.T) {
	var out bytes.Buffer
	srv := &MCPServer{Cfg: &Config{}, Out: &out}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"not/a/method"}
`)
	if err := srv.Serve(in); err != nil {
		t.Fatalf("serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 responses, got %d: %q", len(lines), out.String())
	}

	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatal(err)
	}
	if initResp.Result.ProtocolVersion == "" || initResp.Result.ServerInfo.Name != "ulink" {
		t.Fatalf("bad initialize response: %s", lines[0])
	}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Result.Tools) != 3 {
		t.Fatalf("want 3 tools, got %d", len(listResp.Result.Tools))
	}

	var errResp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error == nil || errResp.Error.Code != -32601 {
		t.Fatalf("want method-not-found, got %s", lines[2])
	}
}

func TestMCPServeIgnoresNotifications(t *testing.T) {
	var out bytes.Buffer
	srv := &MCPServer{Cfg: &Config{}, Out: &out}
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}
`)
	if err := srv.Serve(in); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("notification should not produce a response: %q", out.String())
	}
}

func TestMCPToolsHaveObjectSchema(t *testing.T) {
	for _, tool := range mcpTools() {
		if tool.Name == "" || tool.Description == "" {
			t.Errorf("tool missing name/description: %+v", tool)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s: schema type is %v", tool.Name, tool.InputSchema["type"])
		}
		if _, ok := tool.InputSchema["properties"]; !ok {
			t.Errorf("%s: missing properties", tool.Name)
		}
	}
}

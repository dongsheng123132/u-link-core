package ulink

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dongsheng123132/remote-agent/internal/version"
)

// MCPServer exposes devices/run/audit over the Model Context Protocol
// stdio transport.  It is intentionally small: no external deps, one
// JSON-RPC message per line, stdout only for protocol frames.
type MCPServer struct {
	Cfg     *Config
	Session string
	Out     io.Writer
	Err     io.Writer
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// Serve reads JSON-RPC requests until EOF.
func (s *MCPServer) Serve(in io.Reader) error {
	if s.Out == nil {
		return fmt.Errorf("mcp: Out is nil")
	}
	if s.Session == "" {
		s.Session = NewSessionID()
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.write(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		resp, notify := s.handle(req)
		if notify {
			continue
		}
		s.write(resp)
	}
	return sc.Err()
}

func (s *MCPServer) write(resp rpcResponse) {
	resp.JSONRPC = "2.0"
	enc := json.NewEncoder(s.Out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(resp)
}

func (s *MCPServer) handle(req rpcRequest) (rpcResponse, bool) {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if isNotification {
		return resp, true
	}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "ulink", "version": version.Version},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid tools/call params"}
			return resp, false
		}
		result, err := s.callTool(params.Name, params.Arguments)
		if err != nil {
			resp.Result = mcpToolResult{IsError: true, Content: []mcpContent{{Type: "text", Text: err.Error()}}}
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp, false
}

func mcpTools() []mcpTool {
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}
	return []mcpTool{
		{
			Name:        "ulink_devices",
			Description: "List online devices on the U-Link relay. Returns id, hostname, OS, version, online.",
			InputSchema: obj(map[string]any{}),
		},
		{
			Name:        "ulink_run",
			Description: "Run one command on a paired device and return stdout/stderr/exit_code. Use `ulink_devices` first to get the device id.",
			InputSchema: obj(map[string]any{
				"device":  map[string]any{"type": "string", "description": "Device id from ulink_devices"},
				"command": map[string]any{"type": "string", "description": "Command to execute on the remote machine"},
				"shell":   map[string]any{"type": "string", "enum": []string{"cmd", "powershell"}, "description": "Windows shell hint; omit for default"},
				"session": map[string]any{"type": "string", "description": "Optional audit session id"},
			}, "device", "command"),
		},
		{
			Name:        "ulink_audit",
			Description: "Read the local U-Link audit log (JSONL) tail. Every remote run is recorded here.",
			InputSchema: obj(map[string]any{
				"limit": map[string]any{"type": "number", "description": "How many entries to return (default 20)"},
			}),
		},
	}
}

func (s *MCPServer) callTool(name string, args map[string]any) (mcpToolResult, error) {
	switch name {
	case "ulink_devices":
		devices, err := ListDevices(s.Cfg)
		if err != nil {
			return mcpToolResult{}, err
		}
		data, _ := json.MarshalIndent(devices, "", "  ")
		return textResult(string(data), false), nil
	case "ulink_run":
		device, _ := args["device"].(string)
		command, _ := args["command"].(string)
		shell, _ := args["shell"].(string)
		session, _ := args["session"].(string)
		if session == "" {
			session = s.Session
		}
		res, err := Run(s.Cfg, session, device, command, shell)
		if err != nil {
			return mcpToolResult{}, err
		}
		var b strings.Builder
		b.WriteString(res.Stdout)
		if res.Stderr != "" {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteString("\n")
			}
			b.WriteString(res.Stderr)
		}
		if b.Len() == 0 {
			b.WriteString("(no output)")
		}
		fmt.Fprintf(&b, "\n[exit_code=%d duration_ms=%d device=%s]", res.ExitCode, res.DurationMS, res.Device)
		return textResult(b.String(), res.ExitCode != 0), nil
	case "ulink_audit":
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		entries, err := ReadAuditTail(s.Cfg.AuditPath, limit)
		if err != nil {
			return mcpToolResult{}, err
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		return textResult(string(data), false), nil
	default:
		return mcpToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}
}

func textResult(text string, isError bool) mcpToolResult {
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}, IsError: isError}
}

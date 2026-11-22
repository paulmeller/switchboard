package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (a *App) serveMCP(c CLI) int {
	in := io.Reader(os.Stdin)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	enc := json.NewEncoder(a.Out)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if json.Unmarshal(line, &req) != nil || req.Method == "" {
			if len(req.ID) > 0 && string(req.ID) != "null" {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32600, "message": "invalid request"}})
			}
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			// Notification (e.g. notifications/initialized): no response.
			continue
		}
		var result any
		var rpcErr map[string]any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "switchboard", "version": Version},
			}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": mcpTools()}
		case "tools/call":
			result, rpcErr = a.callMCPTool(c, req.Params)
		default:
			rpcErr = map[string]any{"code": -32601, "message": "method not found"}
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		if enc.Encode(resp) != nil {
			return 1
		}
	}
	return 0
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "sb_route",
			"description": "Dry-run route a coding task to the best flat-rate AI subscription (Claude, Codex, Antigravity) without launching.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task":    map[string]any{"type": "string", "description": "Task prompt to route"},
					"account": map[string]any{"type": "string", "description": "Optional tool/profile pin"},
				},
				"required": []string{"task"},
			},
		},
		{
			"name":        "sb_exec",
			"description": "Stripe and run a coding task non-interactively on the best available subscription CLI (claude -p, codex exec, agy --print, or grok -p).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task":    map[string]any{"type": "string", "description": "Task prompt to execute headlessly"},
					"account": map[string]any{"type": "string", "description": "Optional tool/profile pin"},
				},
				"required": []string{"task"},
			},
		},
		{
			"name":        "sb_status",
			"description": "Check login status across all configured AI CLI accounts.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "sb_usage",
			"description": "Read normalized quota and remaining headroom across all configured AI CLI accounts.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "sb_cooldown",
			"description": "Report a rate-limit (429) cooldown for an account so switchboard temporarily routes around it.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"account":  map[string]any{"type": "string", "description": "Account id (e.g. codex/work)"},
					"duration": map[string]any{"type": "string", "description": "Cooldown duration (e.g. 1h, 30m)"},
				},
				"required": []string{"account"},
			},
		},
	}
}

func (a *App) callMCPTool(c CLI, raw json.RawMessage) (any, map[string]any) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if json.Unmarshal(raw, &call) != nil {
		return nil, map[string]any{"code": -32602, "message": "invalid params"}
	}
	str := func(k string) string {
		if v, ok := call.Arguments[k].(string); ok {
			return v
		}
		return ""
	}
	var out, diag bytes.Buffer
	sub := *a
	sub.Out, sub.Err = &out, &diag
	args := []string{}
	if c.Config != "" {
		args = append(args, "--config", c.Config)
	}
	switch call.Name {
	case "sb_route":
		if str("account") != "" {
			args = append(args, "--account", str("account"))
		}
		args = append(args, "--dry-run", "--json", "run", str("task"))
	case "sb_exec":
		if str("account") != "" {
			args = append(args, "--account", str("account"))
		}
		args = append(args, "exec", str("task"))
	case "sb_status":
		args = append(args, "--json", "status")
	case "sb_usage":
		args = append(args, "--json", "usage")
	case "sb_cooldown":
		args = append(args, "--json", "cooldown", str("account"))
		if d := str("duration"); d != "" {
			args = append(args, "--duration", d)
		}
	default:
		return nil, map[string]any{"code": -32602, "message": "unknown tool"}
	}
	code := sub.Run(args)
	text := out.String()
	if text == "" {
		text = diag.String()
	}
	if code != 0 && diag.Len() > 0 && out.Len() > 0 {
		text = out.String() + "\n" + diag.String()
	}
	if code != 0 {
		text = fmt.Sprintf("exit %d: %s", code, text)
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": code != 0,
	}, nil
}

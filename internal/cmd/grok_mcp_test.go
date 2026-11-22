package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecHeadlessAndMeteredFallback(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	for _, tool := range []string{"claude", "codex", "agy", "grok"} {
		if os.WriteFile(filepath.Join(binDir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o700) != nil {
			t.Fatal("fake binary")
		}
	}
	var gotArgs []string
	var gotPath string
	execFn := func(path string, args, env []string) (int, error) {
		gotPath, gotArgs = path, args
		return 0, nil
	}

	// Default allow_metered_fallback = false: if subs are signed out and only grok is signed in, exit 3.
	cfgNoFallback := filepath.Join(t.TempDir(), "no-fallback.toml")
	os.WriteFile(cfgNoFallback, []byte("[classifier]\nprovider = 'off'\n"), 0o600)
	var out, diag bytes.Buffer
	a := App{
		Out: &out, Err: &diag, Home: home,
		Env:     []string{"PATH=" + binDir, "SB_CONFIG=" + cfgNoFallback},
		Probe:   profileProbe{map[string]bool{"grok/cli": true}},
		Execute: execFn,
	}
	if a.Run([]string{"exec", "fix bug"}) != 3 {
		t.Fatal("expected exit 3 without allow_metered_fallback")
	}

	// With allow_metered_fallback = true, falls back to grok -p.
	cfgFallback := filepath.Join(t.TempDir(), "fallback.toml")
	os.WriteFile(cfgFallback, []byte("allow_metered_fallback = true\n[classifier]\nprovider = 'off'\n"), 0o600)
	a.Env = []string{"PATH=" + binDir, "SB_CONFIG=" + cfgFallback}
	if a.Run([]string{"exec", "fix bug"}) != 0 || !strings.HasSuffix(gotPath, "/grok") || len(gotArgs) != 2 || gotArgs[0] != "-p" || gotArgs[1] != "fix bug" {
		t.Fatalf("expected grok -p fallback, got path=%s args=%v diag=%s", gotPath, gotArgs, diag.String())
	}

	// Headless exec onto codex sub uses codex exec.
	a.Probe = profileProbe{map[string]bool{"codex/work": true, "grok/cli": true}}
	if a.Run([]string{"exec", "fix bug"}) != 0 || !strings.HasSuffix(gotPath, "/codex") || len(gotArgs) != 2 || gotArgs[0] != "exec" || gotArgs[1] != "fix bug" {
		t.Fatalf("expected codex exec, got path=%s args=%v diag=%s", gotPath, gotArgs, diag.String())
	}

	// Headless exec pinned to claude/work uses claude -p.
	a.Probe = profileProbe{map[string]bool{"claude/work": true}}
	a.Env = []string{"PATH=" + binDir, "SB_CONFIG=" + cfgFallback, "CLAUDE_OAUTH_TOKEN_1=fixture"}
	if a.Run([]string{"--account", "claude/work", "exec", "design api"}) != 0 || !strings.HasSuffix(gotPath, "/claude") || len(gotArgs) != 2 || gotArgs[0] != "-p" || gotArgs[1] != "design api" {
		t.Fatalf("expected claude -p, got path=%s args=%v diag=%s", gotPath, gotArgs, diag.String())
	}
}

func TestMCPServe(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	for _, tool := range []string{"claude", "codex", "agy", "grok"} {
		os.WriteFile(filepath.Join(binDir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o700)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfg, []byte("[classifier]\nprovider = 'off'\n"), 0o600)

	var gotArgs []string
	a := App{
		Home: home,
		Env:  []string{"PATH=" + binDir, "SB_CONFIG=" + cfg},
		Probe: profileProbe{map[string]bool{
			"codex/work": true,
		}},
		UsageSource: headroomSource{},
		Execute: func(path string, args, env []string) (int, error) {
			gotArgs = args
			return 0, nil
		},
	}

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sb_route","arguments":{"task":"implement parser"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sb_exec","arguments":{"task":"implement parser"}}}`,
	}, "\n") + "\n"

	// Swap os.Stdin temporarily for serveMCP.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		_, _ = w.WriteString(reqs)
		_ = w.Close()
	}()

	var out, diag bytes.Buffer
	a.Out, a.Err = &out, &diag
	if a.Run([]string{"mcp"}) != 0 {
		t.Fatal("mcp failed", diag.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 MCP responses, got %d: %s", len(lines), out.String())
	}
	var listResp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(lines[1]), &listResp) != nil || len(listResp.Result.Tools) != 5 {
		t.Fatalf("expected 5 MCP tools, got %s", lines[1])
	}
	if len(gotArgs) != 2 || gotArgs[0] != "exec" || gotArgs[1] != "implement parser" {
		t.Fatalf("sb_exec did not run codex exec: %v", gotArgs)
	}
}

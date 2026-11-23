package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"switchboard/internal/provider"
)

type readFunc func(context.Context, Operation, []string) ([]byte, error)

func (f readFunc) Read(c context.Context, o Operation, e []string) ([]byte, error) { return f(c, o, e) }

func TestRPCExactAllowlist(t *testing.T) {
	var requests bytes.Buffer
	raw, err := rateRPC(strings.NewReader("{\"id\":1,\"result\":{}}\n{\"method\":\"notice\"}\n{\"id\":2,\"result\":{\"rateLimits\":{}}}\n"), &requests)
	if err != nil || string(raw) != `{"rateLimits":{}}` {
		t.Fatal("RPC result", err)
	}
	dec := json.NewDecoder(&requests)
	expected := []string{`{"id":1,"method":"initialize","params":{"clientInfo":{"name":"switchboard","version":"0.1.0"}}}`, `{"method":"initialized"}`, `{"id":2,"method":"account/rateLimits/read","params":null}`}
	for _, want := range expected {
		var got, w any
		if dec.Decode(&got) != nil {
			t.Fatal("missing request")
		}
		json.Unmarshal([]byte(want), &w)
		if !reflect.DeepEqual(got, w) {
			t.Fatal("request outside exact protocol")
		}
	}
	if dec.More() {
		t.Fatal("extra request")
	}
}

func TestRPCFailure(t *testing.T) {
	for _, input := range []string{"bad\n", "", "{\"id\":1,\"error\":{\"message\":\"private diagnostic\"}}\n", "{\"id\":1,\"result\":{}}\n{\"id\":2,\"error\":{}}\n", "{\"id\":2,\"result\":{}}\n", strings.Repeat("x", 70000)} {
		var out bytes.Buffer
		_, err := rateRPC(strings.NewReader(input), &out)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe RPC error")
		}
	}
}

func TestCodexWindowMapping(t *testing.T) {
	runner := readFunc(func(_ context.Context, op Operation, env []string) ([]byte, error) {
		if op != CodexLimits || !contains(env, "CODEX_HOME=/fake/.codex-personal") {
			t.Fatal("wrong Codex operation/profile")
		}
		return []byte(`{"rateLimits":{"primary":{"usedPercent":17,"windowDurationMins":10080,"resetsAt":1800000000},"secondary":{"usedPercent":42,"windowDurationMins":300,"resetsAt":1800000100}},"private":"discard"}`), nil
	})
	d, err := (Codex{Runner: runner, Home: "/fake"}).Read(context.Background(), "codex", "personal")
	if err != nil || *d.Weekly != 17 || *d.Five != 42 || *d.WeeklyReset != sydneyTime(time.Unix(1800000000, 0)) || !*d.LoginOK || d.Source != "live codex account/rateLimits/read" {
		t.Fatal("window mapping/provenance", err)
	}
	for _, raw := range []string{`{}`, `bad`, `{"rateLimits":{"primary":{"usedPercent":101,"windowDurationMins":300}}}`, `{"rateLimits":{"primary":{"usedPercent":20,"windowDurationMins":60,"resetsAt":-1}}}`} {
		s := Codex{Runner: readFunc(func(context.Context, Operation, []string) ([]byte, error) { return []byte(raw), nil }), Home: "/fake"}
		if _, err := s.Read(context.Background(), "codex", "work"); err == nil {
			t.Fatal("malformed response accepted")
		}
	}
	s := Codex{Runner: readFunc(func(context.Context, Operation, []string) ([]byte, error) {
		return []byte(`{"rateLimits":{"secondary":{"usedPercent":7,"windowDurationMins":60}}}`), nil
	}), Home: "/fake"}
	d, err = s.Read(context.Background(), "codex", "work")
	if err != nil || d.Extra["60_minute"] == nil || d.Weekly != nil || d.Five != nil {
		t.Fatal("other window")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestSourceAllowlistAndIsolation(t *testing.T) {
	calls := []Operation{}
	dirs := []string{}
	runner := readFunc(func(_ context.Context, op Operation, env []string) ([]byte, error) {
		calls = append(calls, op)
		if op == ClaudeLogin {
			for _, v := range env {
				if strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") {
					dirs = append(dirs, v)
				}
			}
			for _, v := range env {
				if strings.HasPrefix(v, "ANTHROPIC_API_KEY=") || strings.HasPrefix(v, "CLAUDE_CODE_OAUTH_TOKEN=") || strings.HasPrefix(v, "CLAUDE_OAUTH_TOKEN_") {
					t.Fatal("override leaked")
				}
			}
			return []byte(`{"loggedIn":false,"email":"discard"}`), nil
		}
		return nil, errors.New("private failure")
	})
	for _, profile := range []string{"work", "personal", "plain"} {
		d, e := (Claude{Runner: runner, Home: "/fake", Env: []string{"CLAUDE_CODE_OAUTH_TOKEN=fixture", "ANTHROPIC_API_KEY=fixture", "CLAUDE_OAUTH_TOKEN_1=fixture"}}).Read(context.Background(), "claude", profile)
		if e != nil || d.LoginOK == nil || *d.LoginOK || d.Weekly != nil || d.Notice == "" {
			t.Fatal("Claude login only")
		}
	}
	if !reflect.DeepEqual(dirs, []string{"CLAUDE_CONFIG_DIR=/fake/.local/share/claude-usage/work", "CLAUDE_CONFIG_DIR=/fake/.local/share/claude-usage/personal", "CLAUDE_CONFIG_DIR=/fake/.claude"}) {
		t.Fatal("isolated config dirs")
	}
	(Grok{Runner: runner}).Read(context.Background(), "grok", "cli")
	if !reflect.DeepEqual(calls, []Operation{ClaudeLogin, ClaudeLogin, ClaudeLogin, GrokUsage}) {
		t.Fatal("unexpected operation")
	}
}

// Fake executables exercise actual argv, RPC transport, missing binaries and cancellation.
func TestCommandsFakeChildren(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
 '-s read-only app-server')
 IFS= read -r init
 echo '{"id":1,"result":{}}'
 IFS= read -r initialized
 IFS= read -r limits
 echo '{"id":2,"result":{"rateLimits":{}}}'
 # Block on stdin until the parent closes or kills us.
 read -r done
 ;;
 'auth status --json') echo '{"loggedIn":true}';;
 'usage --json') echo '{}';;
 *) exit 9;;
esac
`
	for _, name := range []string{"codex", "claude", "grok"} {
		if os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700) != nil {
			t.Fatal("write fake")
		}
	}
	t.Setenv("PATH", dir)
	for _, op := range []Operation{CodexLimits, ClaudeLogin, GrokUsage} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := (Commands{}).Read(ctx, op, []string{"PATH=" + dir})
		cancel()
		if err != nil {
			t.Fatal("allowed operation", op, err)
		}
	}
	if _, err := (Commands{}).Read(context.Background(), Operation(99), nil); err == nil {
		t.Fatal("unknown operation")
	}
	if os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o700) != nil {
		t.Fatal("write fake")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (Commands{}).Read(ctx, CodexLimits, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("timeout")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := (Commands{}).Read(context.Background(), CodexLimits, nil); err == nil {
		t.Fatal("missing binary")
	}
}

func TestLiveCollectionTimeoutAndPartial(t *testing.T) {
	runner := readFunc(func(ctx context.Context, op Operation, _ []string) ([]byte, error) {
		if op == CodexLimits {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if op == ClaudeLogin {
			return []byte(`{"loggedIn":true}`), nil
		}
		return nil, errors.New("private diagnostic")
	})
	sources := Sources{"codex": Codex{Runner: runner, Home: "/fake"}, "claude": Claude{Runner: runner, Home: "/fake"}, "grok": Grok{Runner: runner}}
	report, code := Collect(context.Background(), sources, provider.NewRegistry(nil), 5*time.Millisecond, time.Now())
	if code != 0 || len(report.Accounts) != 7 || report.SchemaVersion != 1 {
		t.Fatal("partial success contract")
	}
	for _, i := range []int{2, 3} {
		r := report.Accounts[i]
		if r.Error == nil || *r.Error != "usage source timed out" || r.LoginOK != nil || r.WeeklyUsedPct != nil {
			t.Fatal("timeout must be null")
		}
	}
	if report.Accounts[0].Source == nil || !strings.HasPrefix(*report.Accounts[0].Source, "live claude") {
		t.Fatal("live provenance")
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "private") {
		t.Fatal("raw error escaped")
	}
}

func TestClaudeSignedOutTransport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, tc := range []struct {
		output string
		exit   string
		ok     bool
	}{
		{`{"loggedIn":false,"diagnostic":"private diagnostic"}`, "1", true},
		{`{"loggedIn":true}`, "1", false},
		{`{"loggedIn":false}`, "2", false},
		{"", "1", false},
		{"private diagnostic", "1", false},
		{`{}`, "1", false},
		{`{"loggedIn":null}`, "1", false},
		{`{"loggedIn":false} trailing`, "1", false},
	} {
		script := "#!/bin/sh\nprintf '%s' '" + tc.output + "'\nprintf '%s' 'private diagnostic' >&2\nexit " + tc.exit + "\n"
		if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		source := Sources{"claude": Claude{Runner: Commands{}, Home: dir, Env: []string{"PATH=" + dir}}}
		report, code := Collect(context.Background(), source, provider.NewRegistry(nil), time.Second, time.Now())
		row := report.Accounts[6]
		if tc.ok {
			if code != 0 || row.LoginOK == nil || *row.LoginOK || row.Source == nil || row.Error == nil || *row.Error != "login-only source; quota not reported" {
				t.Fatal("signed-out plain login must be a successful source read")
			}
			if report.Accounts[0].Error == nil || *report.Accounts[0].Error != "quota unavailable; no safe noninteractive Claude usage source (only interactive /usage)" {
				t.Fatal("profile quota limitation")
			}
		} else if code != 1 || row.LoginOK != nil || row.Source != nil {
			t.Fatal("invalid signed-out result accepted")
		}
		raw, _ := json.Marshal(report)
		if strings.Contains(string(raw), "private diagnostic") {
			t.Fatal("raw diagnostics reflected")
		}
	}
}

func TestCodexExtraResetSydneyDST(t *testing.T) {
	for _, tc := range []struct{ utc, want string }{
		{"2026-04-04T15:59:59Z", "2026-04-05T02:59:59+11:00"},
		{"2026-04-04T16:00:00Z", "2026-04-05T02:00:00+10:00"},
		{"2026-10-03T15:59:59Z", "2026-10-04T01:59:59+10:00"},
		{"2026-10-03T16:00:00Z", "2026-10-04T03:00:00+11:00"},
	} {
		reset, _ := time.Parse(time.RFC3339, tc.utc)
		payload, _ := json.Marshal(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 7, "windowDurationMins": 60, "resetsAt": reset.Unix()}}})
		runner := readFunc(func(context.Context, Operation, []string) ([]byte, error) { return payload, nil })
		report, code := Collect(context.Background(), Sources{"codex": Codex{Runner: runner, Home: "/fake"}}, provider.NewRegistry(nil), time.Second, time.Now())
		bucket := report.Accounts[2].Extra["60_minute"].(map[string]any)
		if code != 0 || bucket["resets_at"] != tc.want {
			t.Fatal("extra reset DST conversion", bucket)
		}
	}
}

func TestClaudeConfiguredUsageEnv(t *testing.T) {
	home := t.TempDir()
	reg := provider.NewRegistry([]provider.Spec{{Account: provider.Account{Tool: "claude", Profile: "work"}, ConfigDir: "~/profile"}})
	called := false
	runner := readFunc(func(_ context.Context, op Operation, env []string) ([]byte, error) {
		called = true
		if op != ClaudeLogin {
			t.Fatal("unexpected usage operation")
		}
		found := false
		for _, value := range env {
			if value == "CLAUDE_CONFIG_DIR="+filepath.Join(home, "profile") {
				found = true
			}
			for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_OAUTH_TOKEN_1", "CLAUDE_OAUTH_TOKEN_2"} {
				if strings.HasPrefix(value, key+"=") {
					t.Fatal("token variable retained")
				}
			}
		}
		if !found {
			t.Fatal("config_dir not mapped")
		}
		return []byte(`{"loggedIn":true,"ignored":"private diagnostic"}`), nil
	})
	data, err := (Claude{Registry: reg, Runner: runner, Home: home, Env: []string{"CLAUDE_OAUTH_TOKEN_1=fixture"}}).Read(context.Background(), "claude", "work")
	if err != nil || !called || data.LoginOK == nil || !*data.LoginOK || data.Weekly != nil || data.Five != nil || data.Notice != "quota unavailable; no safe noninteractive Claude usage source (only interactive /usage)" {
		t.Fatal("configured usage result")
	}
}

func TestClaudeStatuslineSnapshot(t *testing.T) {
	home := t.TempDir()
	snapDir := filepath.Join(home, "snapshots")
	raw := []byte(`{"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1800000100},"seven_day":{"used_percentage":41.2,"resets_at":1800000000}}}`)
	d, err := ParseStatusline(raw, time.Now())
	if err != nil || *d.Five != 23.5 || *d.Weekly != 41.2 || d.FiveReset == nil || d.WeeklyReset == nil {
		t.Fatal("ParseStatusline failed", d, err)
	}
	os.MkdirAll(snapDir, 0o700)
	b, _ := json.Marshal(d)
	os.WriteFile(filepath.Join(snapDir, "claude-work.json"), b, 0o600)

	runner := readFunc(func(context.Context, Operation, []string) ([]byte, error) {
		return []byte(`{"loggedIn":false}`), nil
	})
	got, err := (Claude{Registry: provider.NewRegistry(nil), Runner: runner, Home: home, SnapshotDir: snapDir}).Read(context.Background(), "claude", "work")
	if err != nil || got.Source != "claude statusline snapshot" || *got.Weekly != 41.2 || *got.Five != 23.5 || got.Notice != "" {
		t.Fatalf("expected statusline snapshot merge, got %+v err=%v", got, err)
	}
}

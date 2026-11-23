package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"switchboard/internal/provider"
)

// Operation is a closed read-only allowlist, separate from task execution.
type Operation uint8

const (
	CodexLimits Operation = iota
	ClaudeLogin
	GrokUsage
	AgyUsage
)

type Runner interface {
	Read(context.Context, Operation, []string) ([]byte, error)
}
type Commands struct{}

func (Commands) Read(ctx context.Context, op Operation, env []string) ([]byte, error) {
	var name string
	var args []string
	switch op {
	case CodexLimits:
		name, args = "codex", []string{"-s", "read-only", "app-server"}
	case ClaudeLogin:
		name, args = "claude", []string{"auth", "status", "--json"}
	case GrokUsage:
		name, args = "grok", []string{"usage", "--json"}
	case AgyUsage:
		// Print-mode slash-command expansion of the built-in /usage; never free text.
		name, args = "agy", []string{"--output-format", "json", "--print", "/usage"}
	default:
		return nil, errors.New("unsupported operation")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if op != CodexLimits {
		var out bounded
		cmd.Stdout = &out
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if err != nil && op == ClaudeLogin && errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// Claude reports signed-out status with exit 1 and structured JSON.
			if loggedIn, parseErr := claudeLogin(out.data); parseErr == nil && !*loggedIn {
				err = nil
			}
		}
		if err != nil || out.overflow {
			return nil, errors.New("usage command failed")
		}
		return out.data, nil
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	// Always reap the child, including malformed RPC and early EOF paths.
	defer func() {
		_ = stdin.Close()
		stop := time.AfterFunc(100*time.Millisecond, func() { _ = cmd.Process.Kill() })
		_ = cmd.Wait()
		stop.Stop()
	}()
	result, err := rateRPC(stdout, stdin)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, err
}

type bounded struct {
	data     []byte
	overflow bool
}

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	left := 65536 - len(b.data)
	if n > left {
		b.overflow = true
		p = p[:left]
	}
	b.data = append(b.data, p...)
	return n, nil
}

// rateRPC writes exactly three protocol messages. No thread, turn or prompt exists.
func rateRPC(reader io.Reader, writer io.Writer) ([]byte, error) {
	enc := json.NewEncoder(writer)
	if err := enc.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "switchboard", "version": "0.1.0"}}}); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(io.LimitReader(reader, 262145))
	scanner.Buffer(make([]byte, 4096), 65536)
	consumed := 0
	for scanner.Scan() {
		consumed += len(scanner.Bytes()) + 1
		if consumed > 262144 {
			break
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			return nil, errors.New("invalid RPC response")
		}
		if msg.ID != 1 && msg.ID != 2 {
			continue
		}
		if len(msg.Error) > 0 && string(msg.Error) != "null" {
			return nil, errors.New("RPC operation failed")
		}
		if len(msg.Result) == 0 || string(msg.Result) == "null" {
			return nil, errors.New("missing RPC result")
		}
		if msg.ID == 1 {
			if err := enc.Encode(map[string]any{"method": "initialized"}); err != nil {
				return nil, err
			}
			if err := enc.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read", "params": nil}); err != nil {
				return nil, err
			}
			// Only the next response for id 2 is accepted; duplicate initialization fails.
			return readRateResult(scanner)
		}
		return nil, errors.New("unexpected RPC response")
	}
	return nil, errors.New("incomplete RPC response")
}

func readRateResult(scanner *bufio.Scanner) ([]byte, error) {
	total := 0
	for scanner.Scan() {
		total += len(scanner.Bytes()) + 1
		if total > 196608 {
			break
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			break
		}
		if msg.ID == 0 {
			continue
		}
		if msg.ID != 2 || (len(msg.Error) > 0 && string(msg.Error) != "null") || len(msg.Result) == 0 || string(msg.Result) == "null" {
			break
		}
		return msg.Result, nil
	}
	return nil, errors.New("invalid rate-limit response")
}

type Codex struct {
	Runner   Runner
	Home     string
	Env      []string
	Registry provider.Registry
}

func (s Codex) Read(ctx context.Context, _, profile string) (Data, error) {
	home := provider.CodexHome(s.Registry.Spec(provider.Account{Tool: "codex", Profile: profile}), s.Home)
	raw, err := s.Runner.Read(ctx, CodexLimits, mappedEnv(s.Env, "CODEX_HOME", home, ""))
	if err != nil {
		return Data{}, err
	}
	var payload struct {
		Limits *struct {
			Primary   *window `json:"primary"`
			Secondary *window `json:"secondary"`
		} `json:"rateLimits"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Limits == nil {
		return Data{}, errors.New("invalid rate limits")
	}
	yes := true
	d := Data{ObservedAt: time.Now(), LoginOK: &yes, Source: "live codex account/rateLimits/read", Extra: map[string]any{}}
	for _, w := range []*window{payload.Limits.Primary, payload.Limits.Secondary} {
		if w == nil {
			continue
		}
		if w.Used == nil || *w.Used < 0 || *w.Used > 100 || w.Minutes <= 0 {
			return Data{}, errors.New("invalid window")
		}
		var reset *string
		if w.Reset != nil {
			if *w.Reset < 0 || *w.Reset > 253402300799 {
				return Data{}, errors.New("invalid reset")
			}
			v := sydneyTime(time.Unix(*w.Reset, 0))
			reset = &v
		}
		switch w.Minutes {
		case 300:
			d.Five, d.FiveReset = w.Used, reset
		case 10080:
			d.Weekly, d.WeeklyReset = w.Used, reset
		default:
			bucket := map[string]any{"used_pct": *w.Used}
			if reset != nil {
				bucket["resets_at"] = *reset
			}
			d.Extra[strconv.Itoa(w.Minutes)+"_minute"] = bucket
		}
	}
	return d, nil
}

type window struct {
	Used    *float64 `json:"usedPercent"`
	Minutes int      `json:"windowDurationMins"`
	Reset   *int64   `json:"resetsAt"`
}

type Claude struct {
	Registry    provider.Registry
	Runner      Runner
	Home        string
	Env         []string
	SnapshotDir string
}

func (s Claude) Read(ctx context.Context, _, profile string) (Data, error) {
	dir := filepath.Join(s.Home, ".claude")
	if profile != "plain" {
		dir = filepath.Join(s.Home, ".local", "share", "claude-usage", profile)
	}
	spec := s.Registry.Spec(provider.Account{Tool: "claude", Profile: profile})
	if spec.ConfigDir != "" {
		dir = provider.ClaudeDir(spec, s.Home)
	}
	var snap Data
	var hasSnap bool
	if s.SnapshotDir != "" {
		if got, snapErr := (Snapshot{Dir: s.SnapshotDir}).Read(ctx, "claude", profile); snapErr == nil && valid(got, time.Now()) && (got.Weekly != nil || got.Five != nil) {
			snap, hasSnap = got, true
		}
	}
	env := mappedEnv(s.Env, "CLAUDE_CONFIG_DIR", dir, spec.TokenEnv)
	raw, err := s.Runner.Read(ctx, ClaudeLogin, env)
	if err != nil {
		if hasSnap {
			return snapshotData(snap), nil
		}
		return Data{}, err
	}
	loggedIn, err := claudeLogin(raw)
	if err != nil {
		if hasSnap {
			return snapshotData(snap), nil
		}
		return Data{}, err
	}
	if hasSnap {
		d := snapshotData(snap)
		if *loggedIn {
			d.LoginOK = loggedIn
		}
		return d, nil
	}
	notice := "quota unavailable; no safe noninteractive Claude usage source (only interactive /usage)"
	if profile == "plain" {
		notice = "login-only source; quota not reported"
	}
	return Data{ObservedAt: time.Now(), LoginOK: loggedIn, Source: "live claude auth status (isolated config)", Notice: notice}, nil
}

func snapshotData(snap Data) Data {
	age := int64(time.Since(snap.ObservedAt).Seconds())
	if age < 0 {
		age = 0
	}
	yes := true
	login := snap.LoginOK
	if login == nil {
		login = &yes
	}
	return Data{
		ObservedAt:  snap.ObservedAt,
		LoginOK:     login,
		Weekly:      snap.Weekly,
		WeeklyReset: snap.WeeklyReset,
		Five:        snap.Five,
		FiveReset:   snap.FiveReset,
		Source:      "claude statusline snapshot",
		Extra: map[string]any{
			"observed_at": sydneyTime(snap.ObservedAt),
			"age_seconds": age,
		},
	}
}

func claudeLogin(raw []byte) (*bool, error) {
	var v struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(raw, &v) != nil || v.LoggedIn == nil {
		return nil, errors.New("invalid login response")
	}
	return v.LoggedIn, nil
}

func mappedEnv(env []string, key, value string, extraStrip ...string) []string {
	strip := map[string]bool{
		key:                       true,
		"CLAUDE_CODE_OAUTH_TOKEN": true,
		"ANTHROPIC_API_KEY":       true,
		"CLAUDE_OAUTH_TOKEN_1":    true,
		"CLAUDE_OAUTH_TOKEN_2":    true,
	}
	for _, s := range extraStrip {
		if s != "" {
			strip[s] = true
		}
	}
	out := []string{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if !strip[k] {
			out = append(out, v)
		}
	}
	return append(out, key+"="+value)
}

var (
	errGrokUnavailable = errors.New("Grok read-only usage unavailable; check the genuine CLI service")
	errGrokSchema      = errors.New("Grok usage schema unverified; quota unavailable")
)

type Grok struct {
	Runner   Runner
	Env      []string
	Home     string
	Registry provider.Registry
}

func (s Grok) Read(ctx context.Context, _, profile string) (Data, error) {
	env := s.Env
	if s.Registry != nil {
		spec := s.Registry.Spec(provider.Account{Tool: "grok", Profile: profile})
		if spec.Home != "" {
			if mapped, err := provider.Env(spec, s.Home, s.Env); err == nil {
				env = mapped
			}
		}
	}
	_, err := s.Runner.Read(ctx, GrokUsage, env)
	if err != nil {
		if ctx.Err() != nil {
			return Data{}, ctx.Err()
		}
		return Data{}, errGrokUnavailable
	}
	// No verified output schema is available yet; never guess quota fields.
	return Data{}, errGrokSchema
}

type Unsupported struct{}

func (Unsupported) Read(context.Context, string, string) (Data, error) {
	return Data{}, errors.New("unsupported safe source")
}

// Sources dispatches independently timed sources without routing/status/classifier dependencies.
type Sources map[string]Source

func (s Sources) Read(ctx context.Context, tool, profile string) (Data, error) {
	source := s[tool]
	if source == nil {
		source = Unsupported{}
	}
	return source.Read(ctx, tool, profile)
}

func (s Sources) Timeout(tool string) time.Duration {
	if t, ok := s[tool].(Timed); ok {
		return t.Timeout(tool)
	}
	return 0
}

func lookupEnvValue(env []string, key string) string {
	for _, v := range env {
		k, x, ok := strings.Cut(v, "=")
		if ok && k == key {
			return x
		}
	}
	return ""
}

func Live(home string, env []string, reg provider.Registry) Sources {
	runner := Commands{}
	snapDir := SnapshotDir(home, lookupEnvValue(env, "XDG_STATE_HOME"))
	return Sources{"codex": Codex{runner, home, env, reg}, "claude": Claude{Registry: reg, Runner: runner, Home: home, Env: env, SnapshotDir: snapDir}, "grok": Grok{Runner: runner, Env: env, Home: home, Registry: reg}, "agy": Agy{runner, home, env, reg}}
}

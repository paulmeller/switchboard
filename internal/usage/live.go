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
	Runner Runner
	Home   string
	Env    []string
}

func (s Codex) Read(ctx context.Context, _, profile string) (Data, error) {
	home := filepath.Join(s.Home, ".codex")
	if profile == "personal" {
		home = filepath.Join(s.Home, ".codex-personal")
	}
	raw, err := s.Runner.Read(ctx, CodexLimits, mappedEnv(s.Env, "CODEX_HOME", home))
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
	Runner Runner
	Home   string
	Env    []string
}

func (s Claude) Read(ctx context.Context, _, profile string) (Data, error) {
	dir := filepath.Join(s.Home, ".claude")
	if profile != "plain" {
		dir = filepath.Join(s.Home, ".local", "share", "claude-usage", profile)
	}
	env := mappedEnv(s.Env, "CLAUDE_CONFIG_DIR", dir)
	raw, err := s.Runner.Read(ctx, ClaudeLogin, env)
	if err != nil {
		return Data{}, err
	}
	loggedIn, err := claudeLogin(raw)
	if err != nil {
		return Data{}, err
	}
	notice := "quota unavailable; no verified noninteractive quota command"
	if profile == "plain" {
		notice = "login-only source; quota not reported"
	}
	return Data{ObservedAt: time.Now(), LoginOK: loggedIn, Source: "live claude auth status (isolated config)", Notice: notice}, nil
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

func mappedEnv(env []string, key, value string) []string {
	out := []string{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if k != key && k != "CLAUDE_CODE_OAUTH_TOKEN" && k != "ANTHROPIC_API_KEY" && k != "CLAUDE_OAUTH_TOKEN_1" && k != "CLAUDE_OAUTH_TOKEN_2" {
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
	Runner Runner
	Env    []string
}

func (s Grok) Read(ctx context.Context, _, _ string) (Data, error) {
	_, err := s.Runner.Read(ctx, GrokUsage, s.Env)
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

func Live(home string, env []string) Sources {
	runner := Commands{}
	return Sources{"codex": Codex{runner, home, env}, "claude": Claude{runner, home, env}, "grok": Grok{runner, env}, "agy": Agy{runner, home, env}}
}

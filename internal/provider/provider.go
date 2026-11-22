// Package provider contains launch mapping and a narrowly allowlisted status probe.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

type Account struct {
	Tool    string `json:"tool"`
	Profile string `json:"profile"`
}

// Accounts returns the four built-in Claude/Codex launch accounts. Antigravity
// accounts come from configuration through a Registry.
func Accounts() []Account {
	return []Account{{"claude", "work"}, {"claude", "personal"}, {"codex", "work"}, {"codex", "personal"}}
}
func (a Account) ID() string { return a.Tool + "/" + a.Profile }

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Valid is a syntactic check for a supported tool and valid profile name.
// Whether a profile is configured is decided by a Registry.
func (a Account) Valid() bool {
	if a.Tool == "claude" && a.Profile == "plain" {
		return false
	}
	return (a.Tool == "claude" || a.Tool == "codex" || a.Tool == "agy" || a.Tool == "grok") && profileName.MatchString(a.Profile)
}

// ParseID splits tool/profile and applies the syntactic Valid check only.
func ParseID(s string) (Account, bool) {
	t, p, ok := strings.Cut(s, "/")
	a := Account{Tool: t, Profile: p}
	return a, ok && a.Valid()
}

// Spec is a launch account with its configuration.
type Spec struct {
	Account
	// Label is a safe configured display name, never an email or identity.
	Label string
	// Plan is an optional configured plan label for usage reporting.
	Plan string
	// Home is the profile HOME (for agy) or CODEX_HOME (for codex). A leading
	// "~/" expands against the process home at use time.
	Home string
	// QuotaPool is an opaque id; accounts sharing it share one allowance.
	QuotaPool string
	// ConfigDir selects an isolated Claude sign-in directory; ~/ expands at use time.
	ConfigDir string
	// TokenEnv names an environment variable supplying a Claude OAuth token.
	TokenEnv string
}

// DefaultAgy is the implicit Antigravity account when none is configured.
func DefaultAgy() Spec {
	return Spec{Account: Account{"agy", "personal"}, Label: "Antigravity"}
}

// DefaultGrok is the implicit Grok account when none is configured.
func DefaultGrok() Spec {
	return Spec{Account: Account{"grok", "cli"}, Label: "Grok"}
}

// Registry is the ordered set of launch accounts: configured accounts for each
// tool (claude, codex, agy, grok) in config order, or that tool's implicit
// defaults when none are configured for it.
type Registry []Spec

func NewRegistry(specs []Spec) Registry {
	byTool := map[string][]Spec{}
	for _, s := range specs {
		byTool[s.Tool] = append(byTool[s.Tool], s)
	}
	if len(byTool["claude"]) == 0 {
		byTool["claude"] = []Spec{
			{Account: Account{"claude", "work"}},
			{Account: Account{"claude", "personal"}},
		}
	}
	if len(byTool["codex"]) == 0 {
		byTool["codex"] = []Spec{
			{Account: Account{"codex", "work"}},
			{Account: Account{"codex", "personal"}},
		}
	}
	if len(byTool["agy"]) == 0 {
		byTool["agy"] = []Spec{DefaultAgy()}
	}
	if len(byTool["grok"]) == 0 {
		byTool["grok"] = []Spec{DefaultGrok()}
	}
	r := make(Registry, 0, len(byTool["claude"])+len(byTool["codex"])+len(byTool["agy"])+len(byTool["grok"]))
	r = append(r, byTool["claude"]...)
	r = append(r, byTool["codex"]...)
	r = append(r, byTool["agy"]...)
	r = append(r, byTool["grok"]...)
	return r
}

func (r Registry) Accounts() []Account {
	out := make([]Account, 0, len(r))
	for _, s := range r {
		out = append(out, s.Account)
	}
	return out
}

// Spec returns the configured spec, or a bare spec when the account is unknown.
func (r Registry) Spec(a Account) Spec {
	if s, ok := r.Lookup(a); ok {
		return s
	}
	return Spec{Account: a}
}

func (r Registry) Lookup(a Account) (Spec, bool) {
	for _, s := range r {
		if s.Account == a {
			return s, true
		}
	}
	return Spec{}, false
}

// IDs lists configured account ids in order, for actionable errors.
func (r Registry) IDs() []string {
	out := make([]string, 0, len(r))
	for _, s := range r {
		out = append(out, s.ID())
	}
	return out
}

// Parse accepts only a configured full tool/profile id.
func (r Registry) Parse(s string) (Account, error) {
	a, _ := ParseID(s)
	if _, ok := r.Lookup(a); !ok {
		return a, errors.New("account must be a configured id: " + strings.Join(r.IDs(), ", "))
	}
	return a, nil
}

// Auto reports whether automatic (usage-based) selection may pick the account.
// Within a shared quota pool only the first configured member, the pool's
// representative, is eligible; other members are used only when pinned.
func (r Registry) Auto(a Account) bool {
	s, ok := r.Lookup(a)
	if !ok {
		return false
	}
	if s.QuotaPool == "" {
		return true
	}
	for _, other := range r {
		if other.QuotaPool == s.QuotaPool {
			return other.Account == a
		}
	}
	return true
}

// ExpandHome resolves "~" or "~/" against home, leaving other paths unchanged.
func ExpandHome(path, home string) string {
	switch {
	case path == "", path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	}
	return path
}

// AgyHome is the HOME the genuine agy binary sees for an Antigravity account:
// the configured profile home, or the process home when none is configured.
func AgyHome(s Spec, home string) string {
	return ExpandHome(s.Home, home)
}

// GrokHome is the HOME the genuine grok binary sees for a Grok account:
// the configured profile home, or the process home when none is configured.
func GrokHome(s Spec, home string) string {
	return ExpandHome(s.Home, home)
}

func isolateHome(values map[string]string, profile, home string) {
	if values["AGY_REAL_HOME"] == "" {
		values["AGY_REAL_HOME"] = values["HOME"]
		if values["AGY_REAL_HOME"] == "" {
			values["AGY_REAL_HOME"] = home
		}
	}
	values["HOME"] = profile
	values["XDG_CONFIG_HOME"] = filepath.Join(profile, ".config")
	values["XDG_DATA_HOME"] = filepath.Join(profile, ".local", "share")
	values["XDG_CACHE_HOME"] = filepath.Join(profile, ".cache")
	values["XDG_STATE_HOME"] = filepath.Join(profile, ".local", "state")
}

// ClaudeDir expands the configured Claude directory without reading its contents.
func ClaudeDir(s Spec, home string) string {
	if s.ConfigDir == "" {
		return ""
	}
	return ExpandHome(s.ConfigDir, home)
}

// AgyLoginMarker is the path pattern whose existence (never contents) indicates
// a stored Antigravity sign-in for the account HOME.
func AgyLoginMarker(accountHome string) string {
	return filepath.Join(accountHome, ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

func Home(home, profile string) string {
	switch profile {
	case "", "work":
		return filepath.Join(home, ".codex")
	case "personal":
		return filepath.Join(home, ".codex-personal")
	default:
		return filepath.Join(home, ".codex-"+profile)
	}
}

// CodexHome returns the CODEX_HOME directory for a Codex spec, expanding ~/
// when Home is configured, or falling back to ~/.codex[-<profile>].
func CodexHome(s Spec, home string) string {
	if s.Home != "" {
		return AgyHome(s, home)
	}
	return Home(home, s.Profile)
}

func Env(s Spec, home string, env []string) ([]string, error) {
	a := s.Account
	if !a.Valid() {
		return nil, errors.New("invalid account")
	}
	values := map[string]string{}
	for _, v := range env {
		k, x, ok := strings.Cut(v, "=")
		if ok {
			values[k] = x
		}
	}
	if a.Tool == "claude" {
		if s.ConfigDir != "" {
			values["CLAUDE_CONFIG_DIR"] = ClaudeDir(s, home)
			for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_OAUTH_TOKEN_1", "CLAUDE_OAUTH_TOKEN_2"} {
				delete(values, k)
			}
			if s.TokenEnv != "" {
				delete(values, s.TokenEnv)
			}
		} else {
			key := s.TokenEnv
			if key == "" {
				switch a.Profile {
				case "work":
					key = "CLAUDE_OAUTH_TOKEN_1"
				case "personal":
					key = "CLAUDE_OAUTH_TOKEN_2"
				}
			}
			if key == "" {
				return nil, fmt.Errorf("Claude credentials missing; set config_dir or token_env for claude/%s in [[tool_accounts]]", a.Profile)
			}
			if values[key] == "" {
				return nil, fmt.Errorf("Claude credentials missing; set config_dir for claude/%s in [[tool_accounts]] or export %s", a.Profile, key)
			}
			values["CLAUDE_CODE_OAUTH_TOKEN"] = values[key]
			delete(values, "ANTHROPIC_API_KEY")
			delete(values, "CLAUDE_CONFIG_DIR")
		}
	} else if a.Tool == "agy" {
		if s.Home != "" {
			// Mirror the isolated-profile launcher: agy derives all state from HOME/XDG.
			isolateHome(values, AgyHome(s, home), home)
		}
	} else if a.Tool == "grok" {
		if s.Home != "" {
			isolateHome(values, GrokHome(s, home), home)
		}
	} else {
		values["CODEX_HOME"] = CodexHome(s, home)
	}
	out := make([]string, 0, len(values))
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out, nil
}

type Runner func(string, []string, []string) (int, error)

func Run(path string, args, env []string) (int, error) {
	c := exec.Command(path, args...)
	c.Env, c.Stdin, c.Stdout, c.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err := c.Start(); err != nil {
		return 1, err
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case sig := <-signals:
				if sig == syscall.SIGTERM || sig == syscall.SIGHUP {
					_ = c.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	err := c.Wait()
	close(done)
	<-stopped
	if err == nil {
		return 0, nil
	}
	if e, ok := err.(*exec.ExitError); ok {
		if status, ok := e.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return e.ExitCode(), nil
	}
	return 1, err
}

func Launch(s Spec, args []string, home string, env []string, dry bool, run Runner) (int, error) {
	a := s.Account
	mapped, err := Env(s, home, env)
	if err != nil {
		return 1, err
	}
	path, err := exec.LookPath(a.Tool)
	if err != nil {
		return 1, errors.New("tool missing on PATH; install the genuine CLI")
	}
	if dry {
		return 0, nil
	}
	if a.Tool == "claude" && s.ConfigDir != "" {
		dir := ClaudeDir(s, home)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return 1, fmt.Errorf("Claude config_dir missing or not a directory; sign in with CLAUDE_CONFIG_DIR=%s claude", dir)
		}
	}
	if a.Tool == "codex" {
		if err = os.MkdirAll(CodexHome(s, home), 0o700); err != nil {
			return 1, errors.New("cannot create Codex home")
		}
	}
	if (a.Tool == "agy" || a.Tool == "grok") && s.Home != "" {
		defer syscall.Umask(syscall.Umask(0o077))
	}
	if run == nil {
		run = Run
	}
	code, err := run(path, args, mapped)
	if err != nil {
		return 1, errors.New("cannot start tool")
	}
	return code, nil
}

// TaskArgs maps a routed task to the tool's interactive initial-prompt argv.
func TaskArgs(a Account, task string) []string {
	if a.Tool == "agy" {
		return []string{"--prompt-interactive", task}
	}
	return []string{task}
}

// HeadlessArgs maps a routed task to the tool's non-interactive/print argv.
func HeadlessArgs(a Account, task string) []string {
	switch a.Tool {
	case "claude", "grok":
		return []string{"-p", task}
	case "codex":
		return []string{"exec", task}
	case "agy":
		return []string{"--print", task}
	default:
		return []string{task}
	}
}

// Probe has no general command or prompt input. Implementations only receive account identities.
type Probe interface {
	Status(context.Context, Spec) Status
}
type Status struct {
	Account
	LoginOK *bool   `json:"login_ok"`
	Error   *string `json:"error"`
}
type Live struct {
	Home    string
	Env     []string
	Timeout time.Duration
}
type limited struct{ bytes.Buffer }

func (b *limited) Write(p []byte) (int, error) {
	n := len(p)
	left := 65536 - b.Len()
	if left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func (l Live) Status(ctx context.Context, spec Spec) Status {
	a := spec.Account
	s := Status{Account: a}
	fail := func(msg string) Status { s.Error = &msg; return s }
	env, err := Env(spec, l.Home, l.Env)
	if err != nil {
		return fail("mapped environment unavailable")
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if a.Tool == "grok" {
		if _, err := exec.LookPath("grok"); err != nil {
			return fail("tool missing on PATH; install the genuine CLI")
		}
		info, err := os.Stat(filepath.Join(GrokHome(spec, l.Home), ".grok", "auth.json"))
		ok := err == nil && info.Mode().IsRegular()
		if err != nil && !os.IsNotExist(err) {
			return fail("sign-in marker unreadable; check the profile directory")
		}
		s.LoginOK = &ok
		return s
	}
	if a.Tool == "agy" {
		// No prompt or subprocess: binary presence plus stored sign-in marker
		// existence (stat only, contents never opened). Not a credit claim.
		if _, err := exec.LookPath("agy"); err != nil {
			return fail("tool missing on PATH; install the genuine CLI")
		}
		accountHome := AgyHome(spec, l.Home)
		markers := []string{
			AgyLoginMarker(accountHome),
			filepath.Join(accountHome, ".gemini", "antigravity-cli", "jetski_state.pbtxt"),
		}
		ok := false
		for _, marker := range markers {
			info, err := os.Stat(marker)
			if err == nil && info.Mode().IsRegular() {
				ok = true
				break
			}
			if err != nil && !os.IsNotExist(err) {
				return fail("sign-in marker unreadable; check the profile directory")
			}
		}
		s.LoginOK = &ok
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"auth", "status", "--json"}
	if a.Tool == "codex" {
		args = []string{"login", "status"}
	}
	c := exec.CommandContext(ctx, a.Tool, args...)
	c.WaitDelay = time.Second
	c.Env = env
	var out, errOut limited
	c.Stdout = &out
	c.Stderr = &errOut
	err = c.Run()
	if ctx.Err() != nil {
		return fail("status timed out")
	}
	if a.Tool == "claude" {
		var v struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		if json.Unmarshal(out.Bytes(), &v) == nil && v.LoggedIn != nil {
			s.LoginOK = v.LoggedIn
			return s
		}
	} else {
		// Only exact official markers are accepted; raw diagnostics are always discarded.
		for _, text := range []string{out.String(), errOut.String()} {
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "Logged in using ChatGPT" || line == "Not logged in" {
					v := line == "Logged in using ChatGPT"
					s.LoginOK = &v
					return s
				}
			}
		}
	}
	if err != nil {
		return fail("status failed; check login with the genuine CLI")
	}
	return fail("unrecognized status response")
}

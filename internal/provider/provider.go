// Package provider contains launch mapping and a narrowly allowlisted status probe.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// Valid is a syntactic check: built-in Claude/Codex profiles, or an agy profile
// name. Whether an agy profile is configured is decided by a Registry.
func (a Account) Valid() bool {
	if a.Tool == "agy" {
		return profileName.MatchString(a.Profile)
	}
	return (a.Tool == "claude" || a.Tool == "codex") && (a.Profile == "work" || a.Profile == "personal")
}

// ParseID splits tool/profile and applies the syntactic Valid check only.
func ParseID(s string) (Account, bool) {
	t, p, ok := strings.Cut(s, "/")
	a := Account{Tool: t, Profile: p}
	return a, ok && a.Valid()
}

// Spec is a launch account with its configuration. Label, Home and QuotaPool
// apply to Antigravity accounts only.
type Spec struct {
	Account
	// Label is a safe configured display name, never an email or identity.
	Label string
	// Home is the agy profile HOME; empty keeps the process HOME. A leading
	// "~/" expands against the process home at use time.
	Home string
	// QuotaPool is an opaque id; accounts sharing it share one allowance.
	QuotaPool string
}

// DefaultAgy is the implicit Antigravity account when none is configured.
func DefaultAgy() Spec {
	return Spec{Account: Account{"agy", "personal"}, Label: "Antigravity"}
}

// Registry is the ordered set of launch accounts: built-in Claude/Codex first,
// then configured Antigravity accounts (or the implicit default).
type Registry []Spec

func NewRegistry(agy []Spec) Registry {
	r := Registry{}
	for _, a := range Accounts() {
		r = append(r, Spec{Account: a})
	}
	if len(agy) == 0 {
		agy = []Spec{DefaultAgy()}
	}
	return append(r, agy...)
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

// AgyHome is the HOME the genuine agy binary sees for an Antigravity account:
// the configured profile home, or the process home when none is configured.
func AgyHome(s Spec, home string) string {
	switch {
	case s.Home == "":
		return home
	case s.Home == "~":
		return home
	case strings.HasPrefix(s.Home, "~/"):
		return filepath.Join(home, s.Home[2:])
	}
	return s.Home
}

// AgyLoginMarker is the path pattern whose existence (never contents) indicates
// a stored Antigravity sign-in for the account HOME.
func AgyLoginMarker(accountHome string) string {
	return filepath.Join(accountHome, ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

func Home(home, profile string) string {
	if profile == "personal" {
		return filepath.Join(home, ".codex-personal")
	}
	return filepath.Join(home, ".codex")
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
		key := "CLAUDE_OAUTH_TOKEN_1"
		if a.Profile == "personal" {
			key = "CLAUDE_OAUTH_TOKEN_2"
		}
		if values[key] == "" {
			return nil, errors.New("mapped Claude token missing; export the profile variable")
		}
		values["CLAUDE_CODE_OAUTH_TOKEN"] = values[key]
		delete(values, "ANTHROPIC_API_KEY")
	} else if a.Tool == "agy" {
		if s.Home != "" {
			// Mirror the isolated-profile launcher: agy derives all state from HOME/XDG.
			profile := AgyHome(s, home)
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
	} else {
		values["CODEX_HOME"] = Home(home, a.Profile)
	}
	out := make([]string, 0, len(values))
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	return out, nil
}

type Executor func(string, []string, []string) error

func Exec(path string, args, env []string) error {
	return syscall.Exec(path, append([]string{path}, args...), env)
}

func Launch(s Spec, args []string, home string, env []string, dry bool, execute Executor) error {
	a := s.Account
	mapped, err := Env(s, home, env)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(a.Tool)
	if err != nil {
		return errors.New("tool missing on PATH; install the genuine CLI")
	}
	if dry {
		return nil
	}
	if a.Tool == "codex" {
		if err = os.MkdirAll(Home(home, a.Profile), 0o700); err != nil {
			return errors.New("cannot create Codex home")
		}
	}
	if a.Tool == "agy" && s.Home != "" {
		// Match the profile launcher's umask 077; restored only if exec returns.
		defer syscall.Umask(syscall.Umask(0o077))
	}
	if execute(path, args, mapped) != nil {
		return errors.New("cannot exec tool")
	}
	return nil
}

// TaskArgs maps a routed task to the tool's interactive initial-prompt argv.
func TaskArgs(a Account, task string) []string {
	if a.Tool == "agy" {
		return []string{"--prompt-interactive", task}
	}
	return []string{task}
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
	if a.Tool == "agy" {
		// No prompt or subprocess: binary presence plus stored sign-in marker
		// existence (stat only, contents never opened). Not a credit claim.
		if _, err := exec.LookPath("agy"); err != nil {
			return fail("tool missing on PATH; install the genuine CLI")
		}
		info, err := os.Stat(AgyLoginMarker(AgyHome(spec, l.Home)))
		ok := err == nil && info.Mode().IsRegular()
		if err != nil && !os.IsNotExist(err) {
			return fail("sign-in marker unreadable; check the profile directory")
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
	var b limited
	c.Stdout = &b
	c.Stderr = &b
	err = c.Run()
	if ctx.Err() != nil {
		return fail("status timed out")
	}
	if a.Tool == "claude" {
		var v struct {
			LoggedIn *bool `json:"loggedIn"`
		}
		if json.Unmarshal(b.Bytes(), &v) == nil && v.LoggedIn != nil {
			s.LoginOK = v.LoggedIn
			return s
		}
	} else {
		// Only exact official markers are accepted; raw diagnostics are always discarded.
		for _, line := range strings.Split(b.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "Logged in using ChatGPT" || line == "Not logged in" {
				v := line == "Logged in using ChatGPT"
				s.LoginOK = &v
				return s
			}
		}
	}
	if err != nil {
		return fail("status failed; check login with the genuine CLI")
	}
	return fail("unrecognized status response")
}

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
	"strings"
	"syscall"
	"time"
)

type Account struct {
	Tool    string `json:"tool"`
	Profile string `json:"profile"`
}

func Accounts() []Account {
	return []Account{{"claude", "work"}, {"claude", "personal"}, {"codex", "work"}, {"codex", "personal"}}
}
func (a Account) ID() string { return a.Tool + "/" + a.Profile }
func (a Account) Valid() bool {
	return (a.Tool == "claude" || a.Tool == "codex") && (a.Profile == "work" || a.Profile == "personal")
}

func Home(home, profile string) string {
	if profile == "personal" {
		return filepath.Join(home, ".codex-personal")
	}
	return filepath.Join(home, ".codex")
}

func Env(a Account, home string, env []string) ([]string, error) {
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

func Launch(a Account, args []string, home string, env []string, dry bool, execute Executor) error {
	mapped, err := Env(a, home, env)
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
	if execute(path, args, mapped) != nil {
		return errors.New("cannot exec tool")
	}
	return nil
}

// Probe has no general command or prompt input. Implementations only receive account identities.
type Probe interface {
	Status(context.Context, Account) Status
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

func (l Live) Status(ctx context.Context, a Account) Status {
	s := Status{Account: a}
	fail := func(msg string) Status { s.Error = &msg; return s }
	env, err := Env(a, l.Home, l.Env)
	if err != nil {
		return fail("mapped environment unavailable")
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
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

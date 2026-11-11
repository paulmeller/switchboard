package provider

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func lookup(env []string, k string) string {
	for _, v := range env {
		key, x, _ := strings.Cut(v, "=")
		if key == k {
			return x
		}
	}
	return ""
}

func TestMapping(t *testing.T) {
	for _, a := range Accounts() {
		env, e := Env(a, "/example", []string{"CLAUDE_OAUTH_TOKEN_1=fixture-work", "CLAUDE_OAUTH_TOKEN_2=fixture-personal", "ANTHROPIC_API_KEY=fixture-unused", "KEEP=yes"})
		if e != nil {
			t.Fatal(e)
		}
		if lookup(env, "KEEP") != "yes" {
			t.Fatal("environment lost")
		}
		if a.Tool == "claude" {
			if lookup(env, "ANTHROPIC_API_KEY") != "" || lookup(env, "CLAUDE_CODE_OAUTH_TOKEN") != "fixture-"+a.Profile {
				t.Fatal("incorrect token mapping")
			}
		} else if lookup(env, "CODEX_HOME") != Home("/example", a.Profile) {
			t.Fatal("incorrect home")
		}
	}
	if _, e := Env(Account{"claude", "work"}, "", nil); e == nil {
		t.Fatal("missing token accepted")
	}
}

func TestLaunch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if e := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir)
	home := t.TempDir()
	called := false
	executor := func(path string, args, env []string) error {
		called = true
		if path != bin || !reflect.DeepEqual(args, []string{"--version", "a b", "--x"}) || lookup(env, "CODEX_HOME") != Home(home, "personal") {
			t.Fatal("exec parity")
		}
		return nil
	}
	a := Account{"codex", "personal"}
	args := []string{"--version", "a b", "--x"}
	if Launch(a, args, home, nil, true, executor) != nil || called {
		t.Fatal("dry run executed")
	}
	if _, e := os.Stat(Home(home, "personal")); !os.IsNotExist(e) {
		t.Fatal("dry run created home")
	}
	if e := Launch(a, args, home, nil, false, executor); e != nil || !called {
		t.Fatal("launch failed")
	}
}

func TestStatusAllowlist(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$*\" != 'login status' ]; then exit 99; fi\nprintf 'Logged in using ChatGPT\\n'\n"
	os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700)
	t.Setenv("PATH", dir)
	s := (Live{Home: t.TempDir(), Timeout: time.Second}).Status(context.Background(), Account{"codex", "work"})
	if s.LoginOK == nil || !*s.LoginOK {
		t.Fatal("status marker rejected")
	}
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nprintf 'private diagnostic'\nexit 1\n"), 0o700)
	s = (Live{Home: t.TempDir()}).Status(context.Background(), Account{"codex", "work"})
	if s.Error == nil || strings.Contains(*s.Error, "private") {
		t.Fatal("raw error reflected")
	}
}

func TestStatusTimeoutAndClaude(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o700)
	s := (Live{Home: t.TempDir(), Timeout: time.Millisecond}).Status(context.Background(), Account{"codex", "work"})
	if s.Error == nil || *s.Error != "status timed out" {
		t.Fatal("timeout not normalized")
	}
	os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nif [ \"$*\" != 'auth status --json' ]; then exit 99; fi\nprintf '{\"loggedIn\":true,\"email\":\"discard-me\"}'\n"), 0o700)
	s = (Live{Home: t.TempDir(), Env: []string{"CLAUDE_OAUTH_TOKEN_1=fixture-work"}}).Status(context.Background(), Account{"claude", "work"})
	if s.LoginOK == nil || !*s.LoginOK || s.Error != nil {
		t.Fatal("Claude status normalization")
	}
}

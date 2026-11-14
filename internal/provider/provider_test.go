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
		} else if a.Tool == "agy" {
			if lookup(env, "CODEX_HOME") != "" || lookup(env, "CLAUDE_CODE_OAUTH_TOKEN") != "" {
				t.Fatal("agy received another tool mapping")
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

func TestAgyAccountsAndEnv(t *testing.T) {
	if len(Accounts()) != 6 || !(Account{"agy", "personal"}).Valid() || !(Account{"agy", "kelly"}).Valid() || (Account{"agy", "work"}).Valid() || (Account{"agy", "gemini"}).Valid() || (Account{"codex", "kelly"}).Valid() {
		t.Fatal("agy account registry")
	}
	if (Account{"agy", "kelly"}).Label() != "Antigravity (Kelly personal)" {
		t.Fatal("kelly label")
	}
	home := t.TempDir()
	env, err := Env(Account{"agy", "kelly"}, home, []string{"HOME=" + home, "XDG_CONFIG_HOME=/elsewhere", "KEEP=yes"})
	profile := filepath.Join(home, ".gemini-kelly")
	if err != nil || lookup(env, "HOME") != profile || lookup(env, "AGY_REAL_HOME") != home || lookup(env, "KEEP") != "yes" ||
		lookup(env, "XDG_CONFIG_HOME") != filepath.Join(profile, ".config") || lookup(env, "XDG_DATA_HOME") != filepath.Join(profile, ".local", "share") ||
		lookup(env, "XDG_CACHE_HOME") != filepath.Join(profile, ".cache") || lookup(env, "XDG_STATE_HOME") != filepath.Join(profile, ".local", "state") {
		t.Fatal("kelly isolation", err)
	}
	override := filepath.Join(home, "other")
	env, _ = Env(Account{"agy", "kelly"}, home, []string{"HOME=" + home, "AGY_REAL_HOME=/kept", AgyKellyHomeEnv + "=" + override})
	if lookup(env, "HOME") != override || lookup(env, "AGY_REAL_HOME") != "/kept" {
		t.Fatal("kelly override")
	}
	env, _ = Env(Account{"agy", "personal"}, home, []string{"HOME=" + home, "XDG_CONFIG_HOME=/mine"})
	if lookup(env, "HOME") != home || lookup(env, "XDG_CONFIG_HOME") != "/mine" || lookup(env, "AGY_REAL_HOME") != "" {
		t.Fatal("personal env rewritten")
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("env mapping created a directory")
	}
}

func TestAgyStatusMarkerExistenceOnly(t *testing.T) {
	dir := t.TempDir()
	// Any execution of the fake binary is a failure: status must not run agy.
	os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\ntouch \"$0.ran\"\nexit 9\n"), 0o700)
	t.Setenv("PATH", dir)
	home := t.TempDir()
	marker := AgyLoginMarker(AgyHome("kelly", home, nil))
	os.MkdirAll(filepath.Dir(marker), 0o700)
	os.WriteFile(marker, nil, 0o000) // unreadable on purpose: only existence is checked
	live := Live{Home: home, Env: []string{"HOME=" + home}}
	kelly := live.Status(context.Background(), Account{"agy", "kelly"})
	personal := live.Status(context.Background(), Account{"agy", "personal"})
	if kelly.LoginOK == nil || !*kelly.LoginOK || kelly.Error != nil || personal.LoginOK == nil || *personal.LoginOK {
		t.Fatal("marker existence")
	}
	if _, err := os.Stat(filepath.Join(dir, "agy.ran")); !os.IsNotExist(err) {
		t.Fatal("status executed agy")
	}
	t.Setenv("PATH", t.TempDir())
	if s := live.Status(context.Background(), Account{"agy", "kelly"}); s.LoginOK != nil || s.Error == nil {
		t.Fatal("missing binary must be unknown")
	}
}

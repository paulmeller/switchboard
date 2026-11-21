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
	for _, a := range NewRegistry(nil) {
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
	if _, e := Env(Spec{Account: Account{"claude", "work"}}, "", nil); e == nil {
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
	executor := func(path string, args, env []string) (int, error) {
		called = true
		if path != bin || !reflect.DeepEqual(args, []string{"--version", "a b", "--x"}) || lookup(env, "CODEX_HOME") != Home(home, "personal") {
			t.Fatal("exec parity")
		}
		return 0, nil
	}
	a := Spec{Account: Account{"codex", "personal"}}
	args := []string{"--version", "a b", "--x"}
	if _, err := Launch(a, args, home, nil, true, executor); err != nil || called {
		t.Fatal("dry run executed")
	}
	if _, e := os.Stat(Home(home, "personal")); !os.IsNotExist(e) {
		t.Fatal("dry run created home")
	}
	if _, e := Launch(a, args, home, nil, false, executor); e != nil || !called {
		t.Fatal("launch failed")
	}
}

func TestStatusAllowlist(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$*\" != 'login status' ]; then exit 99; fi\nprintf 'Logged in using ChatGPT\\n'\n"
	os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700)
	t.Setenv("PATH", dir)
	s := (Live{Home: t.TempDir(), Timeout: time.Second}).Status(context.Background(), Spec{Account: Account{"codex", "work"}})
	if s.LoginOK == nil || !*s.LoginOK {
		t.Fatal("status marker rejected")
	}
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nprintf 'private diagnostic'\nexit 1\n"), 0o700)
	s = (Live{Home: t.TempDir()}).Status(context.Background(), Spec{Account: Account{"codex", "work"}})
	if s.Error == nil || strings.Contains(*s.Error, "private") {
		t.Fatal("raw error reflected")
	}
}

func TestStatusTimeoutAndClaude(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o700)
	s := (Live{Home: t.TempDir(), Timeout: time.Millisecond}).Status(context.Background(), Spec{Account: Account{"codex", "work"}})
	if s.Error == nil || *s.Error != "status timed out" {
		t.Fatal("timeout not normalized")
	}
	os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nif [ \"$*\" != 'auth status --json' ]; then exit 99; fi\nprintf '{\"loggedIn\":true,\"email\":\"discard-me\"}'\n"), 0o700)
	s = (Live{Home: t.TempDir(), Env: []string{"CLAUDE_OAUTH_TOKEN_1=fixture-work"}}).Status(context.Background(), Spec{Account: Account{"claude", "work"}})
	if s.LoginOK == nil || !*s.LoginOK || s.Error != nil {
		t.Fatal("Claude status normalization")
	}
}

func fixtureRegistry(secondHome string) Registry {
	return NewRegistry([]Spec{
		{Account: Account{"agy", "work"}, Label: "Antigravity (work)"},
		{Account: Account{"agy", "second"}, Label: "Antigravity (second)", Home: secondHome, QuotaPool: "pool-a"},
		{Account: Account{"agy", "third"}, Label: "Antigravity (third)", QuotaPool: "pool-a"},
	})
}

func TestRegistry(t *testing.T) {
	def := NewRegistry(nil)
	if len(Accounts()) != 4 || len(def) != 5 || def[4].ID() != "agy/personal" || def[4].Label != "Antigravity" || def[4].Home != "" || def[4].QuotaPool != "" || !def.Auto(def[4].Account) {
		t.Fatal("implicit default agy account")
	}
	if _, err := def.Parse("agy/personal"); err != nil {
		t.Fatal("default pin rejected")
	}
	reg := fixtureRegistry("/profiles/second")
	for _, id := range []string{"agy/work", "agy/second", "agy/third", "codex/work"} {
		if a, err := reg.Parse(id); err != nil || a.ID() != id {
			t.Fatal("configured id rejected", id)
		}
	}
	for _, id := range []string{"agy/personal", "agy", "agy/", "grok/cli", "codex/second", "agy/work/x"} {
		if _, err := reg.Parse(id); err == nil {
			t.Fatal("unconfigured id accepted", id)
		}
	}
	if _, err := reg.Parse("agy/nope"); err == nil || !strings.Contains(err.Error(), "agy/work, agy/second, agy/third") {
		t.Fatal("error must list configured ids")
	}
	if !reg.Auto(Account{"agy", "work"}) || !reg.Auto(Account{"agy", "second"}) || reg.Auto(Account{"agy", "third"}) || !reg.Auto(Account{"codex", "work"}) || reg.Auto(Account{"agy", "personal"}) {
		t.Fatal("pool representative")
	}
	if (Account{"agy", "-x"}).Valid() || (Account{"agy", ""}).Valid() || (Account{"codex", "-x"}).Valid() || !(Account{"codex", "second"}).Valid() || !(Account{"claude", "client"}).Valid() || !(Account{"agy", "any_name-1"}).Valid() {
		t.Fatal("syntactic validity")
	}
}

func TestAgyEnv(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "profiles", "second")
	reg := fixtureRegistry(profile)
	second := reg.Spec(Account{"agy", "second"})
	env, err := Env(second, home, []string{"HOME=" + home, "XDG_CONFIG_HOME=/elsewhere", "KEEP=yes"})
	if err != nil || lookup(env, "HOME") != profile || lookup(env, "AGY_REAL_HOME") != home || lookup(env, "KEEP") != "yes" ||
		lookup(env, "XDG_CONFIG_HOME") != filepath.Join(profile, ".config") || lookup(env, "XDG_DATA_HOME") != filepath.Join(profile, ".local", "share") ||
		lookup(env, "XDG_CACHE_HOME") != filepath.Join(profile, ".cache") || lookup(env, "XDG_STATE_HOME") != filepath.Join(profile, ".local", "state") {
		t.Fatal("configured home isolation", err)
	}
	env, _ = Env(second, home, []string{"HOME=" + home, "AGY_REAL_HOME=/kept"})
	if lookup(env, "AGY_REAL_HOME") != "/kept" {
		t.Fatal("AGY_REAL_HOME override")
	}
	tilde := Spec{Account: Account{"agy", "x"}, Home: "~/profiles/x"}
	if AgyHome(tilde, home) != filepath.Join(home, "profiles", "x") {
		t.Fatal("tilde expansion")
	}
	for _, s := range []Spec{reg.Spec(Account{"agy", "work"}), DefaultAgy()} {
		env, _ = Env(s, home, []string{"HOME=" + home, "XDG_CONFIG_HOME=/mine"})
		if lookup(env, "HOME") != home || lookup(env, "XDG_CONFIG_HOME") != "/mine" || lookup(env, "AGY_REAL_HOME") != "" || AgyHome(s, home) != home {
			t.Fatal("account without home must keep the process env", s.ID())
		}
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
	reg := fixtureRegistry(filepath.Join(home, "profiles", "second"))
	second, work := reg.Spec(Account{"agy", "second"}), reg.Spec(Account{"agy", "work"})
	marker := AgyLoginMarker(AgyHome(second, home))
	os.MkdirAll(filepath.Dir(marker), 0o700)
	os.WriteFile(marker, nil, 0o000) // unreadable on purpose: only existence is checked
	live := Live{Home: home, Env: []string{"HOME=" + home}}
	got := live.Status(context.Background(), second)
	plain := live.Status(context.Background(), work)
	if got.LoginOK == nil || !*got.LoginOK || got.Error != nil || plain.LoginOK == nil || *plain.LoginOK {
		t.Fatal("marker existence")
	}
	// An account without home checks the marker under the process home.
	os.MkdirAll(filepath.Dir(AgyLoginMarker(home)), 0o700)
	os.WriteFile(AgyLoginMarker(home), nil, 0o000)
	if s := live.Status(context.Background(), DefaultAgy()); s.LoginOK == nil || !*s.LoginOK {
		t.Fatal("process-home marker")
	}
	if _, err := os.Stat(filepath.Join(dir, "agy.ran")); !os.IsNotExist(err) {
		t.Fatal("status executed agy")
	}
	t.Setenv("PATH", t.TempDir())
	if s := live.Status(context.Background(), second); s.LoginOK != nil || s.Error == nil {
		t.Fatal("missing binary must be unknown")
	}
}

func TestClaudeConfigEnvAndStatus(t *testing.T) {
	home := t.TempDir()
	spec := Spec{Account: Account{"claude", "work"}, ConfigDir: "~/profile"}
	env, err := Env(spec, home, []string{"KEEP=yes", "CLAUDE_CODE_OAUTH_TOKEN=fixture", "ANTHROPIC_API_KEY=fixture", "CLAUDE_OAUTH_TOKEN_1=fixture", "CLAUDE_OAUTH_TOKEN_2=fixture"})
	if err != nil || lookup(env, "CLAUDE_CONFIG_DIR") != filepath.Join(home, "profile") || lookup(env, "KEEP") != "yes" {
		t.Fatal("config environment mapping")
	}
	for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_OAUTH_TOKEN_1", "CLAUDE_OAUTH_TOKEN_2"} {
		for _, value := range env {
			if strings.HasPrefix(value, key+"=") {
				t.Fatal("token variable retained")
			}
		}
	}
	for _, profile := range []string{"work", "personal"} {
		_, err := Env(Spec{Account: Account{"claude", profile}}, home, nil)
		if err == nil || !strings.Contains(err.Error(), "set config_dir for claude/"+profile) {
			t.Fatal("missing credential guidance")
		}
	}
	bin := t.TempDir()
	script := `#!/bin/sh
[ "$*" = 'auth status --json' ] || exit 99
[ "$CLAUDE_CONFIG_DIR" = "$HOME/profile" ] || exit 98
[ "${CLAUDE_CODE_OAUTH_TOKEN+x}${ANTHROPIC_API_KEY+x}${CLAUDE_OAUTH_TOKEN_1+x}${CLAUDE_OAUTH_TOKEN_2+x}" = '' ] || exit 97
printf '%s' '{"loggedIn":true,"ignored":"private diagnostic"}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	status := (Live{Home: home, Env: []string{"HOME=" + home, "CLAUDE_OAUTH_TOKEN_1=fixture"}}).Status(context.Background(), spec)
	if status.LoginOK == nil || !*status.LoginOK || status.Error != nil {
		t.Fatal("config status failed")
	}
}

func TestChildRunnerExit(t *testing.T) {
	for _, tc := range []struct {
		script string
		code   int
	}{{"exit 7", 7}, {"kill -TERM $$", 143}} {
		path := filepath.Join(t.TempDir(), "fake-child")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		code, err := Run(path, nil, nil)
		if err != nil || code != tc.code {
			t.Fatalf("child exit: %d, %v", code, err)
		}
	}
}

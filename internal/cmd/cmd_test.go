package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"switchboard/internal/state"
	"switchboard/internal/usage"

	"switchboard/internal/provider"
)

type fakeProbe struct {
	calls  int
	signed bool
}

func (p *fakeProbe) Status(_ context.Context, a provider.Account) provider.Status {
	p.calls++
	v := p.signed
	return provider.Status{Account: a, LoginOK: &v}
}

func TestCommands(t *testing.T) {
	for _, args := range [][]string{{"usage", "--json"}, {"schema", "--json"}, {"status", "--json"}, {"--dry-run", "fake task", "--json"}, {"--dry-run", "codex", "personal", "--", "--version"}} {
		var out, err bytes.Buffer
		p := &fakeProbe{signed: true}
		a := App{Out: &out, Err: &err, Home: t.TempDir(), UsageSource: usage.Unsupported{}, Classifier: forbiddenClassifier{t}, Probe: p, Execute: func(string, []string, []string) error { t.Fatal("unexpected launch"); return nil }}
		code := a.Run(args)
		if args[0] == "usage" {
			if code != 1 || p.calls != 0 {
				t.Fatal("usage accessed status or wrong code")
			}
		} else if code != 0 {
			t.Fatalf("command %v failed: %s", args, err.String())
		}
		if args[0] == "usage" || args[0] == "schema" || args[0] == "status" {
			var doc any
			if json.Unmarshal(out.Bytes(), &doc) != nil {
				t.Fatalf("not one document: %s", out.String())
			}
		}
	}
}

func TestNoEligibleAndInvalid(t *testing.T) {
	var out, err bytes.Buffer
	a := App{Out: &out, Err: &err, Home: t.TempDir(), Probe: &fakeProbe{}}
	if code := a.Run([]string{"--dry-run", "fake task"}); code != 3 {
		t.Fatal(code)
	}
	if code := a.Run([]string{"cooldown", "invalid"}); code != 2 {
		t.Fatal(code)
	}
}

func TestLauncherArgvAndModes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o700)
	t.Setenv("PATH", dir)
	var out, diag bytes.Buffer
	calls := 0
	p := &fakeProbe{signed: true}
	a := App{Out: &out, Err: &diag, Home: t.TempDir(), Env: []string{"KEEP=yes"}, Probe: p, Execute: func(_ string, args, env []string) error {
		calls++
		if !reflect.DeepEqual(args, []string{"--version", "a,b", "space arg", "--help"}) {
			t.Fatal("argv changed")
		}
		return nil
	}}
	if code := a.Run([]string{"codex", "personal", "--", "--version", "a,b", "space arg", "--help"}); code != 0 || calls != 1 || p.calls != 0 {
		t.Fatal("explicit selection did not bypass routing")
	}
	for _, args := range [][]string{{"--version"}, {"--json", "--version"}, {"--no-input", "--version"}, {"--help"}, {"run", "--help"}, {"status", "--plain"}} {
		out.Reset()
		diag.Reset()
		if a.Run(args) != 0 {
			t.Fatalf("mode %v failed", args)
		}
	}
	out.Reset()
	if code := a.Run([]string{"status", "--json", "--plain"}); code != 2 {
		t.Fatal("conflicting output flags")
	}
}

func TestDryCooldownAndFallback(t *testing.T) {
	var out, diag bytes.Buffer
	home := t.TempDir()
	p := &fakeProbe{signed: true}
	a := App{Out: &out, Err: &diag, Home: home, Probe: p}
	if a.Run([]string{"--dry-run", "cooldown", "codex/work"}) != 0 {
		t.Fatal("dry cooldown")
	}
	path := state.Path(home, "")
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("dry cooldown wrote state")
	}
	if state.Save(path, state.State{"codex/work": time.Now().Add(time.Hour)}) != nil {
		t.Fatal("save cooldown")
	}
	out.Reset()
	if a.Run([]string{"--dry-run", "run", "fake task", "--json"}) != 0 {
		t.Fatal("fallback failed")
	}
	var v map[string]string
	json.Unmarshal(out.Bytes(), &v)
	if v["tool"] != "claude" || v["profile"] != "work" || v["reason"] != "first eligible fallback" {
		t.Fatal("wrong fallback")
	}
}

type forbiddenClassifier struct{ t *testing.T }

func (f forbiddenClassifier) Choose(context.Context, string, []provider.Account, float64) (provider.Account, bool) {
	f.t.Fatal("usage invoked classifier")
	return provider.Account{}, false
}

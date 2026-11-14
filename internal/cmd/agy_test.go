package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"switchboard/internal/classifier"
	"switchboard/internal/decision"
	"switchboard/internal/usage"
)

type choiceClassifier struct{ family string }

// toolUsage counts usage reads per tool and returns unknown usage.
type toolUsage struct {
	mu    sync.Mutex
	calls map[string]int
}

func (u *toolUsage) Read(_ context.Context, tool, _ string) (usage.Data, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.calls == nil {
		u.calls = map[string]int{}
	}
	u.calls[tool]++
	return usage.Data{}, errors.New("fake unknown")
}

func (f choiceClassifier) Choose(_ context.Context, _ string, eligible []string, _ float64) classifier.Result {
	p, c := 0.9, 0.9
	return classifier.Result{Family: f.family, Probability: &p, Confidence: &c, OK: true}
}

func TestAgyExplicitPins(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"agy", "kelly"}, "agy/kelly"},
		{[]string{"agy", "kelly", "--", "--version"}, "agy/kelly"},
		{[]string{"--account", "agy/kelly", "agy"}, "agy/kelly"},
		{[]string{"agy", "personal"}, "agy/personal"},
		{[]string{"--account=agy/personal", "agy", "--", "--help"}, "agy/personal"},
	} {
		var out, diag bytes.Buffer
		home := t.TempDir()
		p := &fakeProbe{}
		a := App{Home: home, Out: &out, Err: &diag, Probe: p, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}, Execute: func(string, []string, []string) error {
			t.Fatal("dry-run launched")
			return nil
		}}
		if a.Run(append([]string{"--dry-run", "--json"}, tc.args...)) != 0 {
			t.Fatal(diag.String())
		}
		var result map[string]string
		if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"]+"/"+result["profile"] != tc.want || p.calls != 0 {
			t.Fatal("wrong agy pin", tc.args, out.String())
		}
		records, err := decision.Read(decision.Path(home, ""))
		if err != nil || len(records) != 1 || records[0].Label == nil || records[0].Label.Account != tc.want || records[0].Family != "agy" {
			t.Fatal("agy pin decision")
		}
		onlyDecisionLog(t, home)
	}
	for _, args := range [][]string{{"--account=agy/kelly", "codex"}, {"--account=agy/kelly", "claude"}, {"--account=agy/work", "agy"}, {"--account=codex/work", "agy"}} {
		var out, diag bytes.Buffer
		a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if a.Run(append([]string{"--dry-run"}, args...)) != 2 {
			t.Fatal("mismatched agy account accepted", args)
		}
	}
}

func TestAgyFamilyBalancesBothAccounts(t *testing.T) {
	for _, tc := range []struct {
		personal, kelly float64
		want            string
	}{{80, 10, "kelly"}, {5, 60, "personal"}} {
		var out, diag bytes.Buffer
		now := time.Now()
		personal, kelly := tc.personal, tc.kelly
		src := &familyUsage{t: t, family: "agy", rows: map[string]usage.Data{
			"personal": {Source: "fake", ObservedAt: now, Weekly: &personal},
			"kelly":    {Source: "fake", ObservedAt: now, Weekly: &kelly},
		}}
		a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: forbiddenClassifier{t}, Execute: func(string, []string, []string) error {
			t.Fatal("dry-run launched")
			return nil
		}}
		if a.Run([]string{"--dry-run", "--json", "agy"}) != 0 {
			t.Fatal(diag.String())
		}
		var result map[string]string
		if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != "agy" || result["profile"] != tc.want || src.calls.Load() != 2 {
			t.Fatal("agy balance", out.String())
		}
	}
}

func TestAgyLaunchKellyIsolation(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	home := t.TempDir()
	for _, profile := range []string{"kelly", "personal"} {
		var out, diag bytes.Buffer
		var gotArgs, gotEnv []string
		a := App{Home: home, Env: []string{"HOME=" + home, "KEEP=yes"}, Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}, Execute: func(path string, args, env []string) error {
			if path != bin {
				t.Fatal("not the agy binary")
			}
			gotArgs, gotEnv = args, env
			return nil
		}}
		if a.Run([]string{"--no-log", "agy", profile, "--", "--model", "x"}) != 0 {
			t.Fatal(diag.String())
		}
		if !reflect.DeepEqual(gotArgs, []string{"--model", "x"}) || lookupEnv(gotEnv, "KEEP") != "yes" {
			t.Fatal("argv/env parity")
		}
		kellyHome := filepath.Join(home, ".gemini-kelly")
		if profile == "kelly" {
			if lookupEnv(gotEnv, "HOME") != kellyHome || lookupEnv(gotEnv, "AGY_REAL_HOME") != home || lookupEnv(gotEnv, "XDG_CONFIG_HOME") != filepath.Join(kellyHome, ".config") || lookupEnv(gotEnv, "XDG_STATE_HOME") != filepath.Join(kellyHome, ".local", "state") {
				t.Fatal("kelly isolation")
			}
		} else if lookupEnv(gotEnv, "HOME") != home || lookupEnv(gotEnv, "XDG_CONFIG_HOME") != "" {
			t.Fatal("personal HOME rewritten")
		}
		if _, err := os.Stat(kellyHome); !os.IsNotExist(err) {
			t.Fatal("launch created profile home")
		}
	}
}

func lookupEnv(env []string, key string) string {
	for _, v := range env {
		if k, x, ok := strings.Cut(v, "="); ok && k == key {
			return x
		}
	}
	return ""
}

func TestRoutingNeverFallsBackToAgy(t *testing.T) {
	signedAgyOnly := profileProbe{map[string]bool{"agy/personal": true, "agy/kelly": true}}
	for _, family := range []string{"codex", "claude"} {
		var out, diag bytes.Buffer
		src := &toolUsage{}
		a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: choiceClassifier{family}}
		if a.Run([]string{"--dry-run", "--json", "fake task"}) != 0 {
			t.Fatal(diag.String())
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != family || result["source"] != "jev" || src.calls["agy"] != 0 {
			t.Fatal("Jev family not honoured", out.String())
		}
	}
	// Default codex with only Antigravity signed in: no silent fallback to agy.
	var out, diag bytes.Buffer
	a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: signedAgyOnly, UsageSource: forbiddenUsage{t}, Env: []string{"SB_CLASSIFIER_PROVIDER=off"}}
	if a.Run([]string{"--dry-run", "fake task"}) != 3 {
		t.Fatal("routing fell back to agy")
	}
	// Jev may explicitly choose Antigravity; usage is then fetched for agy only.
	out.Reset()
	src := &toolUsage{}
	a = App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: choiceClassifier{"agy"}}
	if a.Run([]string{"--dry-run", "--json", "fake task"}) != 0 {
		t.Fatal(diag.String())
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != "agy" || src.calls["agy"] != 2 {
		t.Fatal("Jev agy choice", out.String())
	}
}

func TestRoutedAgyTaskUsesInteractivePrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var out, diag bytes.Buffer
	var got []string
	a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}, Execute: func(_ string, args, _ []string) error {
		got = args
		return nil
	}}
	if a.Run([]string{"--no-log", "--account", "agy/kelly", "fake task"}) != 0 || !reflect.DeepEqual(got, []string{"--prompt-interactive", "fake task"}) {
		t.Fatal("agy task argv", got, diag.String())
	}
}

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

	"switchboard/internal/classifier"
	"switchboard/internal/decision"
	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"
)

type fakeProbe struct {
	calls  int
	signed bool
}

func (p *fakeProbe) Status(_ context.Context, s provider.Spec) provider.Status {
	a := s.Account
	p.calls++
	v := p.signed
	return provider.Status{Account: a, LoginOK: &v}
}

func TestCommands(t *testing.T) {
	for _, args := range [][]string{{"usage", "--json"}, {"schema", "--json"}, {"status", "--json"}} {
		var out, err bytes.Buffer
		p := &fakeProbe{signed: true}
		a := App{Out: &out, Err: &err, Home: t.TempDir(), UsageSource: usage.Unsupported{}, Classifier: forbiddenClassifier{t}, Probe: p, Execute: func(string, []string, []string) error { t.Fatal("unexpected launch"); return nil }}
		code := a.Run(args)
		if _, err := os.Stat(filepath.Join(a.Home, ".local", "share", "switchboard", "decisions.jsonl")); !os.IsNotExist(err) {
			t.Fatal("report wrote decision log")
		}
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
	a := App{Out: &out, Err: &diag, Home: t.TempDir(), UsageSource: usage.Unsupported{}, Env: []string{"KEEP=yes"}, Probe: p, Execute: func(_ string, args, env []string) error {
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
	a := App{Out: &out, Err: &diag, Home: home, UsageSource: usage.Unsupported{}, Probe: p}
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
	var v map[string]any
	json.Unmarshal(out.Bytes(), &v)
	if v["tool"] != "codex" || v["profile"] != "personal" {
		t.Fatal("wrong fallback")
	}
}

type forbiddenClassifier struct{ t *testing.T }

func (f forbiddenClassifier) Choose(context.Context, string, []string, float64) classifier.Result {
	f.t.Fatal("usage invoked classifier")
	return classifier.Result{}
}

type fakeClassifier struct{ calls int }

func (f *fakeClassifier) Choose(context.Context, string, []string, float64) classifier.Result {
	f.calls++
	p := 0.95
	return classifier.Result{Family: "codex", Probability: &p, Confidence: &p, OK: true}
}

func TestRoutingDryRunClassifier(t *testing.T) {
	for _, args := range [][]string{
		{"--dry-run", "fake task", "--json"},
		{"run", "--dry-run", "fake task", "--json"},
		{"classify", "fake task", "--json"},
		{"--dry-run", "--plain", "fake task"},
	} {
		var out, diag bytes.Buffer
		home := t.TempDir()
		f := &fakeClassifier{}
		a := App{
			Out: &out, Err: &diag, Home: home, UsageSource: usage.Unsupported{}, Probe: &fakeProbe{signed: true}, Classifier: f,
			Execute: func(string, []string, []string) error { t.Fatal("dry-run launched"); return nil },
		}
		if a.Run(args) != 0 || f.calls != 1 {
			t.Fatal("dry-run did not classify")
		}
		if !bytes.Contains(out.Bytes(), []byte("Jev classification")) || !bytes.Contains(out.Bytes(), []byte("0.95")) {
			t.Fatal("missing classification output")
		}
		onlyDecisionLog(t, home)
	}
}

func TestClassifierOffAndOverrides(t *testing.T) {
	var out, diag bytes.Buffer
	f := &fakeClassifier{}
	a := App{Out: &out, Err: &diag, Home: t.TempDir(), UsageSource: usage.Unsupported{}, Env: []string{"SB_CLASSIFIER_PROVIDER=invalid"}, Probe: &fakeProbe{signed: true}, Classifier: f}
	if a.Run([]string{"--dry-run", "fake task"}) != 2 {
		t.Fatal("invalid environment config")
	}
	if a.Run([]string{"--classifier-provider", "off", "--dry-run", "fake task", "--json"}) != 0 || f.calls != 0 {
		t.Fatal("off override")
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil || result["confidence"] != nil {
		t.Fatal("rules confidence")
	}
}

func TestDoctorClassifier(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out, diag bytes.Buffer
	a := App{Out: &out, Err: &diag, Home: t.TempDir(), UsageSource: usage.Unsupported{}, Env: []string{"SB_CLASSIFIER_PROVIDER=off"}, Classifier: forbiddenClassifier{t}}
	a.Run([]string{"doctor", "--json"})
	if !bytes.Contains(out.Bytes(), []byte("classifier provider off")) || bytes.Contains(out.Bytes(), []byte("classifier key present")) {
		t.Fatal("off doctor")
	}
	out.Reset()
	a.Env = []string{"TYPESAFE_API_KEY=fake-test-value"}
	a.Run([]string{"doctor", "--json"})
	if !bytes.Contains(out.Bytes(), []byte("classifier key present")) || bytes.Contains(out.Bytes(), []byte("fake-test-value")) {
		t.Fatal("doctor key presence")
	}
	if _, err := os.Stat(filepath.Join(a.Home, ".local", "share", "switchboard", "decisions.jsonl")); !os.IsNotExist(err) {
		t.Fatal("doctor wrote decision log")
	}
}

type calibrationClassifier struct{ probability float64 }

func (f calibrationClassifier) Choose(context.Context, string, []string, float64) classifier.Result {
	confidence, difficulty, dc := 0.3, 1.2, 0.2
	return classifier.Result{Family: "claude", Probability: &f.probability, Confidence: &confidence, Difficulty: &difficulty, DifficultyConfidence: &dc, OK: true}
}

func TestCalibrationProbabilityAndDifficulty(t *testing.T) {
	for _, command := range []string{"classify", "run"} {
		for _, probability := range []float64{0.55, 0.4} {
			var out, diag bytes.Buffer
			home := t.TempDir()
			a := App{Out: &out, Err: &diag, Home: home, UsageSource: usage.Unsupported{}, Probe: &fakeProbe{signed: true}, Classifier: calibrationClassifier{probability}, Execute: func(string, []string, []string) error { t.Fatal("calibration launched"); return nil }}
			if a.Run([]string{command, "--dry-run", "--json", "fake task"}) != 0 {
				t.Fatal("calibration failed")
			}
			var result map[string]any
			if json.Unmarshal(out.Bytes(), &result) != nil {
				t.Fatal("invalid JSON")
			}
			source := "rules"
			if probability >= .5 {
				source = "jev"
			}
			if result["source"] != source || result["difficulty"] != nil || result["jev"].(map[string]any)["probability"] != probability {
				t.Fatal("decoupled gates or raw numbers")
			}
			onlyDecisionLog(t, home)
		}
	}
}

type profileProbe struct{ signed map[string]bool }

func (p profileProbe) Status(_ context.Context, s provider.Spec) provider.Status {
	a := s.Account
	v := p.signed[a.ID()]
	return provider.Status{Account: a, LoginOK: &v}
}

type forbiddenUsage struct{ t *testing.T }

func (f forbiddenUsage) Read(context.Context, string, string) (usage.Data, error) {
	f.t.Fatal("pin collected usage")
	return usage.Data{}, nil
}

func TestPins(t *testing.T) {
	for _, mode := range []string{"flag", "env", "config"} {
		for _, blocked := range []string{"", "signed-out", "cooldown"} {
			t.Run(mode+blocked, func(t *testing.T) {
				home := t.TempDir()
				var out, diag bytes.Buffer
				a := App{Out: &out, Err: &diag, Home: home, Probe: profileProbe{map[string]bool{"codex/personal": blocked != "signed-out", "claude/work": true}}, Classifier: forbiddenClassifier{t}, UsageSource: forbiddenUsage{t}}
				args := []string{"classify", "--json", "fake task"}
				switch mode {
				case "flag":
					args = append(args, "--account", "codex/personal")
				case "env":
					a.Env = []string{"SB_ACCOUNT=codex/personal"}
				case "config":
					path := filepath.Join(home, "config.toml")
					os.WriteFile(path, []byte("pin='codex/personal'"), 0o600)
					args = append(args, "--config", path)
				}
				if blocked == "cooldown" {
					state.Save(state.Path(home, ""), state.State{"codex/personal": time.Now().Add(time.Hour)})
				}
				code := a.Run(args)
				if blocked != "" {
					if code != 3 {
						t.Fatal(code)
					}
					return
				}
				if code != 0 {
					t.Fatal(diag.String())
				}
				var r map[string]any
				json.Unmarshal(out.Bytes(), &r)
				records, err := decision.Read(decision.Path(home, ""))
				if err != nil || len(records) != 1 || records[0].Label == nil || records[0].Label.Account != "codex/personal" {
					t.Fatal("missing implicit pin label")
				}
				if r["tool"] != "codex" || r["profile"] != "personal" {
					t.Fatal("pin changed")
				}
			})
		}
	}
}

type headroomSource struct{}

func (headroomSource) Read(_ context.Context, tool, profile string) (usage.Data, error) {
	v := 80.
	if profile == "personal" {
		v = 20
	}
	return usage.Data{Source: "fake", ObservedAt: time.Now(), Weekly: &v}, nil
}

func TestDefaultFamilyAndStatusFallback(t *testing.T) {
	for _, codex := range []bool{true, false} {
		home := t.TempDir()
		path := filepath.Join(home, "config.toml")
		os.WriteFile(path, []byte("default='codex/work'\n[classifier]\nprovider='off'"), 0o600)
		var out, diag bytes.Buffer
		a := App{Out: &out, Err: &diag, Home: home, Probe: profileProbe{map[string]bool{"codex/work": codex, "codex/personal": codex, "claude/work": true}}, UsageSource: headroomSource{}, Classifier: forbiddenClassifier{t}}
		if a.Run([]string{"--config", path, "classify", "--json", "fake task"}) != 0 {
			t.Fatal(diag.String())
		}
		var r map[string]any
		json.Unmarshal(out.Bytes(), &r)
		if codex {
			if r["tool"] != "codex" || r["profile"] != "personal" {
				t.Fatal("default became pin")
			}
		} else if r["tool"] != "claude" {
			t.Fatal("no family fallback")
		}
	}
}

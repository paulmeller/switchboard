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
	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"
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
	for _, args := range [][]string{{"usage", "--json"}, {"schema", "--json"}, {"status", "--json"}, {"--dry-run", "codex", "personal", "--", "--version"}} {
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
	var v map[string]any
	json.Unmarshal(out.Bytes(), &v)
	if v["tool"] != "claude" || v["profile"] != "work" || v["reason"] != "first eligible fallback" {
		t.Fatal("wrong fallback")
	}
}

type forbiddenClassifier struct{ t *testing.T }

func (f forbiddenClassifier) Choose(context.Context, string, []provider.Account, float64) classifier.Result {
	f.t.Fatal("usage invoked classifier")
	return classifier.Result{}
}

type fakeClassifier struct{ calls int }

func (f *fakeClassifier) Choose(context.Context, string, []provider.Account, float64) classifier.Result {
	f.calls++
	p := 0.95
	return classifier.Result{Account: provider.Account{Tool: "codex", Profile: "work"}, Probability: &p, Confidence: &p, OK: true}
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
			Out: &out, Err: &diag, Home: home, Probe: &fakeProbe{signed: true}, Classifier: f,
			Execute: func(string, []string, []string) error { t.Fatal("dry-run launched"); return nil },
		}
		if a.Run(args) != 0 || f.calls != 1 {
			t.Fatal("dry-run did not classify")
		}
		if !bytes.Contains(out.Bytes(), []byte("Jev classification")) || !bytes.Contains(out.Bytes(), []byte("0.95")) {
			t.Fatal("missing classification output")
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 0 {
			t.Fatal("dry-run created home or state")
		}
	}
}

func TestClassifierOffAndOverrides(t *testing.T) {
	var out, diag bytes.Buffer
	f := &fakeClassifier{}
	a := App{Out: &out, Err: &diag, Home: t.TempDir(), Env: []string{"SB_CLASSIFIER_PROVIDER=invalid"}, Probe: &fakeProbe{signed: true}, Classifier: f}
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
	a := App{Out: &out, Err: &diag, Home: t.TempDir(), Env: []string{"SB_CLASSIFIER_PROVIDER=off"}, Classifier: forbiddenClassifier{t}}
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
}

type calibrationClassifier struct{ probability float64 }

func (f calibrationClassifier) Choose(context.Context, string, []provider.Account, float64) classifier.Result {
	confidence, difficulty, dc := 0.3, 1.2, 0.2
	return classifier.Result{Account: provider.Account{Tool: "claude", Profile: "personal"}, Probability: &f.probability, Confidence: &confidence, Difficulty: &difficulty, DifficultyConfidence: &dc, OK: true}
}

func TestCalibrationProbabilityAndDifficulty(t *testing.T) {
	for _, command := range []string{"classify", "run"} {
		for _, probability := range []float64{0.55, 0.4} {
			var out, diag bytes.Buffer
			home := t.TempDir()
			a := App{Out: &out, Err: &diag, Home: home, Probe: &fakeProbe{signed: true}, Classifier: calibrationClassifier{probability}, Execute: func(string, []string, []string) error { t.Fatal("calibration launched"); return nil }}
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
			entries, _ := os.ReadDir(home)
			if len(entries) != 0 {
				t.Fatal("calibration wrote files")
			}
		}
	}
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"switchboard/internal/classifier"
	"switchboard/internal/config"
	"switchboard/internal/decision"
	"switchboard/internal/provider"
	"switchboard/internal/usage"
)

func TestModelSelection(t *testing.T) {
	name, effort := "fake-test-value", "low"
	m := config.Models{EasyMax: .67, HardMin: 1.33, Codex: config.ModelFamily{Easy: &name, Normal: &name, Hard: &name, NormalEffort: &effort}, Claude: config.ModelFamily{Normal: &name}}
	for _, tc := range []struct {
		score float64
		tier  string
	}{{.67, "easy"}, {1, "normal"}, {1.33, "hard"}} {
		r := chooseModel(CLI{}, m, provider.Account{Tool: "codex", Profile: "work"}, &tc.score, []string{"task"})
		if r.Tier == nil || *r.Tier != tc.tier {
			t.Fatal(r)
		}
	}
	a := provider.Account{Tool: "codex", Profile: "work"}
	r := chooseModel(CLI{}, m, a, nil, []string{"task"})
	if !reflect.DeepEqual(r.Argv, []string{"--model", name, "-c", `model_reasoning_effort="low"`, "task"}) {
		t.Fatal(r.Argv)
	}
	r = chooseModel(CLI{}, m, provider.Account{Tool: "claude", Profile: "work"}, nil, []string{"task"})
	if !reflect.DeepEqual(r.Argv, []string{"--model", name, "task"}) {
		t.Fatal(r.Argv)
	}
	for _, args := range [][]string{{"--model", name}, {"-m", name}, {"--model=" + name}, {"-m=" + name}, {"-c", `model="fake-test-value"`}, {"--config=model_reasoning_effort=low"}} {
		r = chooseModel(CLI{}, m, a, nil, args)
		if r.Source != "passthrough" || !reflect.DeepEqual(r.Argv, args) || r.Tier != nil {
			t.Fatal(r)
		}
		r = chooseModel(CLI{Model: name}, m, a, nil, args)
		if r.Source != "flag" || !reflect.DeepEqual(r.Argv, []string{"--model", name}) {
			t.Fatal(r)
		}
	}
	m.Accounts = map[string]config.ModelAccount{a.ID(): {Model: &name}}
	if r = chooseModel(CLI{}, m, a, nil, nil); r.Source != "account" {
		t.Fatal(r)
	}
	if r = chooseModel(CLI{}, config.Models{}, a, nil, []string{"task"}); r.Model != nil || r.Tier != nil || !reflect.DeepEqual(r.Argv, []string{"task"}) {
		t.Fatal(r)
	}
}

func TestModelLogFeedbackTune(t *testing.T) {
	var out, diag bytes.Buffer
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("[classifier]\nprovider='off'\n[models.codex]\nnormal='fake-test-value'\nnormal_reasoning_effort='low'"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := App{Home: home, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: forbiddenClassifier{t}}
	if app.Run([]string{"--config", path, "--dry-run", "--json", "task"}) != 0 {
		t.Fatal(diag.String())
	}
	var result modelPick
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Model == nil || result.Tier == nil || result.Source != "tier" || len(result.Argv) != 5 {
		t.Fatal(out.String())
	}
	records, err := decision.Read(decision.Path(home, ""))
	if err != nil || len(records) != 1 || records[0].Model == nil || records[0].Tier == nil || records[0].ModelSource == nil {
		t.Fatal(records, err)
	}
	for _, args := range [][]string{{"feedback", "last", "--ok", "--model", "conflict"}, {"feedback", "last", "--model", "fake-test-value"}, {"tune", "--json"}} {
		out.Reset()
		diag.Reset()
		code := app.Run(args)
		if len(args) > 2 && args[2] == "--ok" {
			if code != 2 {
				t.Fatal(code)
			}
			continue
		}
		if code != 0 {
			t.Fatal(diag.String())
		}
	}
	var report decision.Report
	if json.Unmarshal(out.Bytes(), &report) != nil || report.Tiers[1].N != 1 || report.Tiers[1].Correct != 1 {
		t.Fatal(out.String())
	}
	out.Reset()
	if app.Run([]string{"--config", path, "--model", "fake-test-value", "--dry-run", "--json", "codex", "work", "--", "-m", "other"}) != 0 {
		t.Fatal(diag.String())
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Source != "flag" || !reflect.DeepEqual(result.Argv, []string{"--model", "fake-test-value"}) {
		t.Fatal(out.String())
	}
}

func TestModelLaunchArgv(t *testing.T) {
	for _, tool := range []string{"claude", "codex"} {
		for _, explicit := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", tool, explicit), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir)
				home := t.TempDir()
				path := filepath.Join(home, "config.toml")
				body := "default='" + tool + "'\n[classifier]\nprovider='off'\n[models." + tool + "]\nnormal='fake-test-value'\n"
				want := []string{"--model", "fake-test-value"}
				if tool == "codex" {
					body += "normal_reasoning_effort='low'\n"
					want = append(want, "-c", `model_reasoning_effort="low"`)
				}
				want = append(want, "task")
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				var out, diag bytes.Buffer
				called := false
				a := App{Home: home, Out: &out, Err: &diag, Env: []string{"CLAUDE_OAUTH_TOKEN_1=fake-test-value"}, Probe: &fakeProbe{signed: true}, Classifier: forbiddenClassifier{t}, UsageSource: usage.Unsupported{}, Execute: func(_ string, args, env []string) error {
					called = true
					if !reflect.DeepEqual(args, want) {
						t.Fatal(args)
					}
					return nil
				}}
				args := []string{"--config", path, "--no-log", "run", "task"}
				if explicit {
					args = []string{"--config", path, tool, "work", "--", "task"}
				}
				if code := a.Run(args); code != 0 || !called {
					t.Fatal(code, diag.String())
				}
			})
		}
	}
}

type scoredClassifier struct{ score, confidence float64 }

func (f scoredClassifier) Choose(context.Context, string, []string, float64) classifier.Result {
	p := 1.0
	return classifier.Result{OK: true, Family: "codex", Probability: &p, Difficulty: &f.score, DifficultyConfidence: &f.confidence}
}

func TestReportedDifficultyModels(t *testing.T) {
	for _, tc := range []struct {
		score, confidence float64
		tier              string
	}{{.67, .5, "easy"}, {1, .5, "normal"}, {1.33, .5, "hard"}, {2, .49, "normal"}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.toml")
			if err := os.WriteFile(path, []byte("[models.codex]\neasy='fake-test-value'\nnormal='fake-test-value'\nhard='fake-test-value'"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, diag bytes.Buffer
			a := App{Home: home, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: scoredClassifier{tc.score, tc.confidence}}
			if a.Run([]string{"--config", path, "--no-log", "--json", "classify", "task"}) != 0 {
				t.Fatal(diag.String())
			}
			var result modelPick
			if json.Unmarshal(out.Bytes(), &result) != nil || result.Tier == nil || *result.Tier != tc.tier {
				t.Fatal(out.String())
			}
		})
	}
}

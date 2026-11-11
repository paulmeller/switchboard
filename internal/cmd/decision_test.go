package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"switchboard/internal/decision"
	"switchboard/internal/usage"
)

func onlyDecisionLog(t *testing.T, home string) {
	t.Helper()
	allowed := map[string]bool{".": true, ".local": true, ".local/share": true, ".local/share/switchboard": true, ".local/share/switchboard/decisions.jsonl": true}
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(home, path)
		if !allowed[filepath.ToSlash(rel)] {
			t.Errorf("unexpected dry-run path %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Dir(decision.Path(home, "")), 0o700)
	assertMode(t, decision.Path(home, ""), 0o600)
}

func assertMode(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("permissions %o, want %o", info.Mode().Perm(), mode)
	}
}

func TestDecisionLogging(t *testing.T) {
	for _, command := range []string{"classify", "run"} {
		t.Run(command, func(t *testing.T) {
			var out, diag bytes.Buffer
			home, data := t.TempDir(), t.TempDir()
			a := App{Home: home, Env: []string{"XDG_DATA_HOME=" + data, "TYPESAFE_API_KEY=fake-test-value"}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: &fakeClassifier{}}
			if a.Run([]string{command, "--dry-run", "--json", "task bytes"}) != 0 {
				t.Fatal(diag.String())
			}
			path := decision.Path(home, data)
			records, err := decision.Read(path)
			if err != nil || len(records) != 1 {
				t.Fatal("missing log", err)
			}
			d := records[0]
			hash := sha256.Sum256([]byte("task bytes"))
			if d.Task == nil || *d.Task != "task bytes" || d.TaskSHA256 != hex.EncodeToString(hash[:]) || !d.DryRun || d.ExplicitFamily || d.Type != "decision" || d.Source != "jev" || d.Account != "codex/work" || d.Probability == nil || d.ExitCode != nil || d.DurationMS != nil {
				t.Fatalf("unexpected decision: %+v", d)
			}
			var result map[string]any
			if json.Unmarshal(out.Bytes(), &result) != nil || result["id"] != d.ID {
				t.Fatal("missing decision id")
			}
			b, _ := os.ReadFile(path)
			var fields map[string]any
			if json.Unmarshal(b, &fields) != nil || fields["explicit_family"] != false {
				t.Fatal("missing explicit_family=false")
			}
			for _, secret := range []string{"fake-test-value", "TYPESAFE_API_KEY", "candidates", "weekly_used_pct"} {
				if bytes.Contains(b, []byte(secret)) {
					t.Fatal("non-normalized log")
				}
			}
			assertMode(t, path, 0o600)
			assertMode(t, filepath.Dir(path), 0o700)
		})
	}
}

func TestLogControls(t *testing.T) {
	for _, tc := range []struct {
		name, config          string
		noLog, redact, broken bool
		family                bool
	}{
		{name: "no-log", noLog: true},
		{name: "disabled", config: "[log]\nenabled=false"},
		{name: "redacted", config: "[log]\nredact_task=true", redact: true},
		{name: "failed", broken: true},
		{name: "family no-log", family: true, noLog: true},
		{name: "family disabled", family: true, config: "[log]\nenabled=false"},
		{name: "family redacted", family: true, config: "[log]\nredact_task=true", redact: true},
		{name: "family failed", family: true, broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diag bytes.Buffer
			home := t.TempDir()
			data := t.TempDir()
			if tc.broken {
				data = filepath.Join(data, "file")
				if err := os.WriteFile(data, []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			conf := filepath.Join(home, "config.toml")
			if err := os.WriteFile(conf, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			a := App{Home: home, Env: []string{"XDG_DATA_HOME=" + data}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: &fakeClassifier{}}
			args := []string{"--config", conf, "--dry-run", "--json"}
			if tc.noLog {
				args = append(args, "--no-log")
			}
			task := "redacted text"
			if tc.family {
				task = ""
				args = append(args, "codex", "--", "redacted text")
			} else {
				args = append(args, "classify", task)
			}
			if a.Run(args) != 0 {
				t.Fatal(diag.String())
			}
			records, err := decision.Read(decision.Path(home, data))
			if tc.redact {
				hash := sha256.Sum256([]byte(task))
				if err != nil || len(records) != 1 || records[0].Task != nil || records[0].TaskSHA256 != hex.EncodeToString(hash[:]) {
					t.Fatal("redaction failed")
				}
				b, _ := os.ReadFile(decision.Path(home, data))
				if bytes.Contains(b, []byte("redacted text")) {
					t.Fatal("text leaked")
				}
			} else if len(records) != 0 {
				t.Fatal("unexpected records")
			}
			if tc.broken && !bytes.Contains(diag.Bytes(), []byte("cannot append decision log")) {
				t.Fatal("missing safe diagnostic")
			}
		})
	}
}

func TestRealLaunchLog(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, failed := range []bool{false, true} {
		var out, diag bytes.Buffer
		home := t.TempDir()
		called := false
		a := App{Home: home, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: &fakeClassifier{}, Execute: func(string, []string, []string) error {
			called = true
			if failed {
				return errors.New("fake-test-value")
			}
			return nil
		}}
		code := a.Run([]string{"run", "fake task"})
		if !called || (failed && code != 1) || (!failed && code != 0) {
			t.Fatal("launch exit")
		}
		records, err := decision.Read(decision.Path(home, ""))
		if err != nil || len(records) != 1 || records[0].DryRun || records[0].ExitCode != nil || records[0].DurationMS != nil {
			t.Fatal("launch log")
		}
		if bytes.Contains(diag.Bytes(), []byte("fake-test-value")) {
			t.Fatal("error leaked")
		}
	}
}

func TestFeedbackAndTune(t *testing.T) {
	var out, diag bytes.Buffer
	home := t.TempDir()
	a := App{Home: home, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
	if a.Run([]string{"--account", "codex/work", "classify", "--json", "task"}) != 0 {
		t.Fatal(diag.String())
	}
	records, _ := decision.Read(decision.Path(home, ""))
	if len(records) != 1 || records[0].Label == nil || records[0].Label.Account != "codex/work" {
		t.Fatal("implicit pin label")
	}
	id := records[0].ID
	for _, args := range [][]string{{"feedback", id, "--ok"}, {"feedback", "last", "--family", "claude"}, {"feedback", "last", "--account", "claude/personal", "--no-log"}} {
		out.Reset()
		if a.Run(args) != 0 {
			t.Fatal(diag.String())
		}
	}
	records, _ = decision.Read(decision.Path(home, ""))
	if len(records) != 4 || records[3].DecisionID != id || records[3].Family != "claude" {
		t.Fatal("feedback target or correction")
	}
	for _, args := range [][]string{{"feedback"}, {"feedback", "unknown", "--ok"}, {"feedback", "last"}, {"feedback", "last", "--family", "agy"}, {"feedback", "last", "--account", "codex"}, {"feedback", "last", "--family", "claude", "--account", "codex/work"}, {"feedback", "last", "--ok", "--family", "claude"}, {"feedback", "last", "--ok", "--account", "codex/personal"}} {
		if a.Run(args) != 2 {
			t.Fatalf("accepted invalid feedback %v", args)
		}
	}
	out.Reset()
	if a.Run([]string{"tune", "--json"}) != 0 {
		t.Fatal(diag.String())
	}
	var report decision.Report
	if json.Unmarshal(out.Bytes(), &report) != nil || report.Decisions != 1 || report.Labelled != 1 || report.Families[1].Correct != 0 {
		t.Fatal("tune report")
	}
	empty := App{Home: t.TempDir(), Out: &out, Err: &diag, Classifier: forbiddenClassifier{t}, UsageSource: forbiddenUsage{t}}
	if empty.Run([]string{"feedback", "last", "--ok"}) != 2 {
		t.Fatal("empty feedback")
	}
	out.Reset()
	if empty.Run([]string{"tune", "--json"}) != 0 {
		t.Fatal("empty tune")
	}
}

func TestReportsNeverAppendLog(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, command := range []string{"usage", "status", "doctor", "schema", "cooldown"} {
		t.Run(command, func(t *testing.T) {
			var out, diag bytes.Buffer
			home := t.TempDir()
			data := t.TempDir()
			path := decision.Path(home, data)
			r, err := decision.New("decision")
			if err != nil {
				t.Fatal(err)
			}
			if err = decision.Append(path, 5242880, decision.Decision{Record: r}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			a := App{Home: home, Env: []string{"XDG_DATA_HOME=" + data, "SB_CLASSIFIER_PROVIDER=off"}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: forbiddenClassifier{t}, Execute: func(string, []string, []string) error { t.Fatal("report launched"); return nil }}
			args := []string{command, "--json"}
			if command == "cooldown" {
				args = append(args, "codex/work", "--dry-run")
			}
			a.Run(args)
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("report changed decision log")
			}
			if _, err = os.Stat(path + ".1"); !os.IsNotExist(err) {
				t.Fatal("report rotated decision log")
			}
		})
	}
}

func TestFeedbackExplicitLabelRequired(t *testing.T) {
	t.Setenv("SB_ACCOUNT", "claude/personal")
	var out, diag bytes.Buffer
	home := t.TempDir()
	conf := filepath.Join(home, "config.toml")
	if err := os.WriteFile(conf, []byte("[log]\nenabled=false"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := decision.New("decision")
	if err != nil {
		t.Fatal(err)
	}
	r.Family, r.Account = "codex", "codex/work"
	path := decision.Path(home, "")
	if err = decision.Append(path, 5242880, decision.Decision{Record: r}); err != nil {
		t.Fatal(err)
	}
	a := App{Home: home, Out: &out, Err: &diag, Classifier: forbiddenClassifier{t}, UsageSource: forbiddenUsage{t}}
	if a.Run([]string{"feedback", "last"}) != 2 {
		t.Fatal("environment silently labelled decision")
	}
	if a.Run([]string{"--config", conf, "feedback", "last", "--ok", "--no-log"}) != 0 {
		t.Fatal(diag.String())
	}
	if a.Run([]string{"feedback", "last", "--account=codex/work", "--ok"}) != 0 {
		t.Fatal(diag.String())
	}
	records, err := decision.Read(path)
	if err != nil || len(records) != 3 || records[1].Family != "codex" || records[1].DecisionID != r.ID {
		t.Fatal("disabled logging blocked explicit feedback")
	}
}

func TestRoutingFailureLoggingDoesNotChangeExit(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, args := range [][]string{{"classify", "task"}, {"codex"}} {
			var out, diag bytes.Buffer
			home := t.TempDir()
			data := t.TempDir()
			blocked := filepath.Join(data, "blocked")
			if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
				t.Fatal(err)
			}
			a := App{Home: home, Env: []string{"XDG_DATA_HOME=" + blocked}, Out: &out, Err: &diag, Probe: &fakeProbe{}, Classifier: forbiddenClassifier{t}, UsageSource: forbiddenUsage{t}}
			if disabled {
				args = append([]string{"--no-log"}, args...)
			}
			if a.Run(args) != 3 {
				t.Fatal("logging changed no-eligible exit")
			}
			if bytes.Contains(diag.Bytes(), []byte("cannot append decision log")) == disabled {
				t.Fatal("log failure diagnostic")
			}
		}
	}
}

func TestFeedbackRotatedLast(t *testing.T) {
	var out, diag bytes.Buffer
	home := t.TempDir()
	conf := filepath.Join(home, "config.toml")
	if err := os.WriteFile(conf, []byte("[log]\nmax_bytes=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := decision.New("decision")
	if err != nil {
		t.Fatal(err)
	}
	r.Family, r.Account = "codex", "codex/work"
	path := decision.Path(home, "")
	if err = decision.Append(path, 1, decision.Decision{Record: r}); err != nil {
		t.Fatal(err)
	}
	a := App{Home: home, Out: &out, Err: &diag, Classifier: forbiddenClassifier{t}, UsageSource: forbiddenUsage{t}}
	for range 2 {
		if a.Run([]string{"--config", conf, "feedback", "last", "--ok"}) != 0 {
			t.Fatal(diag.String())
		}
	}
	for _, p := range []string{path, path + ".1"} {
		assertMode(t, p, 0o600)
	}
	records, err := decision.Read(path)
	if err != nil || len(records) != 2 {
		t.Fatal("feedback rotation")
	}
	for _, d := range records {
		if d.Type != "feedback" || d.DecisionID != r.ID {
			t.Fatal("last did not target retained decision")
		}
	}
}

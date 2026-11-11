package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"switchboard/internal/decision"
	"switchboard/internal/picker"
	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"
)

type familyUsage struct {
	t      *testing.T
	family string
	rows   map[string]usage.Data
	calls  atomic.Int32
}

func (f *familyUsage) Read(_ context.Context, tool, profile string) (usage.Data, error) {
	f.calls.Add(1)
	if tool != f.family {
		f.t.Errorf("collected usage outside selected family")
	}
	return f.rows[profile], nil
}

func TestExplicitFamilySelection(t *testing.T) {
	for _, family := range []string{"codex", "claude"} {
		for _, unknown := range []bool{false, true} {
			t.Run(family+"/unknown="+strconv.FormatBool(unknown), func(t *testing.T) {
				var out, diag bytes.Buffer
				home := t.TempDir()
				now := time.Now()
				workUsed, personalUsed := 97., 13.
				soon, later := now.Add(time.Hour).Format(time.RFC3339), now.Add(7*24*time.Hour).Format(time.RFC3339)
				src := &familyUsage{t: t, family: family, rows: map[string]usage.Data{
					"work":     {Source: "fake", ObservedAt: now, Weekly: &workUsed, WeeklyReset: &soon},
					"personal": {Source: "fake", ObservedAt: now, Weekly: &personalUsed, WeeklyReset: &later},
				}}
				if unknown {
					src.rows = nil
				}
				p := &fakeProbe{signed: true}
				a := App{Home: home, Out: &out, Err: &diag, Probe: p, UsageSource: src, Classifier: forbiddenClassifier{t}, Execute: func(string, []string, []string) error {
					t.Fatal("dry-run launched")
					return nil
				}}
				if a.Run([]string{"--dry-run", "--json", family, "--", "exec", "argv is not task text"}) != 0 {
					t.Fatal(diag.String())
				}
				var result map[string]string
				wantProfile := "personal"
				if unknown {
					wantProfile = "work"
				}
				if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != family || result["profile"] != wantProfile || result["id"] == "" {
					t.Fatal("wrong selection or missing id")
				}
				if unknown {
					if result["reason"] != "unknown usage, ranked below known headroom" {
						t.Fatal("unknown usage reason lost")
					}
				} else if result["reason"] != "most headroom: 87% left" {
					t.Fatal("picker reason lost")
				}
				if p.calls == 0 || src.calls.Load() != 2 {
					t.Fatal("unpinned launch skipped status/usage")
				}
				records, err := decision.Read(decision.Path(home, ""))
				if err != nil || len(records) != 1 {
					t.Fatal("missing launch decision", err)
				}
				d := records[0]
				hash := sha256.Sum256(nil)
				if !d.ExplicitFamily || !d.DryRun || d.Source != "rules" || d.Family != family || d.Account != family+"/"+wantProfile || d.Label != nil || d.Override != nil || d.ID != result["id"] || d.Reason != result["reason"] || d.Task == nil || *d.Task != "" || d.TaskSHA256 != hex.EncodeToString(hash[:]) || d.ExitCode != nil || d.DurationMS != nil {
					t.Fatal("incorrect normalized launch decision")
				}
				onlyDecisionLog(t, home)
			})
		}
	}
}

func TestExplicitFamilyPins(t *testing.T) {
	for _, args := range [][]string{
		{"--account", "codex/personal", "codex"},
		{"--account=codex/personal", "codex", "--", "--version"},
		{"codex", "personal", "--", "--version"},
	} {
		var out, diag bytes.Buffer
		home := t.TempDir()
		p := &fakeProbe{}
		a := App{Home: home, Out: &out, Err: &diag, Probe: p, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if a.Run(append([]string{"--dry-run", "--json"}, args...)) != 0 {
			t.Fatal(diag.String())
		}
		records, err := decision.Read(decision.Path(home, ""))
		if err != nil || len(records) != 1 {
			t.Fatal("missing pinned decision", err)
		}
		d := records[0]
		if p.calls != 0 || !d.ExplicitFamily || d.Account != "codex/personal" || d.Label == nil || d.Label.Account != d.Account || d.Label.Family != "codex" || d.Override == nil || !d.Override.Pin || d.Override.Account != d.Account {
			t.Fatal("pin did not bypass eligibility or label correctly")
		}
		onlyDecisionLog(t, home)
	}
	for _, account := range []string{"claude/work", "codex", "codex/invalid", ""} {
		var out, diag bytes.Buffer
		a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if a.Run([]string{"--dry-run", "--account=" + account, "codex"}) != 2 {
			t.Fatal("invalid or mismatched account accepted")
		}
	}
}

func TestHarvestConfigSelection(t *testing.T) {
	for _, command := range [][]string{{"codex"}, {"run", "fake task"}} {
		for _, tc := range []struct{ setting, profile string }{
			{"", "work"},
			{"harvest_min_headroom_pct=31", "personal"},
			{"harvest_window_hours=0", "personal"},
			{"harvest_window_hours=0.5", "personal"},
			{"usage_exhausted_pct=70", "personal"},
		} {
			t.Run(command[0]+"/"+tc.setting, func(t *testing.T) {
				home := t.TempDir()
				path := filepath.Join(home, "config.toml")
				if err := os.WriteFile(path, []byte(tc.setting+"\n[classifier]\nprovider='off'"), 0o600); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				workUsed, personalUsed := 70., 13.
				reset := now.Add(time.Hour).Format(time.RFC3339)
				src := &familyUsage{t: t, family: "codex", rows: map[string]usage.Data{
					"work":     {Source: "fake", ObservedAt: now, Weekly: &workUsed, WeeklyReset: &reset},
					"personal": {Source: "fake", ObservedAt: now, Weekly: &personalUsed},
				}}
				var out, diag bytes.Buffer
				a := App{Home: home, Out: &out, Err: &diag, Probe: profileProbe{map[string]bool{"codex/work": true, "codex/personal": true}}, UsageSource: src, Classifier: forbiddenClassifier{t}}
				args := append([]string{"--config", path, "--dry-run", "--json"}, command...)
				if a.Run(args) != 0 {
					t.Fatal(diag.String())
				}
				var result map[string]any
				if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != "codex" || result["profile"] != tc.profile {
					t.Fatal("harvest config did not reach selection")
				}
			})
		}
	}
}

func TestExplicitFamilyIgnoresRoutingPins(t *testing.T) {
	// Kong resolves process environment independently of App.Env.
	for _, envPin := range []string{"", "claude/work"} {
		t.Run("env="+envPin, func(t *testing.T) {
			t.Setenv("SB_ACCOUNT", envPin)
			var out, diag bytes.Buffer
			home := t.TempDir()
			conf := filepath.Join(home, "config.toml")
			if err := os.WriteFile(conf, []byte("pin='claude/personal'"), 0o600); err != nil {
				t.Fatal(err)
			}
			a := App{Home: home, Env: []string{"SB_ACCOUNT=" + envPin}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: headroomSource{}, Classifier: forbiddenClassifier{t}}
			if a.Run([]string{"--config", conf, "--dry-run", "--json", "codex"}) != 0 {
				t.Fatal(diag.String())
			}
			records, err := decision.Read(decision.Path(home, ""))
			if err != nil || len(records) != 1 || records[0].Account != "codex/personal" || records[0].Override != nil || records[0].Label != nil {
				t.Fatal("routing pin affected explicit-family pick or label")
			}
		})
	}
}

func TestExplicitFamilyCacheTTL(t *testing.T) {
	for _, ttl := range []string{"1h", "0s"} {
		t.Run(ttl, func(t *testing.T) {
			var out, diag bytes.Buffer
			home := t.TempDir()
			path := filepath.Join(filepath.Dir(state.Path(home, "")), "usage-cache.json")
			now := time.Now()
			picker.Cached(context.Background(), headroomSource{}, path, []usage.Row{{Tool: "codex", Profile: "work"}, {Tool: "codex", Profile: "personal"}}, time.Hour, time.Second, now, true)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			conf := filepath.Join(home, "config.toml")
			if err := os.WriteFile(conf, []byte("usage_cache_ttl='"+ttl+"'"), 0o600); err != nil {
				t.Fatal(err)
			}
			workUsed, personalUsed := 10., 90.
			src := &familyUsage{t: t, family: "codex", rows: map[string]usage.Data{
				"work":     {Source: "fake", ObservedAt: now, Weekly: &workUsed},
				"personal": {Source: "fake", ObservedAt: now, Weekly: &personalUsed},
			}}
			a := App{Home: home, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: forbiddenClassifier{t}}
			if a.Run([]string{"--config", conf, "--dry-run", "--json", "codex"}) != 0 {
				t.Fatal(diag.String())
			}
			want, calls := "codex/personal", int32(0)
			if ttl == "0s" {
				want, calls = "codex/work", 2
			}
			records, err := decision.Read(decision.Path(home, ""))
			if err != nil || len(records) != 1 || records[0].Account != want || src.calls.Load() != calls {
				t.Fatal("configured cache TTL ignored")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("dry-run changed usage cache")
			}
		})
	}
}

func TestExplicitFamilyEligibility(t *testing.T) {
	for _, blocked := range []string{"work cooldown", "family cooldown", "signed out", "unknown login"} {
		t.Run(blocked, func(t *testing.T) {
			var out, diag bytes.Buffer
			home := t.TempDir()
			signed := map[string]bool{"claude/work": true}
			cooldown := state.State{}
			if strings.Contains(blocked, "cooldown") {
				signed["codex/work"], signed["codex/personal"] = true, true
				cooldown["codex/work"] = time.Now().Add(time.Hour)
				if blocked == "family cooldown" {
					cooldown["codex/personal"] = time.Now().Add(time.Hour)
				}
				if err := state.Save(state.Path(home, ""), cooldown); err != nil {
					t.Fatal(err)
				}
			}
			a := App{Home: home, Out: &out, Err: &diag, Probe: profileProbe{signed}, UsageSource: usage.Unsupported{}, Classifier: forbiddenClassifier{t}}
			if blocked == "unknown login" {
				a.Probe = unknownProbe{}
			}
			want := 3
			if blocked == "work cooldown" {
				want = 0
			}
			if code := a.Run([]string{"--dry-run", "codex"}); code != want {
				t.Fatalf("exit %d, want %d", code, want)
			}
			records, err := decision.Read(decision.Path(home, ""))
			if err != nil || len(records) != 1 || !records[0].ExplicitFamily || records[0].Label != nil {
				t.Fatal("missing eligibility decision")
			}
			if want == 3 {
				if records[0].Family != "" || records[0].Account != "" {
					t.Fatal("failed family selection fell through")
				}
			} else if records[0].Account != "codex/personal" {
				t.Fatal("cooldown ignored")
			}
		})
	}
}

type unknownProbe struct{}

func (unknownProbe) Status(_ context.Context, a provider.Account) provider.Status {
	return provider.Status{Account: a}
}

func TestExplicitFamilyLaunchLogBeforeExec(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, pin := range []bool{false, true} {
		var out, diag bytes.Buffer
		home := t.TempDir()
		called := false
		a := App{Home: home, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: headroomSource{}, Classifier: forbiddenClassifier{t}, Execute: func(_ string, argv, _ []string) error {
			called = true
			records, err := decision.Read(decision.Path(home, ""))
			if err != nil || len(records) != 1 || !records[0].ExplicitFamily || records[0].DryRun || records[0].Account != "codex/personal" || records[0].ExitCode != nil || records[0].DurationMS != nil {
				t.Fatal("decision not logged before exec")
			}
			if !reflect.DeepEqual(argv, []string{"exec", "space arg", "--flag", "a,b"}) {
				t.Fatal("passthrough argv changed")
			}
			return nil
		}}
		args := []string{"--no-input", "codex"}
		if pin {
			args = append(args, "personal")
		}
		args = append(args, "--", "exec", "space arg", "--flag", "a,b")
		if a.Run(args) != 0 || !called {
			t.Fatal("fake launch failed")
		}
	}
}

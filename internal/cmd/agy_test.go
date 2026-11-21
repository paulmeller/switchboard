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

func (f choiceClassifier) Choose(_ context.Context, _ string, eligible []string, _ float64) classifier.Result {
	p, c := 0.9, 0.9
	return classifier.Result{Family: f.family, Probability: &p, Confidence: &c, OK: true}
}

// toolUsage counts usage reads per tool/profile and returns configured or unknown usage.
type toolUsage struct {
	mu    sync.Mutex
	calls map[string]int
	rows  map[string]usage.Data
}

func (u *toolUsage) Read(_ context.Context, tool, profile string) (usage.Data, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.calls == nil {
		u.calls = map[string]int{}
	}
	u.calls[tool]++
	u.calls[tool+"/"+profile]++
	if d, ok := u.rows[tool+"/"+profile]; ok {
		return d, nil
	}
	return usage.Data{}, errors.New("fake unknown")
}

func (u *toolUsage) count(key string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[key]
}

// writeConfig stores a sb TOML config outside the test home (dry-run home walks
// must only find the decision log) and returns the SB_CONFIG environment entry.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return "SB_CONFIG=" + path
}

// agyAccounts declares two generic accounts: work keeps the process HOME,
// second has an isolated profile home. pool optionally joins both in one pool.
func agyAccounts(secondHome, pool string) string {
	body := "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"Antigravity (work)\"\n"
	if pool != "" {
		body += "quota_pool = \"" + pool + "\"\n"
	}
	body += "\n[[tool_accounts]]\nid = \"agy/second\"\nlabel = \"Antigravity (second)\"\nhome = \"" + secondHome + "\"\n"
	if pool != "" {
		body += "quota_pool = \"" + pool + "\"\n"
	}
	return body
}

func weekly(used float64) usage.Data {
	return usage.Data{Source: "fake", ObservedAt: time.Now(), Weekly: &used}
}

func dryResult(t *testing.T, a App, args ...string) (int, map[string]any) {
	t.Helper()
	out := a.Out.(*bytes.Buffer)
	out.Reset()
	code := a.Run(append([]string{"--dry-run", "--json"}, args...))
	var result map[string]any
	if code == 0 && json.Unmarshal(out.Bytes(), &result) != nil {
		t.Fatal("invalid json", out.String())
	}
	return code, result
}

func account(result map[string]any) string {
	if result == nil {
		return ""
	}
	return result["tool"].(string) + "/" + result["profile"].(string)
}

func TestAgyImplicitDefault(t *testing.T) {
	home := t.TempDir()
	missing := "SB_CONFIG=" + filepath.Join(t.TempDir(), "absent.toml")
	for _, args := range [][]string{{"agy", "personal"}, {"agy", "personal", "--", "--version"}, {"--account", "agy/personal", "agy"}, {"--account=agy/personal", "agy", "--", "--help"}} {
		var out, diag bytes.Buffer
		p := &fakeProbe{}
		a := App{Home: home, Env: []string{missing}, Out: &out, Err: &diag, Probe: p, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if code, result := dryResult(t, a, args...); code != 0 || account(result) != "agy/personal" || p.calls != 0 {
			t.Fatal("implicit default pin", args, diag.String())
		}
	}
	// Unpinned sb agy balances the one implicit account.
	var out, diag bytes.Buffer
	src := &toolUsage{}
	a := App{Home: t.TempDir(), Env: []string{missing}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: forbiddenClassifier{t}}
	if code, result := dryResult(t, a, "agy"); code != 0 || account(result) != "agy/personal" || src.count("agy/personal") != 1 {
		t.Fatal("implicit default balance", diag.String())
	}
	for _, args := range [][]string{{"--account=agy/work", "agy"}, {"--account=agy/personal", "codex"}, {"--account=codex/work", "agy"}} {
		var out, diag bytes.Buffer
		a := App{Home: t.TempDir(), Env: []string{missing}, Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if a.Run(append([]string{"--dry-run"}, args...)) != 2 {
			t.Fatal("mismatched agy account accepted", args)
		}
	}
	// Status and usage report exactly the implicit account.
	out.Reset()
	a = App{Home: t.TempDir(), Env: []string{missing}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}}
	if a.Run([]string{"status", "--json"}) != 0 {
		t.Fatal(diag.String())
	}
	var status struct{ Accounts []map[string]any }
	if json.Unmarshal(out.Bytes(), &status) != nil || len(status.Accounts) != 5 || status.Accounts[4]["tool"] != "agy" || status.Accounts[4]["profile"] != "personal" {
		t.Fatal("implicit status", out.String())
	}
	out.Reset()
	a.Run([]string{"usage", "--json"})
	var report usage.Report
	if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Accounts) != 7 || report.Accounts[4].Profile != "personal" || *report.Accounts[4].Account != "Antigravity" {
		t.Fatal("implicit usage row", out.String())
	}
}

func TestAgyConfiguredAccounts(t *testing.T) {
	home := t.TempDir()
	cfg := writeConfig(t, agyAccounts(filepath.Join(t.TempDir(), "profiles", "second"), ""))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"agy", "second"}, "agy/second"},
		{[]string{"agy", "second", "--", "--version"}, "agy/second"},
		{[]string{"--account", "agy/second", "agy"}, "agy/second"},
		{[]string{"agy", "work"}, "agy/work"},
		{[]string{"--account=agy/work", "agy", "--", "--help"}, "agy/work"},
	} {
		var out, diag bytes.Buffer
		p := &fakeProbe{}
		a := App{Home: home, Env: []string{cfg}, Out: &out, Err: &diag, Probe: p, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}, Execute: func(string, []string, []string) (int, error) {
			t.Fatal("dry-run launched")
			return 0, nil
		}}
		if code, result := dryResult(t, a, tc.args...); code != 0 || account(result) != tc.want || p.calls != 0 {
			t.Fatal("wrong agy pin", tc.args, diag.String())
		}
		records, err := decision.Read(decision.Path(home, ""))
		if err != nil || len(records) == 0 || records[len(records)-1].Label == nil || records[len(records)-1].Label.Account != tc.want || records[len(records)-1].Family != "agy" {
			t.Fatal("agy pin decision")
		}
		onlyDecisionLog(t, home)
	}
	for _, args := range [][]string{{"--account=agy/second", "codex"}, {"--account=agy/second", "claude"}, {"--account=agy/personal", "agy"}, {"--account=codex/work", "agy"}} {
		var out, diag bytes.Buffer
		a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if a.Run(append([]string{"--dry-run"}, args...)) != 2 {
			t.Fatal("mismatched agy account accepted", args)
		}
	}
	// Status covers every configured account; usage has 6 + 2 rows with labels.
	var out, diag bytes.Buffer
	a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}}
	if a.Run([]string{"status", "--json"}) != 0 {
		t.Fatal(diag.String())
	}
	var status struct{ Accounts []map[string]any }
	if json.Unmarshal(out.Bytes(), &status) != nil || len(status.Accounts) != 6 || status.Accounts[4]["profile"] != "work" || status.Accounts[5]["profile"] != "second" {
		t.Fatal("configured status", out.String())
	}
	out.Reset()
	a.Run([]string{"usage", "--json"})
	var report usage.Report
	if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Accounts) != 8 || *report.Accounts[4].Account != "Antigravity (work)" || *report.Accounts[5].Account != "Antigravity (second)" {
		t.Fatal("configured usage rows", out.String())
	}
	// cooldown and feedback accept configured ids only.
	if a.Run([]string{"--dry-run", "cooldown", "agy/second"}) != 0 || a.Run([]string{"--dry-run", "cooldown", "agy/personal"}) != 2 {
		t.Fatal("cooldown account source")
	}
}

func TestAgyInvalidConfigFails(t *testing.T) {
	cfg := writeConfig(t, "[[tool_accounts]]\nid = \"unknown/third\"\nlabel = \"x\"\n")
	for _, args := range [][]string{{"status"}, {"usage"}, {"--dry-run", "agy"}, {"--dry-run", "fake task"}} {
		var out, diag bytes.Buffer
		a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}}
		if a.Run(args) != 2 {
			t.Fatal("invalid tool_accounts accepted", args)
		}
	}
}

func TestAgyFamilyBalancesConfiguredAccounts(t *testing.T) {
	cfg := writeConfig(t, agyAccounts("/profiles/second", ""))
	for _, tc := range []struct {
		work, second float64
		want         string
	}{{80, 10, "agy/second"}, {5, 60, "agy/work"}} {
		var out, diag bytes.Buffer
		src := &familyUsage{t: t, family: "agy", rows: map[string]usage.Data{"work": weekly(tc.work), "second": weekly(tc.second)}}
		a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: forbiddenClassifier{t}}
		if code, result := dryResult(t, a, "agy"); code != 0 || account(result) != tc.want || src.calls.Load() != 2 {
			t.Fatal("agy balance", diag.String())
		}
	}
}

func TestAgySharedPoolSelection(t *testing.T) {
	cfg := writeConfig(t, agyAccounts("/profiles/second", "pool-a"))
	run := func(src *toolUsage, classify classifier.Classifier, args ...string) (int, map[string]any) {
		var out, diag bytes.Buffer
		a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: classify}
		return dryResult(t, a, args...)
	}
	// The secondary has more headroom but is never treated as extra capacity.
	src := &toolUsage{rows: map[string]usage.Data{"agy/work": weekly(70), "agy/second": weekly(0)}}
	if code, result := run(src, forbiddenClassifier{t}, "agy"); code != 0 || account(result) != "agy/work" || src.count("agy/second") != 0 {
		t.Fatal("pool representative not used", result)
	}
	// Representative exhausted: no fallback to the other member; explicit family exits 3.
	src = &toolUsage{rows: map[string]usage.Data{"agy/work": weekly(97), "agy/second": weekly(0)}}
	if code, _ := run(src, forbiddenClassifier{t}, "agy"); code != 3 {
		t.Fatal("exhausted pool picked another member")
	}
	// Explicit pins still reach the secondary, bypassing usage.
	for _, args := range [][]string{{"agy", "second"}, {"--account", "agy/second", "agy"}, {"--account", "agy/second", "fake task"}} {
		if code, result := run(&toolUsage{}, forbiddenClassifier{t}, args...); code != 0 || account(result) != "agy/second" {
			t.Fatal("secondary pin", args)
		}
	}
	// Routing: Jev chooses agy, the pool is exhausted, selection falls through to another family.
	src = &toolUsage{rows: map[string]usage.Data{"agy/work": weekly(99), "agy/second": weekly(0)}}
	code, result := run(src, choiceClassifier{"agy"}, "fake task")
	if code != 0 || result["tool"] == "agy" || src.count("agy/second") != 0 {
		t.Fatal("exhausted pool must fall through", result)
	}
	for _, c := range result["candidates"].([]any) {
		c := c.(map[string]any)
		if c["account"] == "agy/second" && (c["chosen"] == true || c["skipped"] == nil || c["quota_pool"] != "pool-a") {
			t.Fatal("secondary candidate not skipped", c)
		}
		if c["account"] == "agy/work" && c["skipped"] != "shared quota pool exhausted" {
			t.Fatal("exhausted representative not marked", c)
		}
	}
	// Usage rows carry the pool id.
	var out, diag bytes.Buffer
	a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: usage.Unsupported{}}
	a.Run([]string{"usage", "--json"})
	var report usage.Report
	if json.Unmarshal(out.Bytes(), &report) != nil || report.Accounts[4].Extra["quota_pool"] != "pool-a" || report.Accounts[5].Extra["quota_pool"] != "pool-a" || report.Accounts[0].Extra["quota_pool"] != nil {
		t.Fatal("quota_pool rows", out.String())
	}
}

func TestAgyDifferentPoolsIndependent(t *testing.T) {
	cfg := writeConfig(t, `
[[tool_accounts]]
id = "agy/work"
label = "Antigravity (work)"
quota_pool = "pool-a"

[[tool_accounts]]
id = "agy/second"
label = "Antigravity (second)"
quota_pool = "pool-b"
`)
	src := &toolUsage{rows: map[string]usage.Data{"agy/work": weekly(97), "agy/second": weekly(30)}}
	var out, diag bytes.Buffer
	a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: forbiddenClassifier{t}}
	if code, result := dryResult(t, a, "agy"); code != 0 || account(result) != "agy/second" {
		t.Fatal("independent pool not selected", diag.String())
	}
}

func TestAgyLaunchIsolation(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	home := t.TempDir()
	profile := filepath.Join(t.TempDir(), "profiles", "second")
	cfg := writeConfig(t, agyAccounts(profile, ""))
	for _, tc := range []struct{ env, profile string }{{cfg, "second"}, {cfg, "work"}, {"SB_CONFIG=" + filepath.Join(dir, "absent.toml"), "personal"}} {
		var out, diag bytes.Buffer
		var gotArgs, gotEnv []string
		a := App{Home: home, Env: []string{"HOME=" + home, "KEEP=yes", tc.env}, Out: &out, Err: &diag, Probe: &fakeProbe{}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}, Execute: func(path string, args, env []string) (int, error) {
			if path != bin {
				t.Fatal("not the agy binary")
			}
			gotArgs, gotEnv = args, env
			return 0, nil
		}}
		if a.Run([]string{"--no-log", "agy", tc.profile, "--", "--model", "x"}) != 0 {
			t.Fatal(diag.String())
		}
		if !reflect.DeepEqual(gotArgs, []string{"--model", "x"}) || lookupEnv(gotEnv, "KEEP") != "yes" {
			t.Fatal("argv/env parity")
		}
		if tc.profile == "second" {
			if lookupEnv(gotEnv, "HOME") != profile || lookupEnv(gotEnv, "AGY_REAL_HOME") != home || lookupEnv(gotEnv, "XDG_CONFIG_HOME") != filepath.Join(profile, ".config") || lookupEnv(gotEnv, "XDG_STATE_HOME") != filepath.Join(profile, ".local", "state") {
				t.Fatal("configured home isolation")
			}
		} else if lookupEnv(gotEnv, "HOME") != home || lookupEnv(gotEnv, "XDG_CONFIG_HOME") != "" || lookupEnv(gotEnv, "AGY_REAL_HOME") != "" {
			t.Fatal("account without home had HOME rewritten", tc.profile)
		}
		if _, err := os.Stat(profile); !os.IsNotExist(err) {
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
	signedAgyOnly := profileProbe{map[string]bool{"agy/personal": true}}
	for _, family := range []string{"codex", "claude"} {
		var out, diag bytes.Buffer
		src := &toolUsage{}
		a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: choiceClassifier{family}}
		if a.Run([]string{"--dry-run", "--json", "fake task"}) != 0 {
			t.Fatal(diag.String())
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != family || result["source"] != "jev" || src.count("agy") != 0 {
			t.Fatal("Jev family not honoured", out.String())
		}
	}
	// Default codex with only Antigravity signed in: no silent fallback to agy.
	var out, diag bytes.Buffer
	a := App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: signedAgyOnly, UsageSource: forbiddenUsage{t}, Env: []string{"SB_CLASSIFIER_PROVIDER=off"}}
	if a.Run([]string{"--dry-run", "fake task"}) != 3 {
		t.Fatal("routing fell back to agy")
	}
	// Jev may explicitly choose Antigravity; usage is then fetched for agy accounts.
	out.Reset()
	src := &toolUsage{}
	a = App{Home: t.TempDir(), Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: src, Classifier: choiceClassifier{"agy"}}
	if a.Run([]string{"--dry-run", "--json", "fake task"}) != 0 {
		t.Fatal(diag.String())
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil || result["tool"] != "agy" || src.count("agy") != 1 {
		t.Fatal("Jev agy choice", out.String())
	}
}

func TestRoutedAgyTaskUsesInteractivePrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	profile := filepath.Join(t.TempDir(), "second")
	cfg := writeConfig(t, agyAccounts(profile, "pool-a"))
	for _, pin := range []string{"agy/work", "agy/second"} {
		var out, diag bytes.Buffer
		var got, env []string
		a := App{Home: t.TempDir(), Env: []string{cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: forbiddenUsage{t}, Classifier: forbiddenClassifier{t}, Execute: func(_ string, args, e []string) (int, error) {
			got, env = args, e
			return 0, nil
		}}
		if a.Run([]string{"--no-log", "--account", pin, "fake task"}) != 0 || !reflect.DeepEqual(got, []string{"--prompt-interactive", "fake task"}) {
			t.Fatal("agy task argv", got, diag.String())
		}
		if (pin == "agy/second") != (lookupEnv(env, "HOME") == profile) {
			t.Fatal("routed launch env", pin)
		}
	}
}

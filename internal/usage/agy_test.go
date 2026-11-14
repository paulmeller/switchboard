package usage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Made-up fixture: no identities, tokens or live output.
const agyFixture = `{"status":"SUCCESS","command":{"data":{"groups":[
{"name":"Claude and GPT Models","buckets":[{"window":"5h","remaining_fraction":0.25,"reset_time":"2026-11-14T05:00:00Z"}]},
{"name":"Gemini Models","buckets":[
 {"window":"5h","remaining_fraction":1.0,"reset_time":"2026-11-14T16:00:00+11:00"},
 {"window":"weekly","remaining_fraction":0.4971,"reset_time":"2026-11-18T21:06:00+11:00"},
 {"window":"daily","remaining_fraction":0.9}]},
{"name":"Fake Argon Pool","buckets":[{"window":"weekly","remaining_fraction":1.7}]}]}}}`

func TestAgyParse(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{agyFixture, "starting fake log\nPrint mode: running slash command /usage\n{\"partial\":\n" + agyFixture + "\n"} {
		d, err := parseAgyUsage([]byte(raw), now)
		if err != nil {
			t.Fatal(err)
		}
		if *d.Weekly != 50.29 || *d.Five != 0 || *d.WeeklyReset != "2026-11-18T21:06:00+11:00" || *d.FiveReset != "2026-11-14T16:00:00+11:00" || !*d.LoginOK || !strings.Contains(d.Source, "/usage") || d.Notice != "" {
			t.Fatal("Gemini primary windows", d)
		}
		pools := d.Extra["pools"].(map[string]any)
		other := pools["Claude and GPT Models"].(map[string]any)["5h"].(map[string]any)
		if d.Extra["primary_pool"] != "Gemini Models" || other["used_pct"] != 75.0 || other["resets_at"] != "2026-11-14T16:00:00+11:00" {
			t.Fatal("extra pool", pools)
		}
		if pools["Gemini Models"].(map[string]any)["daily"].(map[string]any)["used_pct"] != 10.0 {
			t.Fatal("non-primary window of primary pool")
		}
		if pools["Fake Argon Pool"].(map[string]any)["weekly"].(map[string]any)["used_pct"] != 0.0 {
			t.Fatal("clamp")
		}
	}
	// Without a Gemini group, the first group carrying a 5h/weekly window is primary.
	d, err := parseAgyUsage([]byte(`{"status":"SUCCESS","command":{"data":{"groups":[{"name":"Empty","buckets":[{"window":"daily","remaining_fraction":0.5}]},{"name":"Other","buckets":[{"window":"weekly","remaining_fraction":0}]}]}}}`), now)
	if err != nil || *d.Weekly != 100 || d.Five != nil || d.Extra["primary_pool"] != "Other" {
		t.Fatal("fallback group", d, err)
	}
	for raw, want := range map[string]error{
		`{"status":"SUCCESS","command":{"data":{"groups":[]}}}`: errAgyNoGroups,
		`{"status":"SUCCESS"}`: errAgyNoGroups,
		`{"status":"ERROR","command":{"data":{"groups":[{"name":"x"}]}}}`: errAgyStatus,
		"not json at all": errAgyPayload,
		"":                errAgyPayload,
	} {
		if _, err := parseAgyUsage([]byte(raw), now); !errors.Is(err, want) {
			t.Fatal("unsafe or wrong failure", raw, err)
		}
	}
}

func TestAgySourceEnvAndCollect(t *testing.T) {
	envs := map[string][]string{}
	runner := readFunc(func(_ context.Context, op Operation, env []string) ([]byte, error) {
		if op != AgyUsage {
			t.Fatal("operation outside allowlist")
		}
		home := ""
		for _, v := range env {
			if k, x, _ := strings.Cut(v, "="); k == "HOME" {
				home = x
			}
		}
		envs[home] = env
		if strings.HasSuffix(home, "kelly-fake") {
			return nil, errors.New("private diagnostic")
		}
		return []byte(agyFixture), nil
	})
	src := Sources{"agy": Agy{runner, "/fake", []string{"HOME=/fake", "SB_AGY_KELLY_HOME=/fake/kelly-fake"}}}
	if src.Timeout("agy") != AgyTimeout || src.Timeout("codex") != 0 {
		t.Fatal("agy timeout")
	}
	report, code := Collect(context.Background(), src, time.Millisecond*500, time.Now())
	personal, kelly := report.Accounts[4], report.Accounts[5]
	if code != 0 || personal.Tool != "agy" || personal.Profile != "personal" || kelly.Profile != "kelly" || *kelly.Account != "Antigravity (Kelly personal)" || *personal.Account != "Antigravity (personal)" || personal.Plan != nil {
		t.Fatal("agy rows")
	}
	if personal.WeeklyUsedPct == nil || *personal.WeeklyUsedPct != 50.29 || personal.Source == nil || personal.Error != nil {
		t.Fatal("personal usage")
	}
	if kelly.WeeklyUsedPct != nil || kelly.LoginOK != nil || kelly.Error == nil || *kelly.Error != string(errAgyUnavailable) {
		t.Fatal("kelly failure must be null with safe error")
	}
	if envs["/fake/kelly-fake"] == nil || envs["/fake"] == nil || !contains(envs["/fake/kelly-fake"], "XDG_CONFIG_HOME=/fake/kelly-fake/.config") || contains(envs["/fake"], "XDG_CONFIG_HOME=/fake/kelly-fake/.config") {
		t.Fatal("account HOME isolation")
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "private") {
		t.Fatal("raw diagnostic escaped")
	}
}

type slowTimed struct{}

func (slowTimed) Read(ctx context.Context, _, _ string) (Data, error) {
	select {
	case <-time.After(30 * time.Millisecond):
		yes := true
		return Data{ObservedAt: time.Now(), Source: "fake", LoginOK: &yes}, nil
	case <-ctx.Done():
		return Data{}, ctx.Err()
	}
}
func (slowTimed) Timeout(string) time.Duration { return time.Second }

func TestTimedSourceRaisesRowTimeout(t *testing.T) {
	report, code := CollectRows(context.Background(), slowTimed{}, time.Millisecond, time.Now(), []Row{{Tool: "agy", Profile: "kelly", Extra: map[string]any{}}})
	if code != 0 || report.Accounts[0].Error != nil {
		t.Fatal("per-tool timeout not applied")
	}
}

// The real runner may only spawn the exact print-mode /usage slash command.
func TestAgyCommandAllowlist(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$*\" != '--output-format json --print /usage' ]; then exit 9; fi\necho 'fake log line' >&2\necho 'Print mode: running slash command /usage'\nprintf '%s\\n' '{\"status\":\"SUCCESS\",\"command\":{\"data\":{\"groups\":[{\"name\":\"Gemini Models\",\"buckets\":[{\"window\":\"weekly\",\"remaining_fraction\":0.5}]}]}}}'\n"
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	home := t.TempDir()
	d, err := (Agy{Commands{}, home, []string{"PATH=" + dir, "HOME=" + home}}).Read(context.Background(), "agy", "kelly")
	if err != nil || *d.Weekly != 50 {
		t.Fatal("allowlisted command", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini-kelly")); !os.IsNotExist(err) {
		t.Fatal("usage created profile home")
	}
	if _, err := (Agy{Commands{}, home, nil}).Read(context.Background(), "agy", "work"); !errors.Is(err, errAgyProfile) {
		t.Fatal("unknown agy profile accepted")
	}
}

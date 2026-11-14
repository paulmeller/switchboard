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

	"switchboard/internal/provider"
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

// fixtureAccounts is a generic two-account config: work keeps the process HOME,
// second uses an isolated profile HOME and a shared quota pool.
func fixtureAccounts(secondHome string) provider.Registry {
	return provider.NewRegistry([]provider.Spec{
		{Account: provider.Account{Tool: "agy", Profile: "work"}, Label: "Antigravity (work)"},
		{Account: provider.Account{Tool: "agy", Profile: "second"}, Label: "Antigravity (second)", Home: secondHome, QuotaPool: "pool-a"},
	})
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
		if strings.HasSuffix(home, "second-fake") {
			return nil, errors.New("private diagnostic")
		}
		return []byte(agyFixture), nil
	})
	reg := fixtureAccounts("/fake/second-fake")
	src := Sources{"agy": Agy{runner, "/fake", []string{"HOME=/fake"}, reg}}
	if src.Timeout("agy") != AgyTimeout || src.Timeout("codex") != 0 {
		t.Fatal("agy timeout")
	}
	report, code := Collect(context.Background(), src, reg, time.Millisecond*500, time.Now())
	if len(report.Accounts) != 8 {
		t.Fatal("row count must be 6 + configured agy accounts")
	}
	work, second := report.Accounts[4], report.Accounts[5]
	if code != 0 || work.Tool != "agy" || work.Profile != "work" || second.Profile != "second" || *second.Account != "Antigravity (second)" || *work.Account != "Antigravity (work)" || work.Plan != nil {
		t.Fatal("agy rows")
	}
	if report.Accounts[6].Tool != "grok" || report.Accounts[7].Profile != "plain" {
		t.Fatal("trailing rows")
	}
	if work.WeeklyUsedPct == nil || *work.WeeklyUsedPct != 50.29 || work.Source == nil || work.Error != nil || work.Extra["quota_pool"] != nil {
		t.Fatal("work usage")
	}
	if second.WeeklyUsedPct != nil || second.LoginOK != nil || second.Error == nil || *second.Error != string(errAgyUnavailable) || second.Extra["quota_pool"] != "pool-a" {
		t.Fatal("second failure must be null with safe error and keep its pool")
	}
	if envs["/fake/second-fake"] == nil || envs["/fake"] == nil || !contains(envs["/fake/second-fake"], "XDG_CONFIG_HOME=/fake/second-fake/.config") || contains(envs["/fake"], "XDG_CONFIG_HOME=/fake/second-fake/.config") {
		t.Fatal("account HOME isolation")
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "private") {
		t.Fatal("raw diagnostic escaped")
	}
}

func TestAgyRowsDefaultAndPool(t *testing.T) {
	rows := Rows(provider.NewRegistry(nil))
	if len(rows) != 7 || rows[4].Tool != "agy" || rows[4].Profile != "personal" || *rows[4].Account != "Antigravity" || len(rows[4].Extra) != 0 {
		t.Fatal("implicit default row")
	}
	now := time.Now()
	runner := readFunc(func(context.Context, Operation, []string) ([]byte, error) { return []byte(agyFixture), nil })
	reg := fixtureAccounts("/fake/second")
	report, _ := Collect(context.Background(), Sources{"agy": Agy{runner, "/fake", nil, reg}}, reg, time.Second, now)
	second := report.Accounts[5]
	if second.Extra["quota_pool"] != "pool-a" || second.Extra["primary_pool"] != "Gemini Models" || second.Extra["pools"] == nil {
		t.Fatal("quota_pool must sit beside pools/primary_pool")
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
	report, code := CollectRows(context.Background(), slowTimed{}, time.Millisecond, time.Now(), []Row{{Tool: "agy", Profile: "second", Extra: map[string]any{}}})
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
	profile := filepath.Join(home, "agy-second")
	reg := fixtureAccounts(profile)
	d, err := (Agy{Commands{}, home, []string{"PATH=" + dir, "HOME=" + home}, reg}).Read(context.Background(), "agy", "second")
	if err != nil || *d.Weekly != 50 {
		t.Fatal("allowlisted command", err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("usage created profile home")
	}
	if _, err := (Agy{Commands{}, home, nil, reg}).Read(context.Background(), "agy", "personal"); !errors.Is(err, errAgyProfile) {
		t.Fatal("unknown agy profile accepted")
	}
}

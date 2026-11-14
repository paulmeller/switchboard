package picker

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"
)

func ptr[T any](v T) *T { return &v }
func TestRanking(t *testing.T) {
	now := time.Date(2026, time.November, 11, 21, 0, 0, 0, time.UTC)
	reset := func(after time.Duration) *string { return ptr(now.Add(after).Format(time.RFC3339)) }
	for _, tc := range []struct {
		name           string
		work, personal usage.Row
		want, reason   string
	}{
		{"binding max", usage.Row{WeeklyUsedPct: ptr(10.), FiveHourUsedPct: ptr(90.)}, usage.Row{WeeklyUsedPct: ptr(40.), FiveHourUsedPct: ptr(40.)}, "personal", "most headroom: 60% left"},
		{"exhausted sibling", usage.Row{WeeklyUsedPct: ptr(95.)}, usage.Row{WeeklyUsedPct: ptr(90.)}, "personal", "most headroom: 10% left"},
		{"known above unknown", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(90.)}, "personal", "most headroom: 10% left"},
		{"small under cap headroom above unknown", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(94.)}, "personal", "most headroom: 6% left"},
		{"unknown above exhausted", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(95.)}, "work", "unknown usage, ranked below known headroom"},
		{"unknown above fully used", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(100.)}, "work", "unknown usage, ranked below known headroom"},
		{"today exhausted before reset", usage.Row{WeeklyUsedPct: ptr(97.), WeeklyResetsAt: reset(40 * time.Minute)}, usage.Row{WeeklyUsedPct: ptr(13.), WeeklyResetsAt: reset(7 * 24 * time.Hour)}, "personal", "most headroom: 87% left"},
		{"harvest", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(2 * time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.), WeeklyResetsAt: reset(7 * 24 * time.Hour)}, "work", "expiring soon: resets 10:00 AEDT, 30% left"},
		{"reset days away", usage.Row{WeeklyUsedPct: ptr(80.), WeeklyResetsAt: reset(48 * time.Hour)}, usage.Row{WeeklyUsedPct: ptr(50.)}, "personal", "most headroom: 50% left"},
		{"binding window reset only", usage.Row{WeeklyUsedPct: ptr(85.), WeeklyResetsAt: reset(7 * time.Hour), FiveHourUsedPct: ptr(10.), FiveHourResetsAt: reset(30 * time.Minute)}, usage.Row{WeeklyUsedPct: ptr(40.)}, "personal", "most headroom: 60% left"},
		{"higher five hour binds even without reset", usage.Row{WeeklyUsedPct: ptr(10.), WeeklyResetsAt: reset(time.Hour), FiveHourUsedPct: ptr(85.)}, usage.Row{WeeklyUsedPct: ptr(40.)}, "personal", "most headroom: 60% left"},
		{"five hour binding reset", usage.Row{WeeklyUsedPct: ptr(10.), WeeklyResetsAt: reset(7 * time.Hour), FiveHourUsedPct: ptr(70.), FiveHourResetsAt: reset(time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "work", "expiring soon: resets 09:00 AEDT, 30% left"},
		{"reset without known usage ignored", usage.Row{FiveHourResetsAt: reset(time.Minute), WeeklyUsedPct: ptr(70.)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "personal", "most headroom: 87% left"},
		{"below harvest minimum", usage.Row{WeeklyUsedPct: ptr(94.), WeeklyResetsAt: reset(time.Minute)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "personal", "most headroom: 87% left"},
		{"non eligible sooner reset loses", usage.Row{WeeklyUsedPct: ptr(94.), WeeklyResetsAt: reset(time.Minute)}, usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(time.Hour)}, "personal", "expiring soon: resets 09:00 AEDT, 30% left"},
		{"reset in past", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(-time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "personal", "most headroom: 87% left"},
		{"reset now", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(0)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "personal", "most headroom: 87% left"},
		{"invalid reset", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: ptr("invalid")}, usage.Row{WeeklyUsedPct: ptr(13.)}, "personal", "most headroom: 87% left"},
		{"window boundary inclusive", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(6 * time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "work", "expiring soon: resets 14:00 AEDT, 30% left"},
		{"just outside window", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(6*time.Hour + time.Second)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "personal", "most headroom: 87% left"},
		{"equal headroom personal resets sooner", usage.Row{WeeklyUsedPct: ptr(40.), WeeklyResetsAt: reset(time.Hour)}, usage.Row{WeeklyUsedPct: ptr(40.), WeeklyResetsAt: reset(time.Minute)}, "personal", "expiring soon: resets 08:01 AEDT, 60% left"},
		{"known reset first at minimum", usage.Row{WeeklyUsedPct: ptr(10.)}, usage.Row{WeeklyUsedPct: ptr(90.), WeeklyResetsAt: reset(time.Hour)}, "personal", "expiring soon: resets 09:00 AEDT, 10% left"},
		{"equal windows bind sooner five hour", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(7 * time.Hour), FiveHourUsedPct: ptr(70.), FiveHourResetsAt: reset(time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "work", "expiring soon: resets 09:00 AEDT, 30% left"},
		{"equal windows bind sooner weekly", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(time.Hour), FiveHourUsedPct: ptr(70.), FiveHourResetsAt: reset(7 * time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "work", "expiring soon: resets 09:00 AEDT, 30% left"},
		{"equal windows bind known reset", usage.Row{WeeklyUsedPct: ptr(70.), FiveHourUsedPct: ptr(70.), FiveHourResetsAt: reset(time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.)}, "work", "expiring soon: resets 09:00 AEDT, 30% left"},
		{"harvest tie work before personal", usage.Row{WeeklyUsedPct: ptr(70.), WeeklyResetsAt: reset(time.Hour)}, usage.Row{WeeklyUsedPct: ptr(13.), WeeklyResetsAt: reset(time.Hour)}, "work", "expiring soon: resets 09:00 AEDT, 30% left"},
		{"all exhausted reset", usage.Row{WeeklyUsedPct: ptr(95.), WeeklyResetsAt: reset(time.Hour), FiveHourUsedPct: ptr(10.), FiveHourResetsAt: reset(0)}, usage.Row{FiveHourUsedPct: ptr(100.), FiveHourResetsAt: reset(time.Minute)}, "work", "all exhausted; most headroom: 5% left"},
		{"all exhausted most headroom", usage.Row{WeeklyUsedPct: ptr(97.), WeeklyResetsAt: reset(time.Minute)}, usage.Row{WeeklyUsedPct: ptr(95.)}, "personal", "all exhausted; most headroom: 5% left"},
		{"both fully used reset", usage.Row{WeeklyUsedPct: ptr(100.), WeeklyResetsAt: reset(time.Hour), FiveHourUsedPct: ptr(10.), FiveHourResetsAt: reset(0)}, usage.Row{FiveHourUsedPct: ptr(100.), FiveHourResetsAt: reset(time.Minute)}, "personal", "all fully used; soonest reset 08:01 AEDT"},
		{"fully used missing reset last", usage.Row{WeeklyUsedPct: ptr(100.)}, usage.Row{FiveHourUsedPct: ptr(100.), FiveHourResetsAt: reset(time.Minute)}, "personal", "all fully used; soonest reset 08:01 AEDT"},
		{"fully used tie", usage.Row{WeeklyUsedPct: ptr(100.)}, usage.Row{WeeklyUsedPct: ptr(100.)}, "work", "all exhausted; reset unknown"},
		{"stable tie", usage.Row{WeeklyUsedPct: ptr(40.)}, usage.Row{WeeklyUsedPct: ptr(40.)}, "work", "most headroom: 60% left"},
		{"non harvest resets do not break headroom tie", usage.Row{WeeklyUsedPct: ptr(40.)}, usage.Row{WeeklyUsedPct: ptr(40.), WeeklyResetsAt: reset(48 * time.Hour)}, "work", "most headroom: 60% left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.work.Tool, tc.work.Profile = "codex", "work"
			tc.personal.Tool, tc.personal.Profile = "codex", "personal"
			statuses := []provider.Status{}
			for _, a := range provider.Accounts() {
				statuses = append(statuses, provider.Status{Account: a, LoginOK: ptr(true)})
			}
			pick, reason, candidates := Pick(provider.NewRegistry(nil), []string{"codex", "claude"}, statuses, nil, []usage.Row{tc.work, tc.personal}, 95, 10, 6, now)
			if pick.Tool != "codex" || pick.Profile != tc.want || reason != tc.reason {
				t.Fatalf("got %v (%s), want codex/%s (%s)", pick, reason, tc.want, tc.reason)
			}
			chosen := 0
			for _, c := range candidates {
				if c.Chosen {
					chosen++
				}
				exhausted := (c.Weekly != nil && *c.Weekly >= 95) || (c.Five != nil && *c.Five >= 95)
				if c.Exhausted != exhausted {
					t.Fatal("incorrect exhaustion flag")
				}
			}
			if chosen != 1 {
				t.Fatal("chosen")
			}
		})
	}
	statuses := []provider.Status{{Account: provider.Account{Tool: "claude", Profile: "personal"}, LoginOK: ptr(true)}}
	p, _, _ := Pick(provider.NewRegistry(nil), []string{"codex", "claude"}, statuses, nil, nil, 95, 10, 6, now)
	if p.Tool != "claude" {
		t.Fatal("family fallback")
	}
	p, _, _ = Pick(provider.NewRegistry(nil), []string{"codex", "claude"}, statuses, state.State{"claude/personal": now.Add(time.Hour)}, nil, 95, 10, 6, now)
	if p.Valid() {
		t.Fatal("cooldown must exclude")
	}
}

func TestRankingOverrides(t *testing.T) {
	now := time.Date(2026, time.November, 11, 21, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                string
		cap, minimum, hours float64
		used                float64
		want                string
	}{
		{"defaults", 95, 10, 6, 70, "work"},
		{"harvest disabled", 95, 10, 0, 70, "personal"},
		{"shorter window", 95, 10, 0.5, 70, "personal"},
		{"longer window", 95, 10, 48, 70, "work"},
		{"higher minimum", 95, 31, 6, 70, "personal"},
		{"lower minimum", 95, 6, 6, 94, "work"},
		{"zero minimum", 95, 0, 6, 94, "work"},
		{"cap override", 70, 10, 6, 70, "personal"},
		{"raised cap still respects minimum", 100, 10, 6, 97, "personal"},
		{"raised cap lower minimum", 100, 3, 6, 97, "work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset := now.Add(time.Hour).Format(time.RFC3339)
			statuses := []provider.Status{
				{Account: provider.Account{Tool: "codex", Profile: "work"}, LoginOK: ptr(true)},
				{Account: provider.Account{Tool: "codex", Profile: "personal"}, LoginOK: ptr(true)},
			}
			rows := []usage.Row{
				{Tool: "codex", Profile: "work", WeeklyUsedPct: ptr(tc.used), WeeklyResetsAt: &reset},
				{Tool: "codex", Profile: "personal", WeeklyUsedPct: ptr(13.)},
			}
			pick, _, _ := Pick(provider.NewRegistry(nil), []string{"codex"}, statuses, nil, rows, tc.cap, tc.minimum, tc.hours, now)
			if pick.Profile != tc.want {
				t.Fatalf("got %s, want %s", pick.Profile, tc.want)
			}
		})
	}
}

type fakeSource struct {
	calls atomic.Int32
	fail  bool
	value float64
}

func (f *fakeSource) Read(ctx context.Context, _, _ string) (usage.Data, error) {
	f.calls.Add(1)
	if f.fail {
		<-ctx.Done()
		return usage.Data{}, ctx.Err()
	}
	return usage.Data{ObservedAt: time.Now(), Source: "fake", Weekly: ptr(f.value)}, nil
}

func TestCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "usage-cache.json")
	now := time.Now()
	src := &fakeSource{value: 20}
	rows := []usage.Row{{Tool: "codex", Profile: "work", Extra: map[string]any{}}}
	fetch := func(at time.Time, write bool) []usage.Row {
		return Cached(context.Background(), src, path, rows, time.Minute, time.Millisecond, at, write)
	}
	fetch(now, true)
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("cache permissions")
	}
	info, e = os.Stat(filepath.Dir(path))
	if e != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("parent permissions")
	}
	src.value = 40
	got := fetch(now.Add(time.Second), false)
	if src.calls.Load() != 1 || *got[0].WeeklyUsedPct != 20 {
		t.Fatal("fresh cache")
	}
	got = fetch(now.Add(2*time.Minute), false)
	if src.calls.Load() != 2 || *got[0].WeeklyUsedPct != 40 {
		t.Fatal("stale cache must fetch")
	}
	src.fail = true
	got = fetch(now.Add(2*time.Minute), false)
	if src.calls.Load() != 3 || *got[0].WeeklyUsedPct != 20 {
		t.Fatal("timeout stale fallback")
	}
	got = fetch(now.Add(7*time.Minute), false)
	if got[0].WeeklyUsedPct != nil {
		t.Fatal("too stale")
	}
	got = Cached(context.Background(), src, filepath.Join(t.TempDir(), "missing"), rows, time.Minute, time.Millisecond, now, false)
	if got[0].WeeklyUsedPct != nil {
		t.Fatal("failure unknown")
	}
	b, _ := os.ReadFile(path)
	if len(b) == 0 {
		t.Fatal("cache")
	}
}

func poolRegistry() provider.Registry {
	return provider.NewRegistry([]provider.Spec{
		{Account: provider.Account{Tool: "agy", Profile: "work"}, Label: "Antigravity (work)", QuotaPool: "pool-a"},
		{Account: provider.Account{Tool: "agy", Profile: "second"}, Label: "Antigravity (second)", Home: "/profiles/second", QuotaPool: "pool-a"},
		{Account: provider.Account{Tool: "agy", Profile: "third"}, Label: "Antigravity (third)", QuotaPool: "pool-b"},
		{Account: provider.Account{Tool: "agy", Profile: "solo"}, Label: "Antigravity (solo)"},
	})
}

func TestSharedQuotaPools(t *testing.T) {
	now := time.Date(2026, time.November, 11, 21, 0, 0, 0, time.UTC)
	reg := poolRegistry()
	signed := func(ids ...string) []provider.Status {
		out := []provider.Status{}
		for _, a := range reg.Accounts() {
			ok := len(ids) == 0
			for _, id := range ids {
				ok = ok || a.ID() == id
			}
			out = append(out, provider.Status{Account: a, LoginOK: ptr(ok)})
		}
		return out
	}
	row := func(profile string, used float64) usage.Row {
		return usage.Row{Tool: "agy", Profile: profile, WeeklyUsedPct: ptr(used)}
	}
	for _, tc := range []struct {
		name     string
		statuses []provider.Status
		rows     []usage.Row
		order    []string
		want     string
	}{
		// The secondary has far more headroom, but is never extra capacity.
		{"representative only", signed("agy/work", "agy/second"), []usage.Row{row("work", 60), row("second", 0)}, []string{"agy"}, "agy/work"},
		// Exhausted representative: no other pool member, no eligible agy account.
		{"exhausted pool exits", signed("agy/work", "agy/second"), []usage.Row{row("work", 97), row("second", 0)}, []string{"agy"}, ""},
		{"fully used pool exits", signed("agy/work", "agy/second"), []usage.Row{row("work", 100), row("second", 0)}, []string{"agy"}, ""},
		// Exhausted pool falls through to an independent pool or unpooled account.
		{"other pool independent", signed(), []usage.Row{row("work", 97), row("second", 0), row("third", 50), row("solo", 80)}, []string{"agy"}, "agy/third"},
		{"unpooled independent", signed("agy/work", "agy/second", "agy/solo"), []usage.Row{row("work", 99), row("second", 0), row("solo", 96)}, []string{"agy"}, "agy/solo"},
		{"pools compete on headroom", signed(), []usage.Row{row("work", 10), row("second", 90), row("third", 50), row("solo", 80)}, []string{"agy"}, "agy/work"},
		// Secondary signed in alone is not eligible automatically.
		{"secondary alone", signed("agy/second"), []usage.Row{row("second", 0)}, []string{"agy"}, ""},
		// Routing: an exhausted pool falls through to another family.
		{"routing falls through", signed("agy/work", "agy/second", "codex/work"), []usage.Row{row("work", 96), row("second", 0)}, []string{"agy", "codex"}, "codex/work"},
		// Unknown usage keeps the representative eligible.
		{"unknown representative", signed("agy/work", "agy/second"), []usage.Row{row("second", 0)}, []string{"agy"}, "agy/work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pick, _, candidates := Pick(reg, tc.order, tc.statuses, nil, tc.rows, 95, 10, 6, now)
			got := ""
			if pick.Valid() {
				got = pick.ID()
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for _, c := range candidates {
				if c.Account == "agy/second" && (c.Chosen || c.Skipped == nil || c.QuotaPool == nil || *c.QuotaPool != "pool-a") {
					t.Fatal("pool secondary must be skipped and labelled")
				}
			}
		})
	}
}

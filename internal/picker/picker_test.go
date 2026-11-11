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
	now := time.Now()
	for _, tc := range []struct {
		name           string
		work, personal usage.Row
		want           string
	}{
		{"binding max", usage.Row{WeeklyUsedPct: ptr(10.), FiveHourUsedPct: ptr(90.)}, usage.Row{WeeklyUsedPct: ptr(40.), FiveHourUsedPct: ptr(40.)}, "personal"},
		{"exhausted sibling", usage.Row{WeeklyUsedPct: ptr(95.)}, usage.Row{WeeklyUsedPct: ptr(90.)}, "personal"},
		{"known above unknown", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(90.)}, "personal"},
		{"remaining above unknown", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(95.)}, "personal"},
		{"unknown above exhausted", usage.Row{}, usage.Row{WeeklyUsedPct: ptr(100.)}, "work"},
		{"use before reset", usage.Row{WeeklyUsedPct: ptr(97.), WeeklyResetsAt: ptr(now.Add(time.Hour).Format(time.RFC3339))}, usage.Row{WeeklyUsedPct: ptr(12.), WeeklyResetsAt: ptr(now.Add(7 * 24 * time.Hour).Format(time.RFC3339))}, "work"},
		{"equal headroom personal resets sooner", usage.Row{WeeklyUsedPct: ptr(40.), WeeklyResetsAt: ptr(now.Add(time.Hour).Format(time.RFC3339))}, usage.Row{WeeklyUsedPct: ptr(40.), WeeklyResetsAt: ptr(now.Add(time.Minute).Format(time.RFC3339))}, "personal"},
		{"known reset first", usage.Row{WeeklyUsedPct: ptr(10.)}, usage.Row{WeeklyUsedPct: ptr(90.), WeeklyResetsAt: ptr(now.Add(time.Hour).Format(time.RFC3339))}, "personal"},
		{"all exhausted reset", usage.Row{WeeklyUsedPct: ptr(95.), WeeklyResetsAt: ptr(now.Add(time.Hour).Format(time.RFC3339)), FiveHourUsedPct: ptr(10.), FiveHourResetsAt: ptr(now.Format(time.RFC3339))}, usage.Row{FiveHourUsedPct: ptr(100.), FiveHourResetsAt: ptr(now.Add(time.Minute).Format(time.RFC3339))}, "work"},
		{"both fully used reset", usage.Row{WeeklyUsedPct: ptr(100.), WeeklyResetsAt: ptr(now.Add(time.Hour).Format(time.RFC3339)), FiveHourUsedPct: ptr(10.), FiveHourResetsAt: ptr(now.Format(time.RFC3339))}, usage.Row{FiveHourUsedPct: ptr(100.), FiveHourResetsAt: ptr(now.Add(time.Minute).Format(time.RFC3339))}, "personal"},
		{"fully used missing reset last", usage.Row{WeeklyUsedPct: ptr(100.)}, usage.Row{FiveHourUsedPct: ptr(100.), FiveHourResetsAt: ptr(now.Add(time.Minute).Format(time.RFC3339))}, "personal"},
		{"stable tie", usage.Row{WeeklyUsedPct: ptr(40.)}, usage.Row{WeeklyUsedPct: ptr(40.)}, "work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.work.Tool, tc.work.Profile = "codex", "work"
			tc.personal.Tool, tc.personal.Profile = "codex", "personal"
			statuses := []provider.Status{}
			for _, a := range provider.Accounts() {
				statuses = append(statuses, provider.Status{Account: a, LoginOK: ptr(true)})
			}
			pick, _, c := Pick([]string{"codex", "claude"}, statuses, nil, []usage.Row{tc.work, tc.personal}, 95, now)
			if pick.Tool != "codex" || pick.Profile != tc.want {
				t.Fatal(pick)
			}
			chosen := 0
			for _, v := range c {
				if v.Chosen {
					chosen++
				}
			}
			if chosen != 1 {
				t.Fatal("chosen")
			}
		})
	}
	statuses := []provider.Status{{Account: provider.Account{Tool: "claude", Profile: "personal"}, LoginOK: ptr(true)}}
	p, _, _ := Pick([]string{"codex", "claude"}, statuses, nil, nil, 95, now)
	if p.Tool != "claude" {
		t.Fatal("family fallback")
	}
	p, _, _ = Pick([]string{"codex", "claude"}, statuses, state.State{"claude/personal": now.Add(time.Hour)}, nil, 95, now)
	if p.Valid() {
		t.Fatal("cooldown must exclude")
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

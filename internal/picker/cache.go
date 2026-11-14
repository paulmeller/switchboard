package picker

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"time"

	"switchboard/internal/state"
	"switchboard/internal/usage"
)

type cachedRow struct {
	Tool        string    `json:"tool"`
	Profile     string    `json:"profile"`
	At          time.Time `json:"at"`
	Weekly      *float64  `json:"weekly_used_pct"`
	WeeklyReset *string   `json:"weekly_resets_at"`
	Five        *float64  `json:"five_hour_used_pct"`
	FiveReset   *string   `json:"five_hour_resets_at"`
}

// Cached collects only needed rows. Failed fetches may use entries up to TTL+5m old.
// Persistence is best effort; dry selections pass write=false.
func Cached(ctx context.Context, source usage.Source, path string, rows []usage.Row, ttl, timeout time.Duration, now time.Time, write bool) []usage.Row {
	entries := []cachedRow{}
	if b, e := os.ReadFile(path); e == nil {
		if json.Unmarshal(b, &entries) != nil {
			entries = nil
		}
	}
	out := []usage.Row{}
	for _, r := range rows {
		var old *cachedRow
		for i := range entries {
			e := &entries[i]
			if e.Tool == r.Tool && e.Profile == r.Profile && !e.At.After(now) && validCache(*e) {
				old = e
				break
			}
		}
		apply := func(e cachedRow) usage.Row {
			r.WeeklyUsedPct = e.Weekly
			r.WeeklyResetsAt = e.WeeklyReset
			r.FiveHourUsedPct = e.Five
			r.FiveHourResetsAt = e.FiveReset
			r.CheckedAt = e.At.UTC().Format(time.RFC3339)
			return r
		}
		if old != nil && now.Sub(old.At) < ttl {
			out = append(out, apply(*old))
			continue
		}
		report, code := usage.CollectRows(ctx, source, timeout, now, []usage.Row{r})
		fetched := report.Accounts[0]
		if code != 0 && old != nil && now.Sub(old.At) <= ttl+5*time.Minute {
			out = append(out, apply(*old))
			continue
		}
		out = append(out, fetched)
		if code == 0 {
			e := cachedRow{r.Tool, r.Profile, now, fetched.WeeklyUsedPct, fetched.WeeklyResetsAt, fetched.FiveHourUsedPct, fetched.FiveHourResetsAt}
			if old != nil {
				*old = e
			} else {
				entries = append(entries, e)
			}
		}
	}
	if write {
		_ = state.SaveJSON(path, entries)
	}
	return out
}

func validCache(e cachedRow) bool {
	for _, v := range []*float64{e.Weekly, e.Five} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100) {
			return false
		}
	}
	for _, v := range []*string{e.WeeklyReset, e.FiveReset} {
		if v != nil {
			if _, err := time.Parse(time.RFC3339, *v); err != nil {
				return false
			}
		}
	}
	return !e.At.IsZero()
}

// Package picker ranks launch accounts using status, cooldowns and normalized usage.
package picker

import (
	"fmt"
	"sort"
	"time"

	"switchboard/internal/provider"
	"switchboard/internal/state"
	"switchboard/internal/usage"
)

type Candidate struct {
	Account     string   `json:"account"`
	Login       *bool    `json:"login_ok"`
	Cooldown    bool     `json:"cooldown"`
	Weekly      *float64 `json:"weekly_used_pct"`
	WeeklyReset *string  `json:"weekly_resets_at"`
	Five        *float64 `json:"five_hour_used_pct"`
	FiveReset   *string  `json:"five_hour_resets_at"`
	Exhausted   bool     `json:"exhausted"`
	Headroom    *float64 `json:"headroom"`
	Chosen      bool     `json:"chosen"`
	account     provider.Account
	reset       time.Time
}

func Pick(order []string, statuses []provider.Status, cooldown state.State, rows []usage.Row, cap float64, now time.Time) (provider.Account, string, []Candidate) {
	candidates := []Candidate{}
	for _, a := range provider.Accounts() {
		c := Candidate{Account: a.ID(), account: a, Cooldown: !cooldown.Eligible(a, now)}
		for _, s := range statuses {
			if s.Account == a {
				c.Login = s.LoginOK
			}
		}
		for _, r := range rows {
			if r.Tool == a.Tool && r.Profile == a.Profile {
				c.Weekly, c.Five, c.WeeklyReset, c.FiveReset = r.WeeklyUsedPct, r.FiveHourUsedPct, r.WeeklyResetsAt, r.FiveHourResetsAt
			}
		}
		for _, w := range []struct {
			used  *float64
			reset *string
		}{{c.Weekly, c.WeeklyReset}, {c.Five, c.FiveReset}} {
			if w.used != nil {
				h := 100 - *w.used
				if c.Headroom == nil || h < *c.Headroom {
					c.Headroom = &h
				}
				if *w.used >= cap {
					c.Exhausted = true
				}
			}
		}
		for _, w := range []struct {
			used  *float64
			reset *string
		}{{c.Weekly, c.WeeklyReset}, {c.Five, c.FiveReset}} {
			if w.reset != nil && (!c.Exhausted || (w.used != nil && *w.used >= cap)) {
				t, e := time.Parse(time.RFC3339, *w.reset)
				if e == nil && (c.reset.IsZero() || t.Before(c.reset)) {
					c.reset = t
				}
			}
		}
		candidates = append(candidates, c)
	}
	for _, family := range order {
		indexes := []int{}
		for i, c := range candidates {
			if c.account.Tool == family && c.Login != nil && *c.Login && !c.Cooldown {
				indexes = append(indexes, i)
			}
		}
		rank := func(c Candidate) int {
			if c.Headroom == nil {
				return 1
			}
			if *c.Headroom <= 0 {
				return 2
			}
			return 0
		}
		sort.SliceStable(indexes, func(i, j int) bool {
			a, b := candidates[indexes[i]], candidates[indexes[j]]
			if rank(a) != rank(b) {
				return rank(a) < rank(b)
			}
			if a.reset.IsZero() != b.reset.IsZero() {
				return !a.reset.IsZero()
			}
			if !a.reset.Equal(b.reset) {
				return a.reset.Before(b.reset)
			}
			return rank(a) == 0 && *a.Headroom > *b.Headroom
		})
		if len(indexes) == 0 {
			continue
		}
		c := &candidates[indexes[0]]
		c.Chosen = true
		reason := "unknown usage, ranked below known headroom"
		pct := func(v *float64) string {
			if v == nil {
				return "unknown"
			}
			return fmt.Sprintf("%g%%", *v)
		}
		if c.Headroom != nil {
			reason = fmt.Sprintf("most headroom: weekly %s, 5h %s", pct(c.Weekly), pct(c.Five))
		}
		if c.Headroom != nil && *c.Headroom <= 0 {
			reason = "all exhausted; reset unknown"
		}
		if c.Headroom != nil && !c.reset.IsZero() {
			loc, _ := time.LoadLocation("Australia/Sydney")
			reason = "soonest reset " + c.reset.In(loc).Format("15:04 MST")
		}
		return c.account, reason, candidates
	}
	return provider.Account{}, "no eligible signed-in account", candidates
}

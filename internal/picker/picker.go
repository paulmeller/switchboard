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
	QuotaPool   *string  `json:"quota_pool,omitempty"`
	// Skipped explains exclusion from automatic selection by quota-pool rules.
	Skipped *string `json:"skipped,omitempty"`
	account provider.Account
	reset   time.Time
}

// Shared quota pools: only the pool representative (first configured member)
// is a candidate, and an exhausted representative is excluded rather than
// replaced by another member, so selection falls through to the next family.
const (
	skipPoolMember    = "shared quota pool; not the pool representative"
	skipPoolExhausted = "shared quota pool exhausted"
)

func Pick(reg provider.Registry, order []string, statuses []provider.Status, cooldown state.State, rows []usage.Row, cap, minHeadroom, windowHours float64, now time.Time) (provider.Account, string, []Candidate) {
	candidates := []Candidate{}
	for _, spec := range reg {
		a := spec.Account
		c := Candidate{Account: a.ID(), account: a, Cooldown: !cooldown.Eligible(a, now)}
		if spec.QuotaPool != "" {
			pool := spec.QuotaPool
			c.QuotaPool = &pool
		}
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
				reset := time.Time{}
				if w.reset != nil {
					reset, _ = time.Parse(time.RFC3339, *w.reset)
				}
				// Bind to the highest usage; equal usage binds to the sooner known reset.
				if c.Headroom == nil || h < *c.Headroom || (h == *c.Headroom && !reset.IsZero() && (c.reset.IsZero() || reset.Before(c.reset))) {
					c.Headroom = &h
					c.reset = reset
				}
				if *w.used >= cap {
					c.Exhausted = true
				}
			}
		}
		if !reg.Auto(a) {
			skip := skipPoolMember
			c.Skipped = &skip
		} else if c.QuotaPool != nil && c.Exhausted {
			skip := skipPoolExhausted
			c.Skipped = &skip
		}
		candidates = append(candidates, c)
	}
	harvest := func(c Candidate) bool {
		return c.Headroom != nil && *c.Headroom > 0 && !c.Exhausted && *c.Headroom >= minHeadroom &&
			windowHours > 0 && c.reset.After(now) && c.reset.Sub(now).Hours() <= windowHours
	}
	for _, family := range order {
		indexes := []int{}
		for i, c := range candidates {
			if c.account.Tool == family && c.Login != nil && *c.Login && !c.Cooldown && c.Skipped == nil {
				indexes = append(indexes, i)
			}
		}
		rank := func(c Candidate) int {
			if c.Headroom == nil {
				return 1
			}
			if *c.Headroom <= 0 {
				return 3
			}
			if c.Exhausted {
				return 2
			}
			return 0
		}
		sort.SliceStable(indexes, func(i, j int) bool {
			a, b := candidates[indexes[i]], candidates[indexes[j]]
			if rank(a) != rank(b) {
				return rank(a) < rank(b)
			}
			switch rank(a) {
			case 0:
				if harvest(a) != harvest(b) {
					return harvest(a)
				}
				if harvest(a) {
					return a.reset.Before(b.reset)
				}
				return *a.Headroom > *b.Headroom
			case 2:
				return *a.Headroom > *b.Headroom
			case 3:
				if a.reset.IsZero() != b.reset.IsZero() {
					return !a.reset.IsZero()
				}
				return a.reset.Before(b.reset)
			}
			return false
		})
		if len(indexes) == 0 {
			continue
		}
		c := &candidates[indexes[0]]
		c.Chosen = true
		reason := "unknown usage, ranked below known headroom"
		if c.Headroom != nil {
			reason = fmt.Sprintf("most headroom: %g%% left", *c.Headroom)
			if c.Exhausted {
				reason = "all exhausted; " + reason
			}
		}
		if c.Headroom != nil && *c.Headroom <= 0 {
			reason = "all exhausted; reset unknown"
		}
		if harvest(*c) || (c.Headroom != nil && *c.Headroom <= 0 && !c.reset.IsZero()) {
			loc, _ := time.LoadLocation("Australia/Sydney")
			reset := c.reset.In(loc).Format("15:04 MST")
			reason = fmt.Sprintf("expiring soon: resets %s, %g%% left", reset, *c.Headroom)
			if *c.Headroom <= 0 {
				reason = "all fully used; soonest reset " + reset
			}
		}
		return c.account, reason, candidates
	}
	return provider.Account{}, "no eligible signed-in account", candidates
}

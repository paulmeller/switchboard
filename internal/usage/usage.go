// Package usage isolates allowlisted read-only commands from task execution.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"

	"switchboard/internal/provider"
)

type Row struct {
	Tool             string         `json:"tool"`
	Profile          string         `json:"profile"`
	Account          *string        `json:"account"`
	Plan             *string        `json:"plan"`
	LoginOK          *bool          `json:"login_ok"`
	WeeklyUsedPct    *float64       `json:"weekly_used_pct"`
	WeeklyResetsAt   *string        `json:"weekly_resets_at"`
	FiveHourUsedPct  *float64       `json:"five_hour_used_pct"`
	FiveHourResetsAt *string        `json:"five_hour_resets_at"`
	Extra            map[string]any `json:"extra"`
	Source           *string        `json:"source"`
	Error            *string        `json:"error"`
	CheckedAt        string         `json:"checked_at"`
}
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	CheckedAt     string `json:"checked_at"`
	Accounts      []Row  `json:"accounts"`
}

// Rows lists the report rows: configured Claude/Codex/Antigravity accounts
// (plus built-in defaults), Grok, and plain Claude.
func Rows(reg provider.Registry) []Row {
	rows := []Row{}
	defaultPlans := map[string]string{
		"claude/work":     "Ultra",
		"claude/personal": "Ultra",
		"codex/work":      "Team",
		"codex/personal":  "Plus",
	}
	add := func(tool, profile, plan string, spec provider.Spec) {
		r := Row{Tool: tool, Profile: profile, Extra: map[string]any{}}
		if plan != "" {
			r.Plan = &plan
		}
		if spec.Label != "" && spec.Tool == "agy" {
			// Configured safe display label, never an email or provider identity.
			label := spec.Label
			r.Account = &label
		}
		if spec.QuotaPool != "" {
			r.Extra["quota_pool"] = spec.QuotaPool
		}
		rows = append(rows, r)
	}
	for _, spec := range reg {
		plan := spec.Plan
		if plan == "" {
			plan = defaultPlans[spec.ID()]
		}
		add(spec.Tool, spec.Profile, plan, spec)
	}
	add("grok", "cli", "", provider.Spec{})
	add("claude", "plain", "", provider.Spec{})
	return rows
}

type Data struct {
	Source      string         `json:"-"`
	Notice      string         `json:"-"`
	Extra       map[string]any `json:"-"`
	ObservedAt  time.Time      `json:"observed_at"`
	LoginOK     *bool          `json:"login_ok"`
	Weekly      *float64       `json:"weekly_used_pct"`
	WeeklyReset *string        `json:"weekly_resets_at"`
	Five        *float64       `json:"five_hour_used_pct"`
	FiveReset   *string        `json:"five_hour_resets_at"`
}
type Source interface {
	Read(context.Context, string, string) (Data, error)
}

// Timed lets a source raise the caller's per-row timeout for slow tools.
type Timed interface {
	Timeout(tool string) time.Duration
}

// safeError is a fixed diagnostic that may be shown verbatim in usage rows.
type safeError string

func (e safeError) Error() string { return string(e) }

// Snapshot reads only dedicated numeric snapshots, never provider files or sessions.
type Snapshot struct{ Dir string }

func (s Snapshot) Read(ctx context.Context, tool, profile string) (Data, error) {
	var d Data
	if ctx.Err() != nil {
		return d, ctx.Err()
	}
	path := filepath.Join(s.Dir, tool+"-"+profile+".json")
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() {
		return d, errors.New("no verified safe source; quota unavailable")
	}
	f, e := os.Open(path)
	if e != nil {
		return d, errors.New("no verified safe source; quota unavailable")
	}
	defer f.Close()
	after, e := f.Stat()
	if e != nil || !os.SameFile(before, after) {
		return d, errors.New("invalid normalized snapshot")
	}
	dec := json.NewDecoder(io.LimitReader(f, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil {
		return Data{}, errors.New("invalid normalized snapshot")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Data{}, errors.New("invalid normalized snapshot")
	}
	return d, nil
}

func valid(d Data, now time.Time) bool {
	if d.ObservedAt.IsZero() || (d.Source == "" && d.ObservedAt.After(now)) {
		return false
	}
	for _, p := range []*float64{d.Weekly, d.Five} {
		if p != nil && (*p < 0 || *p > 100 || math.IsNaN(*p) || math.IsInf(*p, 0)) {
			return false
		}
	}
	for _, p := range []*string{d.WeeklyReset, d.FiveReset} {
		if p != nil {
			if _, e := time.Parse(time.RFC3339, *p); e != nil {
				return false
			}
		}
	}
	return d.LoginOK != nil || d.Weekly != nil || d.Five != nil || d.WeeklyReset != nil || d.FiveReset != nil
}

func sydneyTime(t time.Time) string {
	loc, _ := time.LoadLocation("Australia/Sydney")
	return t.In(loc).Format(time.RFC3339)
}

func sydneyReset(value *string) *string {
	if value == nil {
		return nil
	}
	// Collect validates explicit timezone-bearing timestamps before normalization.
	t, _ := time.Parse(time.RFC3339, *value)
	normalized := sydneyTime(t)
	return &normalized
}

func Collect(ctx context.Context, source Source, reg provider.Registry, timeout time.Duration, now time.Time) (Report, int) {
	return CollectRows(ctx, source, timeout, now, Rows(reg))
}

func CollectRows(ctx context.Context, source Source, timeout time.Duration, now time.Time, rows []Row) (Report, int) {
	report := Report{1, sydneyTime(now), rows}
	success := 0
	for i := range report.Accounts {
		r := &report.Accounts[i]
		rowTimeout := timeout
		if t, ok := source.(Timed); ok && t.Timeout(r.Tool) > rowTimeout {
			rowTimeout = t.Timeout(r.Tool)
		}
		c, cancel := context.WithTimeout(ctx, rowTimeout)
		type result struct {
			d Data
			e error
		}
		ch := make(chan result, 1)
		go func(tool, profile string) { d, e := source.Read(c, tool, profile); ch <- result{d, e} }(r.Tool, r.Profile)
		var got result
		select {
		case got = <-ch:
		case <-c.Done():
			got.e = c.Err()
		}
		cancel()
		// Per-row read time in UTC: the attempt time, or the observed/fetch time on success.
		r.CheckedAt = now.UTC().Format(time.RFC3339)
		if got.e != nil || !valid(got.d, now) {
			msg := "no verified safe source; quota unavailable"
			if errors.Is(got.e, errGrokUnavailable) {
				msg = errGrokUnavailable.Error()
			}
			if errors.Is(got.e, errGrokSchema) {
				msg = errGrokSchema.Error()
			}
			var safe safeError
			if errors.As(got.e, &safe) {
				msg = safe.Error()
			}
			if errors.Is(got.e, context.DeadlineExceeded) {
				msg = "usage source timed out"
			}
			r.Error = &msg
			continue
		}
		d := got.d
		r.CheckedAt = d.ObservedAt.UTC().Format(time.RFC3339) // valid() guarantees non-zero
		r.LoginOK = d.LoginOK
		r.WeeklyUsedPct = d.Weekly
		r.WeeklyResetsAt = sydneyReset(d.WeeklyReset)
		r.FiveHourUsedPct = d.Five
		r.FiveHourResetsAt = sydneyReset(d.FiveReset)
		src := d.Source
		if src == "" {
			src = "cached normalized snapshot"
		}
		r.Source = &src
		if d.Source == "" {
			r.Extra["observed_at"] = sydneyTime(d.ObservedAt)
			r.Extra["age_seconds"] = int64(now.Sub(d.ObservedAt).Seconds())
		} else {
			for k, v := range d.Extra {
				r.Extra[k] = v
			}
			if d.Notice != "" {
				r.Error = &d.Notice
			}
		}
		success++
	}
	if success == 0 {
		return report, 1
	}
	return report, 0
}

package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"regexp"
	"time"

	"switchboard/internal/provider"
)

// AgyTimeout bounds one Antigravity /usage read; silent sign-in alone often takes 1-2s.
const AgyTimeout = 20 * time.Second

const (
	errAgyUnavailable = safeError("Antigravity /usage failed; check sign-in with the genuine agy CLI")
	errAgyPayload     = safeError("Antigravity /usage returned an unrecognized payload; quota unavailable")
	errAgyStatus      = safeError("Antigravity /usage did not report success; quota unavailable")
	errAgyNoGroups    = safeError("Antigravity /usage returned no quota groups; quota unavailable")
	errAgyProfile     = safeError("unknown Antigravity profile")
)

// Agy reads quota with the built-in print-mode /usage slash command (not a model
// prompt) under each account's HOME: process HOME for personal, isolated for kelly.
type Agy struct {
	Runner Runner
	Home   string
	Env    []string
}

func (Agy) Timeout(string) time.Duration { return AgyTimeout }

func (s Agy) Read(ctx context.Context, _, profile string) (Data, error) {
	env, err := provider.Env(provider.Account{Tool: "agy", Profile: profile}, s.Home, s.Env)
	if err != nil {
		return Data{}, errAgyProfile
	}
	raw, err := s.Runner.Read(ctx, AgyUsage, env)
	if err != nil {
		if ctx.Err() != nil {
			return Data{}, ctx.Err()
		}
		return Data{}, errAgyUnavailable
	}
	return parseAgyUsage(raw, time.Now())
}

type agyBucket struct {
	Window    string   `json:"window"`
	Remaining *float64 `json:"remaining_fraction"`
	Reset     *string  `json:"reset_time"`
}
type agyGroup struct {
	Name    string      `json:"name"`
	Buckets []agyBucket `json:"buckets"`
}
type agyPayload struct {
	Status  string `json:"status"`
	Command *struct {
		Data struct {
			Groups []agyGroup `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// agyJSON accepts raw JSON or the last column-zero JSON object after log lines.
func agyJSON(raw []byte) (agyPayload, bool) {
	starts := []int{}
	for i, b := range raw {
		if b == '{' && (i == 0 || raw[i-1] == '\n') {
			starts = append(starts, i)
		}
	}
	for i := len(starts) - 1; i >= 0; i-- {
		var p agyPayload
		if json.NewDecoder(bytes.NewReader(raw[starts[i]:])).Decode(&p) == nil && (p.Command != nil || p.Status != "") {
			return p, true
		}
	}
	return agyPayload{}, false
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9 ._/()+:-]{1,64}$`)

func agyWindow(b agyBucket) (*float64, *string, bool) {
	if b.Remaining == nil || math.IsNaN(*b.Remaining) || math.IsInf(*b.Remaining, 0) {
		return nil, nil, false
	}
	used := math.Round(math.Min(100, math.Max(0, (1-*b.Remaining)*100))*100) / 100
	var reset *string
	if b.Reset != nil {
		if t, err := time.Parse(time.RFC3339, *b.Reset); err == nil {
			v := sydneyTime(t)
			reset = &v
		}
	}
	return &used, reset, true
}

func hasPrimaryWindow(g agyGroup) bool {
	for _, b := range g.Buckets {
		if b.Window == "weekly" || b.Window == "5h" {
			return true
		}
	}
	return false
}

func parseAgyUsage(raw []byte, now time.Time) (Data, error) {
	p, ok := agyJSON(raw)
	if !ok {
		return Data{}, errAgyPayload
	}
	if p.Status != "" && p.Status != "SUCCESS" {
		return Data{}, errAgyStatus
	}
	if p.Command == nil || len(p.Command.Data.Groups) == 0 {
		return Data{}, errAgyNoGroups
	}
	groups := p.Command.Data.Groups
	primary := -1
	for i, g := range groups {
		if g.Name == "Gemini Models" {
			primary = i
			break
		}
	}
	if primary < 0 {
		for i, g := range groups {
			if hasPrimaryWindow(g) {
				primary = i
				break
			}
		}
	}
	yes := true
	d := Data{ObservedAt: now, LoginOK: &yes, Source: "live agy --output-format json --print /usage (built-in slash command)", Extra: map[string]any{}}
	pools := map[string]any{}
	for i, g := range groups {
		windows := map[string]any{}
		for _, b := range g.Buckets {
			used, reset, ok := agyWindow(b)
			if !ok {
				continue
			}
			if i == primary && b.Window == "weekly" && d.Weekly == nil {
				d.Weekly, d.WeeklyReset = used, reset
				continue
			}
			if i == primary && b.Window == "5h" && d.Five == nil {
				d.Five, d.FiveReset = used, reset
				continue
			}
			if !safeName.MatchString(b.Window) {
				continue
			}
			bucket := map[string]any{"used_pct": *used}
			if reset != nil {
				bucket["resets_at"] = *reset
			}
			windows[b.Window] = bucket
		}
		if len(windows) > 0 && safeName.MatchString(g.Name) {
			pools[g.Name] = windows
		}
	}
	if primary >= 0 && safeName.MatchString(groups[primary].Name) {
		d.Extra["primary_pool"] = groups[primary].Name
	}
	if len(pools) > 0 {
		d.Extra["pools"] = pools
	}
	if d.Weekly == nil && d.Five == nil {
		d.Notice = "Antigravity /usage reported no 5h or weekly window"
	}
	return d, nil
}

package config

import (
	"errors"
	"math"
	"strings"
)

type ModelFamily struct {
	Easy         *string `toml:"easy"`
	Normal       *string `toml:"normal"`
	Hard         *string `toml:"hard"`
	EasyEffort   *string `toml:"easy_reasoning_effort"`
	NormalEffort *string `toml:"normal_reasoning_effort"`
	HardEffort   *string `toml:"hard_reasoning_effort"`
}
type ModelAccount struct {
	Model  *string `toml:"model"`
	Effort *string `toml:"reasoning_effort"`
}
type Models struct {
	EasyMax  float64                 `toml:"easy_max"`
	HardMin  float64                 `toml:"hard_min"`
	Codex    ModelFamily             `toml:"codex"`
	Claude   ModelFamily             `toml:"claude"`
	Accounts map[string]ModelAccount `toml:"accounts"`
}

func ModelString(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n")
}

func (m Models) Validate() error {
	for _, v := range []float64{m.EasyMax, m.HardMin} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 2 {
			return errors.New("model thresholds must be finite 0..2")
		}
	}
	if m.EasyMax >= m.HardMin {
		return errors.New("models easy_max must be less than hard_min")
	}
	values := []*string{}
	for _, f := range []ModelFamily{m.Codex, m.Claude} {
		values = append(values, f.Easy, f.Normal, f.Hard, f.EasyEffort, f.NormalEffort, f.HardEffort)
	}
	if m.Claude.EasyEffort != nil || m.Claude.NormalEffort != nil || m.Claude.HardEffort != nil {
		return errors.New("reasoning_effort is Codex-only")
	}
	for id, r := range m.Accounts {
		a, err := ParseAccount(id)
		if err != nil {
			return err
		}
		if a.Tool != "codex" && r.Effort != nil {
			return errors.New("reasoning_effort is Codex-only")
		}
		values = append(values, r.Model, r.Effort)
	}
	for _, v := range values {
		if v != nil && !ModelString(*v) {
			return errors.New("model and effort must be non-empty without surrounding whitespace or newlines")
		}
	}
	return nil
}

func (m Models) Rule(tool, account string, score *float64) (*string, *string, string, string) {
	tier := "normal"
	if score != nil {
		if *score <= m.EasyMax {
			tier = "easy"
		} else if *score >= m.HardMin {
			tier = "hard"
		}
	}
	if r, ok := m.Accounts[account]; ok && r.Model != nil {
		return r.Model, r.Effort, tier, "account"
	}
	f := m.Codex
	if tool == "claude" {
		f = m.Claude
	}
	model, effort := f.Normal, f.NormalEffort
	if tier == "easy" {
		model, effort = f.Easy, f.EasyEffort
	}
	if tier == "hard" {
		model, effort = f.Hard, f.HardEffort
	}
	if model == nil {
		return nil, nil, "", "default"
	}
	return model, effort, tier, "tier"
}

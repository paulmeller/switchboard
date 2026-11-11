package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"switchboard/internal/config"
	"switchboard/internal/provider"
)

type modelPick struct {
	Model  *string  `json:"model"`
	Tier   *string  `json:"tier"`
	Source string   `json:"model_source"`
	Argv   []string `json:"argv"`
}

// Recognize only model-specific overrides; other tool config remains untouched.
func passthroughModel(args []string) (*string, bool) {
	var model *string
	found := false
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			break
		}
		key, value, eq := strings.Cut(v, "=")
		if key == "--model" || key == "-m" {
			found = true
			if !eq && i+1 < len(args) {
				i++
				value = args[i]
			}
			if value != "" {
				x := value
				model = &x
			}
		}
		if key == "-c" || key == "--config" {
			if !eq && i+1 < len(args) {
				i++
				value = args[i]
			}
			k, x, ok := strings.Cut(value, "=")
			k = strings.TrimSpace(k)
			if ok && (k == "model" || k == "model_reasoning_effort") {
				found = true
				if k == "model" {
					var decoded struct {
						Model *string `toml:"model"`
					}
					if toml.Unmarshal([]byte("model = "+x), &decoded) == nil && decoded.Model != nil && *decoded.Model != "" {
						model = decoded.Model
					}
				}
			}
		}
	}
	return model, found
}

func chooseModel(c CLI, m config.Models, a provider.Account, score *float64, args []string) modelPick {
	r := modelPick{Source: "default", Argv: append([]string{}, args...)}
	if c.Model != "" {
		r.Model = &c.Model
		r.Source = "flag"
		// An explicit sb flag must reach the genuine CLI. Remove competing model
		// options, while preserving all unrelated passthrough arguments.
		clean := []string{}
		for i := 0; i < len(args); i++ {
			k, v, eq := strings.Cut(args[i], "=")
			if args[i] == "--" {
				clean = append(clean, args[i:]...)
				break
			}
			if k == "--model" || k == "-m" {
				if !eq && i+1 < len(args) {
					i++
				}
				continue
			}
			if k == "-c" || k == "--config" {
				if !eq && i+1 < len(args) {
					v = args[i+1]
				}
				key, _, ok := strings.Cut(v, "=")
				if ok && (strings.TrimSpace(key) == "model" || strings.TrimSpace(key) == "model_reasoning_effort") {
					if !eq {
						i++
					}
					continue
				}
			}
			clean = append(clean, args[i])
			if (k == "-c" || k == "--config") && !eq && i+1 < len(args) {
				i++
				clean = append(clean, args[i])
			}
		}
		r.Argv = append([]string{"--model", c.Model}, clean...)
		return r
	}
	if model, ok := passthroughModel(args); ok {
		r.Model = model
		r.Source = "passthrough"
		return r
	}
	model, effort, tier, source := m.Rule(a.Tool, a.ID(), score)
	r.Model, r.Source = model, source
	if tier != "" {
		r.Tier = &tier
	}
	if model != nil {
		prefix := []string{"--model", *model}
		if a.Tool == "codex" && effort != nil {
			quoted, _ := json.Marshal(*effort)
			prefix = append(prefix, "-c", "model_reasoning_effort="+string(quoted))
		}
		r.Argv = append(prefix, r.Argv...)
	}
	return r
}

func (r modelPick) text() string {
	val := func(s *string) string {
		if s == nil {
			return "null"
		}
		return *s
	}
	return fmt.Sprintf("model=%s tier=%s model_source=%s", val(r.Model), val(r.Tier), r.Source)
}

func (r modelPick) fields(v map[string]any) {
	v["model"], v["tier"], v["model_source"], v["argv"] = r.Model, r.Tier, r.Source, r.Argv
}

func modelContract() map[string]any {
	return map[string]any{
		"defaults":         map[string]any{"easy_max": 0.67, "hard_min": 1.33, "models": nil},
		"score":            "reported 0..2 difficulty after difficulty_floor; unknown uses normal",
		"precedence":       []string{"flag", "passthrough", "account", "tier", "default"},
		"reasoning_effort": "Codex-only; inject only with rule model; Claude config rejects effort",
		"validation":       "no model-list API; strings non-empty without surrounding whitespace/newlines",
		"argv":             "prepend --model NAME; Codex optional -c with TOML-quoted model_reasoning_effort",
	}
}

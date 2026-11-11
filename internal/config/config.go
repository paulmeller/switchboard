package config

import (
	"bytes"
	"errors"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"switchboard/internal/provider"

	"github.com/pelletier/go-toml/v2"
)

type Rule struct {
	Contains string `toml:"contains"`
	Account  string `toml:"account"`
}
type Classifier struct {
	Provider       string `toml:"provider"`
	BaseURL        string `toml:"base_url"`
	Model          string `toml:"model"`
	APIKeyEnv      string `toml:"api_key_env"`
	APIKeyFile     string `toml:"api_key_file"`
	APIKeyKeychain string `toml:"api_key_keychain"`
}

func (c *Classifier) Validate() error {
	if c.Provider == "" {
		c.Provider = "typesafe"
	}
	base, model, env := "https://api.typesafe.ai", "jev-1.14.0", "TYPESAFE_API_KEY"
	switch c.Provider {
	case "typesafe", "off":
	case "ai-gateway":
		base, model, env = "https://ai-gateway.vercel.sh/typesafe", "typesafe-ai/jev-1.14.0", "AI_GATEWAY_API_KEY"
	default:
		return errors.New("invalid classifier provider")
	}
	if c.BaseURL == "" {
		c.BaseURL = base
	}
	if c.Model == "" {
		c.Model = model
	}
	if c.APIKeyEnv == "" {
		c.APIKeyEnv = env
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid classifier base_url")
	}
	if strings.TrimSpace(c.Model) != c.Model || strings.ContainsAny(c.Model, " \t\r\n") || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(c.APIKeyEnv) || strings.ContainsAny(c.APIKeyKeychain, "\r\n") {
		return errors.New("invalid classifier configuration")
	}
	return nil
}

type Log struct {
	Enabled    bool  `toml:"enabled"`
	MaxBytes   int64 `toml:"max_bytes"`
	RedactTask bool  `toml:"redact_task"`
}

type Config struct {
	Models            Models              `toml:"models"`
	Log               Log                 `toml:"log"`
	Pin               string              `toml:"pin"`
	UsageExhaustedPct float64             `toml:"usage_exhausted_pct"`
	UsageCacheTTL     string              `toml:"usage_cache_ttl"`
	Classifier        Classifier          `toml:"classifier"`
	Default           string              `toml:"default"`
	Confidence        float64             `toml:"confidence"`
	AccountFloor      float64             `toml:"account_floor"`
	DifficultyFloor   float64             `toml:"difficulty_floor"`
	Accounts          map[string]Criteria `toml:"accounts"`
	Rules             []Rule              `toml:"rules"`
}

func Path(home, xdg string) string {
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "switchboard", "config.toml")
}

func ParseAccount(s string) (provider.Account, error) {
	t, p, _ := strings.Cut(s, "/")
	a := provider.Account{Tool: t, Profile: p}
	if !a.Valid() {
		return a, errors.New("account must be claude/work, claude/personal, codex/work or codex/personal")
	}
	return a, nil
}

func Load(path, envDefault, flagDefault string, overrides ...Classifier) (Config, error) {
	c := Config{Models: Models{EasyMax: 0.67, HardMin: 1.33}, Log: Log{Enabled: true, MaxBytes: 5242880}, Default: "codex/work", UsageExhaustedPct: 95, UsageCacheTTL: "60s", AccountFloor: 0.5, DifficultyFloor: 0.5}
	b, e := os.ReadFile(path)
	if e == nil {
		if toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c) != nil {
			return c, errors.New("invalid TOML config")
		}
		var presence struct {
			Confidence   *float64 `toml:"confidence"`
			AccountFloor *float64 `toml:"account_floor"`
		}
		if toml.Unmarshal(b, &presence) != nil {
			return c, errors.New("invalid TOML config")
		}
		if presence.AccountFloor == nil && presence.Confidence != nil {
			c.AccountFloor = *presence.Confidence
		}
	} else if !os.IsNotExist(e) {
		return c, errors.New("cannot read config")
	}
	if envDefault != "" {
		c.Pin = envDefault
	}
	if flagDefault != "" {
		c.Pin = flagDefault
	}
	for _, override := range overrides {
		if override.Provider != "" {
			c.Classifier.Provider = override.Provider
		}
		if override.BaseURL != "" {
			c.Classifier.BaseURL = override.BaseURL
		}
		if override.Model != "" {
			c.Classifier.Model = override.Model
		}
	}
	if e = c.Models.Validate(); e != nil {
		return c, e
	}
	if c.Log.MaxBytes <= 0 {
		return c, errors.New("log max_bytes must be positive")
	}
	if e = c.Classifier.Validate(); e != nil {
		return c, e
	}
	if _, e = Family(c.Default); e != nil {
		return c, e
	}
	for _, floor := range []float64{c.AccountFloor, c.DifficultyFloor} {
		if math.IsNaN(floor) || math.IsInf(floor, 0) || floor < 0 || floor > 1 {
			return c, errors.New("floors must be 0..1")
		}
	}
	if c.Pin != "" {
		if _, e = ParseAccount(c.Pin); e != nil {
			return c, e
		}
	}
	if math.IsNaN(c.UsageExhaustedPct) || math.IsInf(c.UsageExhaustedPct, 0) || c.UsageExhaustedPct < 0 || c.UsageExhaustedPct > 100 {
		return c, errors.New("usage_exhausted_pct must be 0..100")
	}
	if ttl, err := time.ParseDuration(c.UsageCacheTTL); err != nil || ttl < 0 {
		return c, errors.New("invalid usage_cache_ttl")
	}
	defaults := DefaultCriteria()
	var fields struct {
		Accounts map[string]map[string]any `toml:"accounts"`
	}
	_ = toml.Unmarshal(b, &fields)
	for id, override := range c.Accounts {
		merged := defaults[id]
		table := fields.Accounts[id]
		if _, ok := table["what"]; ok {
			merged.What = override.What
		}
		if _, ok := table["not_for"]; ok {
			merged.NotFor = override.NotFor
		}
		if _, ok := table["examples"]; ok {
			merged.Examples = override.Examples
		}
		if strings.TrimSpace(merged.What) == "" {
			return c, errors.New("account what must not be empty")
		}
		defaults[id] = merged
	}
	c.Accounts = defaults
	if math.IsNaN(c.Confidence) || math.IsInf(c.Confidence, 0) || c.Confidence < 0 || c.Confidence > 1 {
		return c, errors.New("confidence must be 0..1")
	}
	for _, r := range c.Rules {
		if strings.TrimSpace(r.Contains) == "" {
			return c, errors.New("rule contains must not be empty")
		}
		if _, e = Family(r.Account); e != nil {
			return c, e
		}
	}
	return c, nil
}

func (c Config) Order(task string) ([]string, string) {
	target := c.Default
	reason := "configured default"
	for _, r := range c.Rules {
		if strings.Contains(strings.ToLower(task), strings.ToLower(r.Contains)) {
			target = r.Account
			reason = "first matching rule"
			break
		}
	}
	a, _ := Family(target)
	out := []string{a}
	for _, other := range provider.Accounts() {
		if other.Tool != a && (len(out) == 1 || out[len(out)-1] != other.Tool) {
			out = append(out, other.Tool)
		}
	}
	return out, reason
}

func Family(s string) (string, error) {
	if s == "claude" || s == "codex" {
		return s, nil
	}
	a, err := ParseAccount(s)
	return a.Tool, err
}

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
	Log                   Log                 `toml:"log"`
	Pin                   string              `toml:"pin"`
	UsageExhaustedPct     float64             `toml:"usage_exhausted_pct"`
	HarvestMinHeadroomPct float64             `toml:"harvest_min_headroom_pct"`
	HarvestWindowHours    float64             `toml:"harvest_window_hours"`
	UsageCacheTTL         string              `toml:"usage_cache_ttl"`
	Classifier            Classifier          `toml:"classifier"`
	Default               string              `toml:"default"`
	Confidence            float64             `toml:"confidence"`
	AccountFloor          float64             `toml:"account_floor"`
	DifficultyFloor       float64             `toml:"difficulty_floor"`
	Accounts              map[string]Criteria `toml:"accounts"`
	Rules                 []Rule              `toml:"rules"`
	ToolAccounts          []ToolAccount       `toml:"tool_accounts"`
	// Registry is derived from ToolAccounts: built-in Claude/Codex accounts,
	// then configured Antigravity accounts or the implicit agy/personal.
	Registry provider.Registry `toml:"-"`
}

// ToolAccount declares a launch account or overlays a built-in Claude/Codex account.
type ToolAccount struct {
	ID        string `toml:"id"`
	Label     string `toml:"label"`
	Plan      string `toml:"plan"`
	Home      string `toml:"home"`
	QuotaPool string `toml:"quota_pool"`
	ConfigDir string `toml:"config_dir"`
	TokenEnv  string `toml:"token_env"`
}

var (
	poolID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
	envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Registry validates tool_accounts and returns the launch account registry.
func Registry(accounts []ToolAccount) (provider.Registry, error) {
	specs := []provider.Spec{}
	seen := map[string]bool{}
	for _, t := range accounts {
		tool, profile, ok := strings.Cut(t.ID, "/")
		a := provider.Account{Tool: tool, Profile: profile}
		if !ok || !a.Valid() {
			return nil, errors.New("tool_accounts id must be claude/<profile>, codex/<profile>, or agy/<profile>")
		}
		if seen[a.ID()] {
			return nil, errors.New("duplicate tool_accounts id " + a.ID())
		}
		seen[a.ID()] = true
		label := strings.TrimSpace(t.Label)
		if label == "" || label != t.Label || len(label) > 64 || strings.ContainsAny(label, "@\r\n\t") {
			return nil, errors.New("tool_accounts " + a.ID() + " label must be non-empty, at most 64 characters, and not an email")
		}
		plan := strings.TrimSpace(t.Plan)
		if plan != t.Plan || len(plan) > 32 || strings.ContainsAny(plan, "@\r\n\t") {
			return nil, errors.New("tool_accounts " + a.ID() + " plan must be at most 32 characters and not an email")
		}
		if tool == "claude" {
			if t.Home != "" || t.QuotaPool != "" {
				return nil, errors.New("Claude entries cannot set home or quota_pool")
			}
			if t.ConfigDir != "" && t.TokenEnv != "" {
				return nil, errors.New("Claude entries cannot set both config_dir and token_env")
			}
			if profile != "work" && profile != "personal" && t.ConfigDir == "" && t.TokenEnv == "" {
				return nil, errors.New("custom Claude profile " + a.ID() + " requires config_dir or token_env")
			}
		}
		if tool != "claude" && (t.ConfigDir != "" || t.TokenEnv != "") {
			return nil, errors.New("config_dir and token_env are only valid for Claude")
		}
		if tool == "codex" && t.QuotaPool != "" {
			return nil, errors.New("Codex entries cannot set quota_pool")
		}
		if t.TokenEnv != "" {
			if !envName.MatchString(t.TokenEnv) {
				return nil, errors.New("tool_accounts " + a.ID() + " token_env must be a valid environment variable name")
			}
			switch t.TokenEnv {
			case "ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "HOME", "PATH":
				return nil, errors.New("tool_accounts " + a.ID() + " token_env cannot be " + t.TokenEnv)
			}
		}
		if t.ConfigDir != "" && (strings.ContainsAny(t.ConfigDir, "\r\n") || !(filepath.IsAbs(t.ConfigDir) || t.ConfigDir == "~" || strings.HasPrefix(t.ConfigDir, "~/"))) {
			return nil, errors.New("config_dir must be absolute or start with ~/")
		}
		if t.Home != "" && (strings.ContainsAny(t.Home, "\r\n") || !(filepath.IsAbs(t.Home) || t.Home == "~" || strings.HasPrefix(t.Home, "~/"))) {
			return nil, errors.New("tool_accounts " + a.ID() + " home must be absolute or start with ~/")
		}
		if t.QuotaPool != "" && !poolID.MatchString(t.QuotaPool) {
			return nil, errors.New("tool_accounts " + a.ID() + " quota_pool must be a simple id")
		}
		specs = append(specs, provider.Spec{Account: a, Label: label, Plan: plan, Home: t.Home, QuotaPool: t.QuotaPool, ConfigDir: t.ConfigDir, TokenEnv: t.TokenEnv})
	}
	return provider.NewRegistry(specs), nil
}

func Path(home, xdg string) string {
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "switchboard", "config.toml")
}

// accounts returns the loaded registry, or the built-in default for a Config
// constructed without Load.
func (c Config) accounts() provider.Registry {
	if c.Registry == nil {
		return provider.NewRegistry(nil)
	}
	return c.Registry
}

// ParseAccount accepts only a configured full tool/profile id.
func (c Config) ParseAccount(s string) (provider.Account, error) {
	return c.accounts().Parse(s)
}

func Load(path, envDefault, flagDefault string, overrides ...Classifier) (Config, error) {
	c := Config{Log: Log{Enabled: true, MaxBytes: 5242880}, Default: "codex", UsageExhaustedPct: 95, HarvestMinHeadroomPct: 10, HarvestWindowHours: 6, UsageCacheTTL: "60s", AccountFloor: 0.5, DifficultyFloor: 0.5}
	b, e := os.ReadFile(path)
	if e == nil {
		if toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c) != nil {
			return c, errors.New("invalid TOML config")
		}
		var presence struct {
			Confidence   *float64 `toml:"confidence"`
			AccountFloor *float64 `toml:"account_floor"`
			ToolAccounts []struct {
				ID        string  `toml:"id"`
				Home      *string `toml:"home"`
				QuotaPool *string `toml:"quota_pool"`
				ConfigDir *string `toml:"config_dir"`
				TokenEnv  *string `toml:"token_env"`
			} `toml:"tool_accounts"`
		}
		if toml.Unmarshal(b, &presence) != nil {
			return c, errors.New("invalid TOML config")
		}
		for _, t := range presence.ToolAccounts {
			tool, profile, ok := strings.Cut(t.ID, "/")
			if !ok || !(provider.Account{Tool: tool, Profile: profile}).Valid() {
				continue
			}
			if tool == "claude" && (t.Home != nil || t.QuotaPool != nil) {
				return c, errors.New("Claude entries cannot set home or quota_pool")
			}
			if tool != "claude" && (t.ConfigDir != nil || t.TokenEnv != nil) {
				return c, errors.New("config_dir and token_env are only valid for Claude")
			}
			if tool == "codex" && t.QuotaPool != nil {
				return c, errors.New("Codex entries cannot set quota_pool")
			}
		}
		if presence.AccountFloor == nil && presence.Confidence != nil {
			c.AccountFloor = *presence.Confidence
		}
	} else if !os.IsNotExist(e) {
		return c, errors.New("cannot read config")
	}
	if c.Registry, e = Registry(c.ToolAccounts); e != nil {
		return c, e
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
	if c.Log.MaxBytes <= 0 {
		return c, errors.New("log max_bytes must be positive")
	}
	if e = c.Classifier.Validate(); e != nil {
		return c, e
	}
	if _, e = c.Family(c.Default); e != nil {
		return c, e
	}
	for _, floor := range []float64{c.AccountFloor, c.DifficultyFloor} {
		if math.IsNaN(floor) || math.IsInf(floor, 0) || floor < 0 || floor > 1 {
			return c, errors.New("floors must be 0..1")
		}
	}
	if c.Pin != "" {
		if _, e = c.ParseAccount(c.Pin); e != nil {
			return c, e
		}
	}
	if math.IsNaN(c.UsageExhaustedPct) || math.IsInf(c.UsageExhaustedPct, 0) || c.UsageExhaustedPct < 0 || c.UsageExhaustedPct > 100 {
		return c, errors.New("usage_exhausted_pct must be 0..100")
	}
	if math.IsNaN(c.HarvestMinHeadroomPct) || math.IsInf(c.HarvestMinHeadroomPct, 0) || c.HarvestMinHeadroomPct < 0 || c.HarvestMinHeadroomPct > 100 {
		return c, errors.New("harvest_min_headroom_pct must be finite 0..100")
	}
	if math.IsNaN(c.HarvestWindowHours) || math.IsInf(c.HarvestWindowHours, 0) || c.HarvestWindowHours < 0 {
		return c, errors.New("harvest_window_hours must be finite and >= 0")
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
		if _, e = c.Family(r.Account); e != nil {
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
	a, _ := c.Family(target)
	out := []string{a}
	for _, other := range c.accounts().Accounts() {
		if other.Tool != a && (len(out) == 1 || out[len(out)-1] != other.Tool) {
			out = append(out, other.Tool)
		}
	}
	return out, reason
}

// Fallback filters a family order for automatic fallback. Antigravity is only
// reached when it is the selected family (rule/default, Jev or explicit pin),
// never as a silent fallback from Claude or Codex.
func Fallback(order []string) []string {
	out := []string{}
	for i, f := range order {
		if f != "agy" || i == 0 {
			out = append(out, f)
		}
	}
	return out
}

// Family accepts a bare launchable family or a configured full account id.
func (c Config) Family(s string) (string, error) {
	if s == "claude" || s == "codex" || s == "agy" {
		return s, nil
	}
	a, err := c.ParseAccount(s)
	return a.Tool, err
}

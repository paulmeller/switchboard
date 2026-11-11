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

type Config struct {
	Classifier Classifier `toml:"classifier"`
	Default    string     `toml:"default"`
	Confidence float64    `toml:"confidence"`
	Rules      []Rule     `toml:"rules"`
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
	c := Config{Default: "codex/work", Confidence: 0.8}
	b, e := os.ReadFile(path)
	if e == nil {
		if toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c) != nil {
			return c, errors.New("invalid TOML config")
		}
	} else if !os.IsNotExist(e) {
		return c, errors.New("cannot read config")
	}
	if envDefault != "" {
		c.Default = envDefault
	}
	if flagDefault != "" {
		c.Default = flagDefault
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
	if e = c.Classifier.Validate(); e != nil {
		return c, e
	}
	if _, e = ParseAccount(c.Default); e != nil {
		return c, e
	}
	if math.IsNaN(c.Confidence) || math.IsInf(c.Confidence, 0) || c.Confidence < 0 || c.Confidence > 1 {
		return c, errors.New("confidence must be 0..1")
	}
	for _, r := range c.Rules {
		if strings.TrimSpace(r.Contains) == "" {
			return c, errors.New("rule contains must not be empty")
		}
		if _, e = ParseAccount(r.Account); e != nil {
			return c, e
		}
	}
	return c, nil
}

func (c Config) Order(task string) ([]provider.Account, string) {
	target := c.Default
	reason := "configured default"
	for _, r := range c.Rules {
		if strings.Contains(strings.ToLower(task), strings.ToLower(r.Contains)) {
			target = r.Account
			reason = "first matching rule"
			break
		}
	}
	a, _ := ParseAccount(target)
	out := []provider.Account{a}
	for _, other := range provider.Accounts() {
		if other != a {
			out = append(out, other)
		}
	}
	return out, reason
}

package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"switchboard/internal/provider"

	"github.com/pelletier/go-toml/v2"
)

type Rule struct {
	Contains string `toml:"contains"`
	Account  string `toml:"account"`
}
type Config struct {
	Default    string  `toml:"default"`
	Confidence float64 `toml:"confidence"`
	Rules      []Rule  `toml:"rules"`
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

func Load(path, envDefault, flagDefault string) (Config, error) {
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
	if _, e = ParseAccount(c.Default); e != nil {
		return c, e
	}
	if c.Confidence < 0 || c.Confidence > 1 {
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

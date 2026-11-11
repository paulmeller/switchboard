package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrecedenceAndRules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("default = \"claude/work\"\n[[rules]]\ncontains = \"test\"\naccount = \"codex/personal\"\n"), 0o600)
	c, e := Load(p, "codex/work", "claude/personal")
	if e != nil || c.Default != "claude/personal" {
		t.Fatal("precedence")
	}
	order, reason := c.Order("TEST fake")
	if order[0].ID() != "codex/personal" || reason != "first matching rule" {
		t.Fatal("rule")
	}
	os.WriteFile(p, []byte("unknown = true\n"), 0o600)
	if _, e = Load(p, "", ""); e == nil {
		t.Fatal("unknown config accepted")
	}
}

func TestClassifierConfig(t *testing.T) {
	for _, tc := range []struct {
		text, provider, model, env, base string
		bad                              bool
	}{
		{"", "typesafe", "jev-1.14.0", "TYPESAFE_API_KEY", "https://api.typesafe.ai", false},
		{"[classifier]\nprovider='ai-gateway'", "ai-gateway", "typesafe-ai/jev-1.14.0", "AI_GATEWAY_API_KEY", "https://ai-gateway.vercel.sh/typesafe", false},
		{"[classifier]\nprovider='off'", "off", "jev-1.14.0", "TYPESAFE_API_KEY", "https://api.typesafe.ai", false},
		{text: "confidence=nan", bad: true},
		{text: "[classifier]\nprovider='unknown'", bad: true},
		{text: "[classifier]\nbase_url='not a URL'", bad: true},
		{text: "[classifier]\napi_key_env='bad name'", bad: true},
		{text: "[classifier]\nunknown=true", bad: true},
	} {
		p := filepath.Join(t.TempDir(), "config.toml")
		os.WriteFile(p, []byte(tc.text), 0o600)
		c, err := Load(p, "", "")
		if tc.bad {
			if err == nil {
				t.Fatal("invalid config accepted")
			}
			continue
		}
		if err != nil || c.Classifier.Provider != tc.provider || c.Classifier.Model != tc.model || c.Classifier.APIKeyEnv != tc.env || c.Classifier.BaseURL != tc.base {
			t.Fatal("classifier defaults")
		}
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[classifier]\nprovider='off'\nmodel='configured'"), 0o600)
	c, err := Load(p, "", "", Classifier{Provider: "ai-gateway", Model: "environment"}, Classifier{Model: "flag"})
	if err != nil || c.Classifier.Provider != "ai-gateway" || c.Classifier.Model != "flag" {
		t.Fatal("override precedence")
	}
}

func TestFloorsAndCriteria(t *testing.T) {
	for _, tc := range []struct {
		text  string
		floor float64
		bad   bool
	}{
		{"", .5, false},
		{"confidence=0.8", .8, false},
		{"confidence=0.8\naccount_floor=0", 0, false},
		{"difficulty_floor=inf", .5, true},
		{"[accounts.\"future/id\"]\nwhat='Future tool'", .5, false},
		{"[accounts.\"codex/work\"]\nwhat=''", .5, true},
		{"[accounts.agy]\nunknown=true", .5, true},
	} {
		p := filepath.Join(t.TempDir(), "config.toml")
		os.WriteFile(p, []byte(tc.text), 0o600)
		c, err := Load(p, "", "")
		if tc.bad {
			if err == nil {
				t.Fatal("invalid config accepted")
			}
			continue
		}
		if err != nil || c.AccountFloor != tc.floor {
			t.Fatalf("floor: %v %g", err, c.AccountFloor)
		}
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[accounts.\"codex/work\"]\nwhat='Custom implementation'\nexamples=[]"), 0o600)
	c, err := Load(p, "", "")
	if err != nil || c.Accounts["codex/work"].What != "Custom implementation" || c.Accounts["codex/work"].NotFor == "" || len(c.Accounts["codex/work"].Examples) != 0 {
		t.Fatal("field merge")
	}
}

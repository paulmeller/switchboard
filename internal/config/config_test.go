package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHarvestConfig(t *testing.T) {
	for _, tc := range []struct {
		body           string
		minimum, hours float64
	}{
		{"", 10, 6},
		{"harvest_min_headroom_pct=25.5", 25.5, 6},
		{"harvest_window_hours=0.5", 10, 0.5},
		{"harvest_min_headroom_pct=0\nharvest_window_hours=0", 0, 0},
		{"harvest_min_headroom_pct=100\nharvest_window_hours=1000", 100, 1000},
	} {
		t.Run(tc.body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path, "", "")
			if err != nil || c.HarvestMinHeadroomPct != tc.minimum || c.HarvestWindowHours != tc.hours {
				t.Fatalf("harvest config: minimum=%g hours=%g error=%v", c.HarvestMinHeadroomPct, c.HarvestWindowHours, err)
			}
		})
	}
	for key, values := range map[string][]string{
		"harvest_min_headroom_pct": {"-0.1", "100.1", "nan", "+nan", "-nan", "inf", "+inf", "-inf"},
		"harvest_window_hours":     {"-0.1", "nan", "+nan", "-nan", "inf", "+inf", "-inf"},
	} {
		for _, value := range values {
			t.Run(key+"="+value, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.toml")
				if err := os.WriteFile(path, []byte(fmt.Sprintf("%s=%s", key, value)), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := Load(path, "", ""); err == nil {
					t.Fatal("invalid harvest config accepted")
				}
			})
		}
	}
	// A missing config file uses the same built-in defaults as omitted settings.
	c, err := Load(filepath.Join(t.TempDir(), "missing.toml"), "", "")
	if err != nil || c.HarvestMinHeadroomPct != 10 || c.HarvestWindowHours != 6 {
		t.Fatal("missing config defaults")
	}
}

func TestPrecedenceAndRules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("default = \"claude/work\"\n[[rules]]\ncontains = \"test\"\naccount = \"codex/personal\"\n"), 0o600)
	c, e := Load(p, "codex/work", "claude/personal")
	if e != nil || c.Pin != "claude/personal" {
		t.Fatal("precedence")
	}
	order, reason := c.Order("TEST fake")
	if order[0] != "codex" || reason != "first matching rule" {
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
		{"usage_exhausted_pct=nan", .5, true},
		{"usage_exhausted_pct=101", .5, true},
		{"usage_cache_ttl='-1s'", .5, true},
		{"usage_cache_ttl='bad'", .5, true},
		{"usage_exhausted_pct=0\nusage_cache_ttl='0s'", .5, false},
		{"pin='codex'", .5, true},
		{"default='codex'", .5, false},
		{"[accounts.\"future/id\"]\nwhat='Future tool'", .5, false},
		{"[accounts.\"codex\"]\nwhat=''", .5, true},
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
	os.WriteFile(p, []byte("[accounts.\"codex\"]\nwhat='Custom implementation'\nexamples=[]"), 0o600)
	c, err := Load(p, "", "")
	if err != nil || c.Accounts["codex"].What != "Custom implementation" || c.Accounts["codex"].NotFor == "" || len(c.Accounts["codex"].Examples) != 0 {
		t.Fatal("field merge")
	}
}

func TestLogConfig(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{"", true},
		{"[log]\nenabled=false\nmax_bytes=1\nredact_task=true", true},
		{"[log]\nmax_bytes=0", false},
		{"[log]\nmax_bytes=-1", false},
		{"[log]\nmax_bytes=1.5", false},
		{"[log]\nenabled='yes'", false},
		{"[log]\nredact_task='false'", false},
		{"[log]\nunknown=true", false},
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path, "", "")
		if (err == nil) != tc.valid {
			t.Fatalf("log validation %q: %v", tc.body, err)
		}
		if tc.body == "" && (!c.Log.Enabled || c.Log.MaxBytes != 5242880 || c.Log.RedactTask) {
			t.Fatal("log defaults")
		}
	}
}

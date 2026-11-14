package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

func load(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, "", "")
}

const twoAgy = `
[[tool_accounts]]
id = "agy/work"
label = "Antigravity (work)"

[[tool_accounts]]
id = "agy/second"
label = "Antigravity (second)"
home = "/path/to/agy-second-profile"
quota_pool = "shared-pool-id"
`

func TestAgyAccounts(t *testing.T) {
	// No tool_accounts: implicit agy/personal.
	c, err := Load(filepath.Join(t.TempDir(), "missing.toml"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"agy/personal", "codex/work", "claude/personal"} {
		if a, e := c.ParseAccount(v); e != nil || a.ID() != v {
			t.Fatal("default account rejected", v)
		}
	}
	for _, v := range []string{"agy/work", "agy/gemini", "agy", "grok/cli"} {
		if _, e := c.ParseAccount(v); e == nil {
			t.Fatal("invalid account accepted", v)
		}
	}
	for _, v := range []string{"agy", "agy/personal"} {
		if f, e := c.Family(v); e != nil || f != "agy" {
			t.Fatal("agy family rejected", v)
		}
	}
	if _, e := c.Family("grok"); e == nil {
		t.Fatal("grok must stay non-launchable")
	}
	order, _ := c.Order("fake task")
	if !reflect.DeepEqual(order, []string{"codex", "claude", "agy"}) || !reflect.DeepEqual(Fallback(order), []string{"codex", "claude"}) || !reflect.DeepEqual(Fallback([]string{"agy", "claude", "codex"}), []string{"agy", "claude", "codex"}) {
		t.Fatal("agy order/fallback", order)
	}

	// Configured accounts replace the implicit default; pins and rules use them.
	c, err = load(t, twoAgy+"\npin = \"agy/second\"\n")
	if err == nil {
		t.Fatal("top-level key after array table must not parse as pin")
	}
	c, err = load(t, "pin = \"agy/second\"\ndefault = \"agy/work\"\n"+twoAgy)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Registry) != 6 || c.Registry[4].ID() != "agy/work" || c.Registry[4].Home != "" || c.Registry[5].Label != "Antigravity (second)" || c.Registry[5].Home != "/path/to/agy-second-profile" || c.Registry[5].QuotaPool != "shared-pool-id" {
		t.Fatal("configured registry", c.Registry)
	}
	if _, e := c.ParseAccount("agy/personal"); e == nil {
		t.Fatal("implicit default must disappear once accounts are configured")
	}
	if c.Pin != "agy/second" || c.Default != "agy/work" {
		t.Fatal("configured pin/default")
	}
	if _, err = load(t, "pin = \"agy/personal\"\n"+twoAgy); err == nil {
		t.Fatal("pin outside configured accounts accepted")
	}
}

func TestToolAccountsInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"bad id":           "[[tool_accounts]]\nid = \"agy\"\nlabel = \"A\"\n",
		"empty profile":    "[[tool_accounts]]\nid = \"agy/\"\nlabel = \"A\"\n",
		"empty id":         "[[tool_accounts]]\nlabel = \"A\"\n",
		"bad profile":      "[[tool_accounts]]\nid = \"agy/-x\"\nlabel = \"A\"\n",
		"non agy":          "[[tool_accounts]]\nid = \"codex/work\"\nlabel = \"A\"\n",
		"other tool":       "[[tool_accounts]]\nid = \"claude/third\"\nlabel = \"A\"\n",
		"missing label":    "[[tool_accounts]]\nid = \"agy/work\"\n",
		"blank label":      "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"  \"\n",
		"email label":      "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"someone@example.com\"\n",
		"duplicate id":     "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"A\"\n[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"B\"\n",
		"unknown field":    "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"A\"\nemail = \"x\"\n",
		"relative home":    "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"A\"\nhome = \"profiles/work\"\n",
		"bad pool":         "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"A\"\nquota_pool = \"a b\"\n",
		"not array":        "[tool_accounts]\nid = \"agy/work\"\nlabel = \"A\"\n",
		"criteria clash":   "[accounts.agy]\nid = \"agy/work\"\n",
		"wrong value type": "[[tool_accounts]]\nid = 3\nlabel = \"A\"\n",
	} {
		if _, err := load(t, body); err == nil {
			t.Fatal("invalid tool_accounts accepted:", name)
		}
	}
	c, err := load(t, "[[tool_accounts]]\nid = \"agy/work\"\nlabel = \"A\"\nhome = \"~/profiles/work\"\nquota_pool = \"\"\n")
	if err != nil || c.Registry[4].Home != "~/profiles/work" || c.Registry[4].QuotaPool != "" {
		t.Fatal("tilde home and empty pool must be accepted", err)
	}
}

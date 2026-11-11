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

package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"switchboard/internal/provider"
)

func TestCooldown(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "cooldowns.json")
	s, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	a := provider.Account{Tool: "codex", Profile: "work"}
	now := time.Now()
	s[a.ID()] = now.Add(time.Hour)
	if e = Save(p, s); e != nil {
		t.Fatal(e)
	}
	s, e = Load(p)
	if e != nil || s.Eligible(a, now) || !s.Eligible(a, now.Add(2*time.Hour)) {
		t.Fatal("cooldown persistence")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("state permissions")
	}
}

func TestCooldownKeysAreSyntactic(t *testing.T) {
	dir := t.TempDir()
	for body, ok := range map[string]bool{
		`{"agy/removed-profile":"2026-01-01T00:00:00Z"}`: true,
		`{"codex/work":"2026-01-01T00:00:00Z"}`:          true,
		`{"agy/":"2026-01-01T00:00:00Z"}`:                false,
		`{"grok/cli":"2026-01-01T00:00:00Z"}`:            false,
		`{"codex/other":"2026-01-01T00:00:00Z"}`:         false,
	} {
		p := filepath.Join(dir, "cooldowns.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); (err == nil) != ok {
			t.Fatal("cooldown key validation", body)
		}
	}
}

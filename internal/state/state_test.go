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

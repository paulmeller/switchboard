package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelsLoad(t *testing.T) {
	for _, body := range []string{
		"[models]\neasy_max=1.33\nhard_min=1.33",
		"[models]\neasy_max=nan",
		"[models]\nhard_min=2.1",
		"[models]\nunknown=1",
		"[models.codex]\neasy=''",
		"[models.codex]\nnormal=' trailing '",
		"[models.claude]\nnormal_reasoning_effort='low'",
		"[models.accounts.'codex/other']\nmodel='fake-test-value'",
	} {
		p := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p, "", ""); err == nil {
			t.Fatalf("accepted %q", body)
		}
	}
	c, err := Load(filepath.Join(t.TempDir(), "missing"), "", "")
	if err != nil || c.Models.EasyMax != .67 || c.Models.HardMin != 1.33 {
		t.Fatal(c.Models, err)
	}
}

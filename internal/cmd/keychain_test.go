package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeychainPresenceOnly(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
[ "$#" -eq 3 ] || exit 2
[ "$1" = find-generic-password ] || exit 2
[ "$2" = -s ] || exit 2
[ "$3" = claude-oauth-work ] || exit 1
echo 'discard attributes'
echo 'discard diagnostics' >&2
exit 0
`
	if os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0o700) != nil {
		t.Fatal("fake security")
	}
	t.Setenv("PATH", dir)
	if !keychainPresent("claude-oauth-work") || keychainPresent("claude-oauth-personal") || keychainPresent("other") {
		t.Fatal("presence-only allowlist")
	}
}

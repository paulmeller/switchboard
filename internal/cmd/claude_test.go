package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"switchboard/internal/decision"
	"switchboard/internal/usage"
)

func TestSelectedLaunchFailures(t *testing.T) {
	for _, routed := range []bool{false, true} {
		for _, kind := range []string{"missing dir", "file dir", "missing credentials", "missing tool", "start", "codex home"} {
			for _, noLog := range []bool{false, true} {
				t.Run(kind, func(t *testing.T) {
					home, bin, cfg := t.TempDir(), t.TempDir(), t.TempDir()
					family := "claude"
					if kind == "codex home" {
						family = "codex"
					}
					if kind != "missing tool" {
						if err := os.WriteFile(filepath.Join(bin, family), []byte("fake"), 0o700); err != nil {
							t.Fatal(err)
						}
					}
					t.Setenv("PATH", bin)
					dir := filepath.Join(home, "profile")
					if kind == "file dir" {
						if err := os.WriteFile(dir, []byte("fake"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					body := "[classifier]\nprovider=\"off\"\n"
					if family == "claude" && kind != "missing credentials" {
						body += "[[tool_accounts]]\nid=\"claude/work\"\nlabel=\"Claude (work)\"\nconfig_dir=\"~/profile\"\n"
					}
					if kind == "start" {
						if err := os.Mkdir(dir, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					if kind == "codex home" {
						if err := os.WriteFile(filepath.Join(home, ".codex"), []byte("fake"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.MkdirAll(filepath.Join(cfg, "switchboard"), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(cfg, "switchboard", "config.toml"), []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
					var out, diag bytes.Buffer
					called := false
					a := App{Home: home, Env: []string{"XDG_CONFIG_HOME=" + cfg}, Out: &out, Err: &diag, Probe: &fakeProbe{signed: true}, UsageSource: usage.Unsupported{}, Classifier: forbiddenClassifier{t}, Execute: func(string, []string, []string) (int, error) {
						called = true
						return 1, errors.New("private diagnostic")
					}}
					args := []string{family, "work"}
					if routed {
						args = []string{"--account", family + "/work", "run", "task"}
					}
					if noLog {
						args = append([]string{"--no-log"}, args...)
					}
					if code := a.Run(args); code != 1 {
						t.Fatalf("failure returned %d", code)
					}
					if called != (kind == "start") {
						t.Fatal("unexpected runner call")
					}
					if strings.Contains(diag.String(), "private diagnostic") {
						t.Fatal("runner error exposed")
					}
					records, err := decision.Read(decision.Path(home, ""))
					if err != nil {
						t.Fatal(err)
					}
					if noLog {
						if len(records) != 0 {
							t.Fatal("no-log wrote decision")
						}
						return
					}
					if len(records) != 1 {
						t.Fatal("expected single failure record")
					}
					d := records[0]
					if d.Family != family || d.Account != family+"/work" || d.ExitCode == nil || *d.ExitCode != 1 || d.DurationMS == nil || !strings.HasPrefix(d.Reason, "launch failed: ") || !strings.HasSuffix(d.Reason, "; pinned account; usage selection bypassed") {
						t.Fatalf("failure record: %+v", d)
					}
					if kind == "missing dir" || kind == "file dir" {
						if !strings.Contains(d.Reason, "Claude config_dir missing or not a directory; sign in with CLAUDE_CONFIG_DIR="+dir+" claude") {
							t.Fatal("missing directory guidance")
						}
					}
				})
			}
		}
	}
}

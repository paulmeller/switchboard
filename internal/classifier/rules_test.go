package classifier

import (
	"context"
	"testing"

	"switchboard/internal/config"
)

func TestRulesChooseFamily(t *testing.T) {
	r := Rules{Config: config.Config{Default: "codex/work", Rules: []config.Rule{{Contains: "design", Account: "claude/personal"}}}}
	for _, tc := range []struct {
		task     string
		eligible []string
		want     string
	}{{"design auth", []string{"codex", "claude"}, "claude"}, {"fix test", []string{"claude", "codex"}, "codex"}, {"fix test", []string{"claude"}, "claude"}, {"fix test", nil, ""}} {
		got := r.Choose(context.Background(), tc.task, tc.eligible, .5)
		if got.Family != tc.want || got.OK != (tc.want != "") {
			t.Fatal(got)
		}
	}
}

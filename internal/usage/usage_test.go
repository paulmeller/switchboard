package usage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"switchboard/internal/provider"
)

type fakeSource struct{ mode int }

func (f fakeSource) Read(ctx context.Context, tool, profile string) (Data, error) {
	if f.mode == 2 {
		<-ctx.Done()
		return Data{}, ctx.Err()
	}
	if f.mode == 1 && tool == "codex" {
		v := 25.0
		return Data{ObservedAt: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), Weekly: &v}, nil
	}
	return Data{}, errors.New("private diagnostic must never escape")
}

func TestDefaultRowsAndFailures(t *testing.T) {
	now := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)
	for _, mode := range []int{0, 1, 2} {
		r, code := Collect(context.Background(), fakeSource{mode}, provider.NewRegistry(nil), time.Millisecond, now)
		if len(r.Accounts) != 7 {
			t.Fatal("row count")
		}
		if (mode == 1) != (code == 0) {
			t.Fatal("exit status")
		}
		if !strings.HasSuffix(r.CheckedAt, "+11:00") {
			t.Fatal("Sydney offset")
		}
		b, e := json.Marshal(r)
		if e != nil || strings.Contains(string(b), "private") {
			t.Fatal("unsafe output")
		}
		var decoded map[string]any
		json.Unmarshal(b, &decoded)
		for _, raw := range decoded["accounts"].([]any) {
			row := raw.(map[string]any)
			for _, k := range []string{"account", "plan", "login_ok", "weekly_used_pct", "weekly_resets_at", "five_hour_used_pct", "five_hour_resets_at", "extra", "source", "error"} {
				if _, ok := row[k]; !ok {
					t.Fatal("missing key", k)
				}
			}
		}
		for _, row := range r.Accounts {
			if row.Source != nil && *row.Source != "cached normalized snapshot" {
				t.Fatal("false live provenance")
			}
		}
	}
}

func TestSnapshotValidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "codex-work.json")
	os.WriteFile(p, []byte(`{"observed_at":"2026-01-01T00:00:00Z","weekly_used_pct":101}`), 0o600)
	d, e := (Snapshot{dir}).Read(context.Background(), "codex", "work")
	if e != nil || valid(d, time.Now()) {
		t.Fatal("invalid percentage accepted")
	}
	os.WriteFile(p, []byte(`{"observed_at":"2026-01-01T00:00:00Z","prompt":"forbidden"}`), 0o600)
	if _, e = (Snapshot{dir}).Read(context.Background(), "codex", "work"); e == nil {
		t.Fatal("unexpected fields accepted")
	}
}

// The Source interface exposes no executor, classifier, prompt or command argv.
// Every usage path above is backed by a fake or a dedicated temporary snapshot.

func TestSnapshotSydneyTimestamps(t *testing.T) {
	for _, tc := range []struct{ utc, want string }{
		{"2026-01-01T00:00:00Z", "2026-01-01T11:00:00+11:00"},
		{"2026-07-01T00:00:00Z", "2026-07-01T10:00:00+10:00"},
	} {
		dir := t.TempDir()
		payload := `{"observed_at":"` + tc.utc + `","weekly_resets_at":"` + tc.utc + `","five_hour_resets_at":"` + tc.utc + `"}`
		if err := os.WriteFile(filepath.Join(dir, "codex-work.json"), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		now, _ := time.Parse(time.RFC3339, tc.utc)
		report, code := Collect(context.Background(), Snapshot{dir}, provider.NewRegistry(nil), time.Second, now)
		row := report.Accounts[2]
		if code != 0 || report.CheckedAt != tc.want || row.Extra["observed_at"] != tc.want || *row.WeeklyResetsAt != tc.want || *row.FiveHourResetsAt != tc.want {
			t.Fatal("snapshot timestamps must use Sydney's actual offset")
		}
	}
	invalid := "2026-07-01"
	if valid(Data{ObservedAt: time.Now(), WeeklyReset: &invalid}, time.Now()) {
		t.Fatal("date without timezone accepted")
	}
}

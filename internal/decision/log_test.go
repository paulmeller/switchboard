package decision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotationAndRecords(t *testing.T) {
	path := Path(t.TempDir(), "")
	ids := map[string]bool{}
	for range 3 {
		r, err := New("decision")
		if err != nil {
			t.Fatal(err)
		}
		if ids[r.ID] || len(r.ID) != 36 {
			t.Fatal("invalid UUID")
		}
		ids[r.ID] = true
		ts, err := time.Parse(time.RFC3339, r.TS)
		if err != nil {
			t.Fatal(err)
		}
		_, offset := ts.Zone()
		if offset != 10*3600 && offset != 11*3600 {
			t.Fatal("Sydney offset")
		}
		if err = Append(path, 1, Decision{Record: r}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{path, path + ".1"} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("log permissions")
		}
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("directory permissions")
	}
	records, err := Read(path)
	if err != nil || len(records) != 2 {
		t.Fatal("rotation retention", err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 2 {
		t.Fatal("unexpected retained files")
	}
}

func TestFeedbackFalseIsPresent(t *testing.T) {
	b, err := json.Marshal(Feedback{Record: Record{Type: "feedback"}, OK: false})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["ok"] != false {
		t.Fatal("missing false ok")
	}
}

func TestReadInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("accepted malformed log")
	}
}

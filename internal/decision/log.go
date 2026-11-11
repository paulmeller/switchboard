// Package decision stores only normalized local routing decisions and labels.
package decision

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"
)

type Label struct {
	Family  string `json:"family"`
	Account string `json:"account"`
}
type Override struct {
	Account string `json:"account"`
	Pin     bool   `json:"pin"`
}
type Record struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	ID            string `json:"id"`
	TS            string `json:"ts"`
	DecisionID    string `json:"decision_id,omitempty"`
	Family        string `json:"family"`
	Account       string `json:"account"`
}

// Decision deliberately excludes candidates, environment and raw classifier data.
type Decision struct {
	Record
	DryRun               bool      `json:"dry_run"`
	Task                 *string   `json:"task"`
	TaskSHA256           string    `json:"task_sha256"`
	Source               string    `json:"source"`
	Probability          *float64  `json:"probability"`
	Confidence           *float64  `json:"confidence"`
	Difficulty           *float64  `json:"difficulty"`
	DifficultyConfidence *float64  `json:"difficulty_confidence"`
	Reason               string    `json:"reason"`
	Override             *Override `json:"override"`
	Label                *Label    `json:"label,omitempty"`
	ExitCode             *int      `json:"exit_code"`
	DurationMS           *int64    `json:"duration_ms"`
}
type Feedback struct {
	Record
	OK bool `json:"ok"`
}

func New(kind string) (Record, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Record{}, errors.New("cannot create decision id")
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	h := hex.EncodeToString(b[:])
	loc, _ := time.LoadLocation("Australia/Sydney")
	return Record{SchemaVersion: 1, Type: kind, ID: h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], TS: time.Now().In(loc).Format(time.RFC3339)}, nil
}

func Path(home, xdg string) string {
	if xdg == "" {
		xdg = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(xdg, "switchboard", "decisions.jsonl")
}

func Append(path string, max int64, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return errors.New("cannot encode decision log")
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0o700) != nil || os.Chmod(dir, 0o700) != nil {
		return errors.New("cannot create decision directory")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("invalid decision log file")
		}
		if os.Chmod(path, 0o600) != nil {
			return errors.New("cannot restrict decision log")
		}
		if info.Size()+int64(len(b)) > max {
			if os.Rename(path, path+".1") != nil {
				return errors.New("cannot rotate decision log")
			}
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect decision log")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("cannot open decision log")
	}
	defer f.Close()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		return errors.New("cannot append decision log")
	}
	return nil
}

// Read includes the retained generation in chronological append order.
func Read(path string) ([]Decision, error) {
	records := []Decision{}
	for _, name := range []string{path + ".1", path} {
		info, err := os.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("invalid decision log file")
		}
		f, err := os.Open(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot read decision log")
		}
		dec := json.NewDecoder(f)
		for {
			var r Decision
			err = dec.Decode(&r)
			if err == io.EOF {
				break
			}
			if err != nil || r.SchemaVersion != 1 || (r.Type != "decision" && r.Type != "feedback") || r.ID == "" {
				f.Close()
				return nil, errors.New("invalid decision log")
			}
			records = append(records, r)
		}
		if f.Close() != nil {
			return nil, errors.New("cannot read decision log")
		}
	}
	return records, nil
}

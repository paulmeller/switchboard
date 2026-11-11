package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"switchboard/internal/provider"
)

type State map[string]time.Time

func Path(home, xdg string) string {
	if xdg == "" {
		xdg = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(xdg, "switchboard", "cooldowns.json")
}

func Load(path string) (State, error) {
	s := State{}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil || json.Unmarshal(b, &s) != nil {
		return nil, errors.New("cannot read cooldown state")
	}
	for k := range s {
		valid := false
		for _, a := range provider.Accounts() {
			if k == a.ID() {
				valid = true
			}
		}
		if !valid {
			return nil, errors.New("invalid cooldown state")
		}
	}
	return s, nil
}
func (s State) Eligible(a provider.Account, now time.Time) bool { return !s[a.ID()].After(now) }
func Save(path string, s State) error {
	return SaveJSON(path, s)
}

func SaveJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0o700) != nil {
		return errors.New("cannot create state directory")
	}
	if os.Chmod(dir, 0o700) != nil {
		return errors.New("cannot restrict state directory")
	}
	f, e := os.CreateTemp(dir, ".cooldown-*")
	if e != nil {
		return errors.New("cannot stage state")
	} // Failed temp files are retained rather than deleting existing files.
	if e = f.Chmod(0o600); e == nil {
		e = json.NewEncoder(f).Encode(value)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return errors.New("cannot write state")
	}
	if os.Rename(f.Name(), path) != nil {
		return errors.New("cannot replace state")
	}
	return nil
}

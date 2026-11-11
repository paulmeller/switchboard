package classifier

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"switchboard/internal/config"
)

// Keys isolates secret sources so tests never need real credentials or a Mac.
type Keys struct {
	Home     string
	Env      func(string) string
	ReadFile func(string) ([]byte, error)
	Keychain func(context.Context, bool, string) ([]byte, error)
	OS       string
}

func (k Keys) file(path string) string {
	if path == "~" {
		return k.Home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(k.Home, path[2:])
	}
	return path
}

func (k Keys) chain(presence bool, service string) ([]byte, error) {
	platform := k.OS
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "darwin" || service == "" {
		return nil, os.ErrNotExist
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if k.Keychain != nil {
		return k.Keychain(ctx, presence, service)
	}
	args := []string{"find-generic-password"}
	if !presence {
		args = append(args, "-w")
	}
	args = append(args, "-s", service)
	cmd := exec.CommandContext(ctx, "security", args...)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if presence {
		cmd.Stdout = io.Discard
		return nil, cmd.Run()
	}
	return cmd.Output()
}

func (k Keys) Resolve(c config.Classifier) string {
	if c.Provider == "off" {
		return ""
	}
	if k.Env != nil {
		if v := strings.TrimSpace(k.Env(c.APIKeyEnv)); v != "" {
			return v
		}
	}
	if c.APIKeyFile != "" {
		read := k.ReadFile
		if read == nil {
			read = os.ReadFile
		}
		if b, err := read(k.file(c.APIKeyFile)); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	b, err := k.chain(false, c.APIKeyKeychain)
	if err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// Present checks file readability and Keychain presence without reading a password.
func (k Keys) Present(c config.Classifier) bool {
	if c.Provider == "off" {
		return false
	}
	if k.Env != nil && strings.TrimSpace(k.Env(c.APIKeyEnv)) != "" {
		return true
	}
	if c.APIKeyFile != "" {
		f, err := os.Open(k.file(c.APIKeyFile))
		if err == nil {
			info, err := f.Stat()
			f.Close()
			if err == nil && info.Mode().IsRegular() {
				return true
			}
		}
	}
	_, err := k.chain(true, c.APIKeyKeychain)
	return err == nil
}

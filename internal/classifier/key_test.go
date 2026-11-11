package classifier

import (
	"context"
	"errors"
	"testing"

	"switchboard/internal/config"
)

func TestKeyResolution(t *testing.T) {
	for _, tc := range []struct {
		env, file, chain, want string
		fileErr                bool
		fileCalls, chainCalls  int
	}{
		{" fake-env ", "fake-file", "fake-chain", "fake-env", false, 0, 0},
		{" ", " fake-file ", "fake-chain", "fake-file", false, 1, 0},
		{"", "", " fake-chain ", "fake-chain", false, 1, 1},
		{"", "", "", "", true, 1, 1},
	} {
		files, chains := 0, 0
		keys := Keys{
			Home: "/fake-home", OS: "darwin", Env: func(string) string { return tc.env },
			ReadFile: func(path string) ([]byte, error) {
				files++
				if path != "/fake-home/fake.key" {
					t.Fatal("tilde expansion")
				}
				if tc.fileErr {
					return nil, errors.New("unreadable")
				}
				return []byte(tc.file), nil
			},
			Keychain: func(ctx context.Context, presence bool, service string) ([]byte, error) {
				chains++
				if presence || service != "fake-service" {
					t.Fatal("keychain arguments")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded keychain")
				}
				return []byte(tc.chain), nil
			},
		}
		c := config.Classifier{Provider: "typesafe", APIKeyEnv: "FAKE_ENV", APIKeyFile: "~/fake.key", APIKeyKeychain: "fake-service"}
		if keys.Resolve(c) != tc.want || files != tc.fileCalls || chains != tc.chainCalls {
			t.Fatal("key precedence")
		}
		c.Provider = "off"
		if keys.Resolve(c) != "" || files != tc.fileCalls || chains != tc.chainCalls {
			t.Fatal("off resolved key")
		}
	}
}

func TestMissingFileAndNonDarwin(t *testing.T) {
	k := Keys{OS: "linux", Keychain: func(context.Context, bool, string) ([]byte, error) { t.Fatal("non-darwin keychain"); return nil, nil }}
	c := config.Classifier{APIKeyFile: t.TempDir() + "/missing", APIKeyKeychain: "fake-service"}
	if k.Resolve(c) != "" || k.Present(c) {
		t.Fatal("missing source")
	}
	c.APIKeyFile = t.TempDir()
	if k.Resolve(c) != "" || k.Present(c) {
		t.Fatal("unreadable source")
	}
}

func TestKeychainPresence(t *testing.T) {
	k := Keys{OS: "darwin", Keychain: func(_ context.Context, presence bool, _ string) ([]byte, error) {
		if !presence {
			t.Fatal("doctor requested password")
		}
		return nil, nil
	}}
	if !k.Present(config.Classifier{APIKeyKeychain: "fake-service"}) {
		t.Fatal("missing presence")
	}
}

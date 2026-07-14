package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetEnv unsets every DROIDMCP_* variable for the duration of the test so
// the host environment cannot bleed into a result. t.Setenv handles the
// restore automatically.
func resetEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, envPrefix+"_") {
			key := kv[:strings.IndexByte(kv, '=')]
			t.Setenv(key, "")
			_ = os.Unsetenv(key)
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	resetEnv(t)
	// Default ROOT is "/" which exists on every supported platform.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("port: got %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.Root != defaultRoot {
		t.Errorf("root: got %q, want %q", cfg.Root, defaultRoot)
	}
}

func TestLoadConfigEnvOverride(t *testing.T) {
	resetEnv(t)
	root := t.TempDir()
	t.Setenv("DROIDMCP_PORT", "4100")
	t.Setenv("DROIDMCP_ROOT", root)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != 4100 {
		t.Errorf("port: %d", cfg.Port)
	}
	if cfg.Root != root {
		t.Errorf("root: %q", cfg.Root)
	}
}

func TestLoadConfigPortOutOfRange(t *testing.T) {
	resetEnv(t)
	cases := []string{"0", "-1", "70000", "100000"}
	for _, p := range cases {
		t.Run("port="+p, func(t *testing.T) {
			t.Setenv("DROIDMCP_PORT", p)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("expected error for port %s", p)
			}
		})
	}
}

func TestLoadConfigPortInRangeBoundaries(t *testing.T) {
	resetEnv(t)
	for _, p := range []string{"1", "65535"} {
		t.Run("port="+p, func(t *testing.T) {
			t.Setenv("DROIDMCP_PORT", p)
			if _, err := LoadConfig(); err != nil {
				t.Fatalf("port %s should be valid: %v", p, err)
			}
		})
	}
}

func TestLoadConfigEmptyRoot(t *testing.T) {
	resetEnv(t)
	// Setting ROOT to an empty string is rejected.
	t.Setenv("DROIDMCP_ROOT", "")
	// Viper falls back to defaults when an env var is empty, so we
	// instead build the config manually to exercise validate().
	cfg := &Config{Port: defaultPort, Root: ""}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected validate to fail on empty root")
	}
}

func TestLoadConfigNonexistentRoot(t *testing.T) {
	resetEnv(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("DROIDMCP_ROOT", missing)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected error for nonexistent root")
	}
}

func TestLoadConfigRootIsFileNotDir(t *testing.T) {
	resetEnv(t)
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROIDMCP_ROOT", file)
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when root is a regular file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got %v", err)
	}
}

func TestResolveAPIKeyPrecedence(t *testing.T) {
	resetEnv(t)
	t.Setenv("DROIDMCP_API_KEY", "global")
	if got := ResolveAPIKey("filesystem"); got != "global" {
		t.Errorf("fallback: %q", got)
	}
	t.Setenv("DROIDMCP_FILESYSTEM_KEY", "per-server")
	if got := ResolveAPIKey("filesystem"); got != "per-server" {
		t.Errorf("per-server should win: %q", got)
	}
	if got := ResolveAPIKey("github"); got != "global" {
		t.Errorf("unrelated server should fall back to global: %q", got)
	}
}

func TestResolveAPIKeyEmpty(t *testing.T) {
	resetEnv(t)
	if got := ResolveAPIKey("filesystem"); got != "" {
		t.Errorf("expected empty when no keys set, got %q", got)
	}
}

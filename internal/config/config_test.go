package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	cfg, err := LoadConfig("filesystem")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != 3000 {
		t.Errorf("port: got %d, want 3000", cfg.Port)
	}
	if cfg.Server != "filesystem" {
		t.Errorf("server: %q", cfg.Server)
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

	cfg, err := LoadConfig("filesystem")
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
			if _, err := LoadConfig("filesystem"); err == nil {
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
			if _, err := LoadConfig("filesystem"); err != nil {
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
	cfg := &Config{Port: 3000, Root: "", Transport: TransportHTTP}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected validate to fail on empty root")
	}
	if !strings.Contains(err.Error(), "DROIDMCP_ROOT") {
		t.Errorf("error should be about the root, got %v", err)
	}
}

func TestLoadConfigNonexistentRoot(t *testing.T) {
	resetEnv(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("DROIDMCP_ROOT", missing)
	if _, err := LoadConfig("filesystem"); err == nil {
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
	_, err := LoadConfig("filesystem")
	if err == nil {
		t.Fatal("expected error when root is a regular file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got %v", err)
	}
}

func TestLoadConfigPerServerDefaultPort(t *testing.T) {
	resetEnv(t)
	for server, want := range map[string]int{"filesystem": 3000, "termux": 3003, "llmproxy": 3012, "automation": 3013} {
		cfg, err := LoadConfig(server)
		if err != nil {
			t.Fatalf("LoadConfig(%q): %v", server, err)
		}
		if cfg.Port != want {
			t.Errorf("%s: default port %d, want %d", server, cfg.Port, want)
		}
	}
	// DROIDMCP_PORT still wins over the per-server default.
	t.Setenv("DROIDMCP_PORT", "4200")
	cfg, err := LoadConfig("termux")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 4200 {
		t.Errorf("override: got %d, want 4200", cfg.Port)
	}
}

func TestLoadConfigUnknownServer(t *testing.T) {
	resetEnv(t)
	if _, err := LoadConfig("nope"); err == nil {
		t.Fatal("an unknown server name should be an error, not port 0")
	}
}

// Every server under cmd/ needs a default port, and no two may share one, or
// two servers started without DROIDMCP_PORT would collide.
func TestDefaultPortsCoverEveryServer(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := defaultPorts[e.Name()]; e.IsDir() && !ok {
			t.Errorf("cmd/%s has no entry in defaultPorts", e.Name())
		}
	}
	seen := map[int]string{}
	for server, port := range defaultPorts {
		if other, dup := seen[port]; dup {
			t.Errorf("port %d is the default of both %s and %s", port, server, other)
		}
		seen[port] = server
	}
}

func TestLoadConfigDisabledTools(t *testing.T) {
	resetEnv(t)
	t.Setenv("DROIDMCP_TERMUX_DISABLED_TOOLS", " run_command, termux_sms_send ,,run_command")
	cfg, err := LoadConfig("termux")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"run_command", "termux_sms_send"}; !reflect.DeepEqual(cfg.DisabledTools, want) {
		t.Errorf("disabled tools: got %q, want %q", cfg.DisabledTools, want)
	}
	if !cfg.ToolDisabled("termux_sms_send") || cfg.ToolDisabled("read_env") {
		t.Errorf("ToolDisabled disagrees with %q", cfg.DisabledTools)
	}
	// The list is per server: another server's variable does not apply.
	other, err := LoadConfig("sms")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.DisabledTools) != 0 {
		t.Errorf("mcp-sms picked up termux's list: %q", other.DisabledTools)
	}
	if got := DisabledToolsVar("llmproxy"); got != "DROIDMCP_LLMPROXY_DISABLED_TOOLS" {
		t.Errorf("DisabledToolsVar: %q", got)
	}
}

func TestLoadConfigTransportDefaultsToHTTP(t *testing.T) {
	resetEnv(t)
	cfg, err := LoadConfig("filesystem")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Transport != TransportHTTP || cfg.Stdio() {
		t.Errorf("transport: got %q (stdio=%v), want %q", cfg.Transport, cfg.Stdio(), TransportHTTP)
	}
}

func TestLoadConfigTransportStdio(t *testing.T) {
	resetEnv(t)
	// Case and surrounding space are not significant.
	for _, v := range []string{"stdio", " STDIO "} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("DROIDMCP_TRANSPORT", v)
			cfg, err := LoadConfig("filesystem")
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if !cfg.Stdio() {
				t.Errorf("DROIDMCP_TRANSPORT=%q should select stdio, got %q", v, cfg.Transport)
			}
		})
	}
}

func TestLoadConfigTransportInvalid(t *testing.T) {
	resetEnv(t)
	// "sse" is not a value: the HTTP transport serves SSE as well.
	for _, v := range []string{"sse", "tcp", "std io"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("DROIDMCP_TRANSPORT", v)
			_, err := LoadConfig("filesystem")
			if err == nil {
				t.Fatalf("expected error for DROIDMCP_TRANSPORT=%q", v)
			}
			if !strings.Contains(err.Error(), "DROIDMCP_TRANSPORT") {
				t.Errorf("error should name the variable, got %v", err)
			}
		})
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

func TestParseRateLimit(t *testing.T) {
	valid := map[string]RateLimit{
		"10/h":    {Calls: 10, Per: time.Hour},
		" 5 / m ": {Calls: 5, Per: time.Minute},
		"1/s":     {Calls: 1, Per: time.Second},
		"100/d":   {Calls: 100, Per: 24 * time.Hour},
		"off":     {},
		"OFF":     {},
	}
	for spec, want := range valid {
		got, err := ParseRateLimit(spec)
		if err != nil || got != want {
			t.Errorf("ParseRateLimit(%q) = %+v, %v; want %+v", spec, got, err, want)
		}
	}
	for _, spec := range []string{"", "10", "10/w", "0/m", "-1/m", "x/m", "1.5/m", "100001/s", "10/hour"} {
		if _, err := ParseRateLimit(spec); err == nil {
			t.Errorf("ParseRateLimit(%q) should fail", spec)
		}
	}
	if got := (RateLimit{Calls: 10, Per: time.Hour}).String(); got != "10/h" {
		t.Errorf("String: %q", got)
	}
}

func TestLoadConfigRateLimits(t *testing.T) {
	resetEnv(t)
	t.Setenv("DROIDMCP_SMS_RATE_LIMITS", " send_sms=5/h, *=120/m ,list_sms=off")
	cfg, err := LoadConfig("sms")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]RateLimit{
		"send_sms": {Calls: 5, Per: time.Hour},
		AllTools:   {Calls: 120, Per: time.Minute},
		"list_sms": {},
	}
	if !reflect.DeepEqual(cfg.RateLimits, want) {
		t.Errorf("rate limits: got %+v, want %+v", cfg.RateLimits, want)
	}
	if got := RateLimitsVar("llmproxy"); got != "DROIDMCP_LLMPROXY_RATE_LIMITS" {
		t.Errorf("RateLimitsVar: %q", got)
	}
}

func TestLoadConfigRateLimitsInvalid(t *testing.T) {
	resetEnv(t)
	for _, raw := range []string{"send_sms", "=5/h", "send_sms=5", "send_sms=5/h,send_sms=1/m"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("DROIDMCP_SMS_RATE_LIMITS", raw)
			_, err := LoadConfig("sms")
			if err == nil {
				t.Fatalf("expected an error for %q", raw)
			}
			if !strings.Contains(err.Error(), "DROIDMCP_SMS_RATE_LIMITS") {
				t.Errorf("error should name the variable: %v", err)
			}
		})
	}
}

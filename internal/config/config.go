// Package config handles environment-based configuration.
// DroidMCP follows a 12-factor approach: every knob is an environment variable
// prefixed with DROIDMCP_. This package owns only the two values every server
// shares — Port and Root — and validates them at startup. Server-specific
// variables (e.g. DROIDMCP_TERMUX_ALLOWLIST, DROIDMCP_NETWORK_DB) are read
// directly with os.Getenv at their point of use, keeping a single, predictable
// configuration idiom across the codebase.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Env prefix and defaults are kept as package constants so tests and callers
// can refer to the same values without restating them.
const (
	envPrefix = "DROIDMCP"

	defaultPort = 3000
	defaultRoot = "/"

	minPort = 1
	maxPort = 65535
)

// Config holds the two configuration values shared by every server: the HTTP
// Port the SSE listener binds to, and the filesystem Root (acted on only by
// mcp-filesystem). Both are validated by LoadConfig.
type Config struct {
	Port int    // HTTP port for the MCP server
	Root string // Root directory for filesystem operations
}

// LoadConfig reads DROIDMCP_PORT and DROIDMCP_ROOT from the environment,
// applies defaults and validates them. Viper is used locally to source the env
// values and then discarded: no configuration state is retained beyond the
// returned Config, and per-process isolation keeps tests independent.
func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetDefault("PORT", defaultPort)
	v.SetDefault("ROOT", defaultRoot)

	v.SetEnvPrefix(envPrefix)
	// Replace dots with underscores in env keys to support nested structs if needed.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := &Config{
		Port: v.GetInt("PORT"),
		Root: v.GetString("ROOT"),
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate enforces invariants the rest of the code depends on. It runs
// once at LoadConfig time so per-server main() functions can fail-fast
// with a clear message instead of crashing later on a bad port or a
// missing root directory.
func (c *Config) validate() error {
	if c.Port < minPort || c.Port > maxPort {
		return fmt.Errorf("DROIDMCP_PORT out of range: got %d, want [%d, %d]", c.Port, minPort, maxPort)
	}
	if c.Root == "" {
		return errors.New("DROIDMCP_ROOT must not be empty")
	}
	info, err := os.Stat(c.Root)
	if err != nil {
		return fmt.Errorf("DROIDMCP_ROOT %q: %w", c.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("DROIDMCP_ROOT %q: not a directory", c.Root)
	}
	return nil
}

// ResolveAPIKey returns the API key the named server should enforce on inbound
// requests. It checks the per-server variable DROIDMCP_<NAME>_KEY first, then
// falls back to the global DROIDMCP_API_KEY. An empty result means no auth is
// configured (dev mode); callers that require a key must enforce that themselves.
func ResolveAPIKey(serverName string) string {
	specific := envPrefix + "_" + strings.ToUpper(serverName) + "_KEY"
	if k := os.Getenv(specific); k != "" {
		return k
	}
	return os.Getenv(envPrefix + "_API_KEY")
}

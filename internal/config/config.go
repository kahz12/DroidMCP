// Package config handles environment-based configuration using Viper.
// DroidMCP follows a 12-factor app approach for configuration.
package config

import (
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the application-wide configuration parameters.
type Config struct {
	Port int    // HTTP port for the MCP server
	Root string // Root directory for filesystem operations
}

// LoadConfig initializes a fresh Viper instance and loads configuration from
// environment variables. All variables are prefixed with DROIDMCP_ (e.g.,
// DROIDMCP_PORT). Using viper.New() instead of the package-global keeps state
// isolated per process and per test, so concurrent tests cannot trample each
// other's defaults.
func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetDefault("PORT", 3000)
	v.SetDefault("ROOT", "/")

	v.SetEnvPrefix("DROIDMCP")
	// Replace dots with underscores in env keys to support nested structs if needed.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	return &Config{
		Port: v.GetInt("PORT"),
		Root: v.GetString("ROOT"),
	}, nil
}

// ResolveAPIKey returns the API key the named server should enforce on inbound
// requests. It checks the per-server variable DROIDMCP_<NAME>_KEY first, then
// falls back to the global DROIDMCP_API_KEY. An empty result means no auth is
// configured (dev mode); callers that require a key must enforce that themselves.
func ResolveAPIKey(serverName string) string {
	specific := "DROIDMCP_" + strings.ToUpper(serverName) + "_KEY"
	if k := os.Getenv(specific); k != "" {
		return k
	}
	return os.Getenv("DROIDMCP_API_KEY")
}

// Package config handles environment-based configuration using Viper.
// DroidMCP follows a 12-factor app approach for configuration.
package config

import (
	"strings"

	"github.com/spf13/viper"
)

// Config holds the application-wide configuration parameters.
type Config struct {
	Port int    // HTTP port for the MCP server
	Root string // Root directory for filesystem operations
}

// LoadConfig initializes Viper and loads configuration from environment variables.
// All variables are prefixed with DROIDMCP_ (e.g., DROIDMCP_PORT).
func LoadConfig() (*Config, error) {
	viper.SetDefault("PORT", 3000)
	viper.SetDefault("ROOT", "/")

	viper.SetEnvPrefix("DROIDMCP")
	// Replace dots with underscores in env keys to support nested structs if needed.
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	return &Config{
		Port: viper.GetInt("PORT"),
		Root: viper.GetString("ROOT"),
	}, nil
}

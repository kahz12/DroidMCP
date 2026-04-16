package config

import (
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Port int
	Root string
}

func LoadConfig() (*Config, error) {
	viper.SetDefault("PORT", 3000)
	viper.SetDefault("ROOT", "/")

	viper.SetEnvPrefix("DROIDMCP")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	return &Config{
		Port: viper.GetInt("PORT"),
		Root: viper.GetString("ROOT"),
	}, nil
}

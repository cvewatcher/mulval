package config

import (
	"os"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
)

const (
	configCLIName = "config"
)

// ConfigFlag for CLI to load the configuration file
var ConfigFlag = &cli.StringFlag{
	Name:      configCLIName,
	Usage:     "The path to the configuration file.",
	Value:     "config.yaml",
	Sources:   cli.EnvVars("CONFIG"),
	TakesFile: true,
	OnlyOnce:  true,
	Local:     true,
}

// Load a configuration file from the CLI flags.
func Load(cmd *cli.Command) (*Config, error) {
	cfg := New()

	p := cmd.String(configCLIName)
	cf, err := os.Open(p)
	if err != nil {
		// If the file does not exist, no problem we default it
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer func() {
		_ = cf.Close()
	}()

	dec := yaml.NewDecoder(cf)
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

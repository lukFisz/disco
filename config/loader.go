package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads the YAML config file at path, parses it, fills each Command's
// Name from its map key, validates it (which also merges Defaults onto
// commands that don't override them), and returns the resulting Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	for name, cmd := range cfg.Commands {
		cmd.Name = name
		cfg.Commands[name] = cmd
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config file %q: %w", path, err)
	}

	return &cfg, nil
}

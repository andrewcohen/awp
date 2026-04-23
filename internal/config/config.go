package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type UserAction struct {
	Command string `json:"command"`
	Alias   string `json:"alias"`
}

type Config struct {
	Hooks struct {
		Bootstrap []string `json:"bootstrap"`
	} `json:"hooks"`
	Actions map[string]UserAction `json:"actions"`
}

func Load(repoRoot string) (Config, error) {
	global, globalErr := loadFile(globalConfigPath())
	project, projectErr := loadFile(filepath.Join(repoRoot, ".awp", "config.json"))

	if globalErr != nil && !errors.Is(globalErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("global config: %w", globalErr)
	}
	if projectErr != nil && !errors.Is(projectErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("project config: %w", projectErr)
	}

	return merge(global, project), nil
}

func globalConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "awp", "config.json")
}

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %q: %w", path, err)
	}
	return cfg, nil
}

func merge(global, project Config) Config {
	out := project
	if out.Actions == nil {
		out.Actions = make(map[string]UserAction)
	}
	for name, action := range global.Actions {
		if _, exists := out.Actions[name]; !exists {
			out.Actions[name] = action
		}
	}
	if len(out.Hooks.Bootstrap) == 0 {
		out.Hooks.Bootstrap = global.Hooks.Bootstrap
	}
	return out
}

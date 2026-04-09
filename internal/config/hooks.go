package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type hookFile struct {
	Hooks struct {
		Bootstrap []string `json:"bootstrap"`
	} `json:"hooks"`
}

type FileHookProvider struct{}

func NewFileHookProvider() *FileHookProvider {
	return &FileHookProvider{}
}

func (p *FileHookProvider) PostWorkspaceStart(repoRoot string) ([]string, error) {
	configPath := filepath.Join(repoRoot, ".awp", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config %q: %w", configPath, err)
	}

	var cfg hookFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", configPath, err)
	}

	out := make([]string, 0, len(cfg.Hooks.Bootstrap))
	for _, cmd := range cfg.Hooks.Bootstrap {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		out = append(out, cmd)
	}
	return out, nil
}

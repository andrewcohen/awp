package config

import (
	"strings"
)

type FileHookProvider struct{}

func NewFileHookProvider() *FileHookProvider {
	return &FileHookProvider{}
}

func (p *FileHookProvider) PostWorkspaceStart(repoRoot string) ([]string, error) {
	cfg, err := Load(repoRoot)
	if err != nil {
		return nil, err
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

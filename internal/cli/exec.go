package cli

import (
	"context"
	"os/exec"
)

// Runner runs external commands.
type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

// ExecRunner is the production command runner.
type ExecRunner struct{}

func NewExecRunner() *ExecRunner {
	return &ExecRunner{}
}

func (r *ExecRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

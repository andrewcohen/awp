package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/andrewcohen/awp/internal/cli"
	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/doctor"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

func main() {
	exec := cli.NewExecRunner()
	jjClient := jj.New(exec)
	tmuxClient := tmux.New(exec)
	store := state.NewJSONStore()
	hookProvider := config.NewFileHookProvider()
	invocationDir, _ := os.Getwd()

	svc := workspace.NewService(workspace.Dependencies{
		JJ:            jjClient,
		Tmux:          tmuxClient,
		Store:         store,
		Hooks:         hookProvider,
		Runner:        exec,
		InvocationDir: invocationDir,
		Input:         os.Stdin,
		Out:           os.Stdout,
	})

	doctorSvc := doctor.New(doctor.Dependencies{Runner: exec, Hooks: hookProvider, Out: os.Stdout})

	app := cli.NewApp(svc, os.Stdout)
	app.SetDoctor(doctorSvc)
	if err := app.Run(os.Args[1:]); err != nil {
		if errors.Is(err, cli.ErrOpenCancelled) {
			os.Exit(2)
		}
		// The completion gate and the require-task gate both signal a block
		// with exit code 2; the reason is already on stderr for Claude to feed
		// back, so don't re-print it.
		if errors.Is(err, cli.ErrGateBlocked) || errors.Is(err, cli.ErrTaskRequired) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

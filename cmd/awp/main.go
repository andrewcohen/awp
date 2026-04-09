package main

import (
	"fmt"
	"os"

	"github.com/andrewcohen/awp/internal/cli"
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

	svc := workspace.NewService(workspace.Dependencies{
		JJ:    jjClient,
		Tmux:  tmuxClient,
		Store: store,
		Input: os.Stdin,
		Out:   os.Stdout,
	})

	app := cli.NewApp(svc, os.Stdout)
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

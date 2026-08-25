package cli

import (
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
)

// What awp tells an agent working in one of its workspaces, appended to the
// system prompt at launch.
//
// It is two sections that arrive together and are configured apart. The dev-loop
// half (watch.GeneratePreamble) exists only where a repo has a `dev_loop` in its
// config; the workspace half below holds for every workspace there is, because it
// is about awp's own model rather than about how this repo likes to work. Before
// they were composed there was only the first, so a repo without a dev loop got
// no preamble at all — and everything true of every awp agent had nowhere to be
// said.
//
// Composed here rather than in internal/watch for the same reason: that package
// is the dev loop, and a section about naming workspaces is not part of one.

// workspacePreamble tells the agent that the row it appears on is its to name.
//
// A workspace's *name* is three things at once — the directory, the zmx session,
// and usually the bookmark — so it is a slug, chosen before the work started by
// whoever typed the prompt. Six of those on a deck is six slugs to read past. The
// display name is the same row said in words, it is free and reversible, and the
// agent is the one that finds out what the work actually turned out to be.
//
// It is told with the command spelled out, environment variable and all. An agent
// told "you can retitle this workspace" and left to find the verb will guess one,
// run it, and report awp as broken; `awp w label` is not a name anybody guesses.
//
// $AWP_WORKSPACE rather than the workspace's name, because this text is written
// once per repository and read by every workspace's agent in it. The variable is
// in the environment of every agent awp starts (see workspaceEnvPairs), and where
// a session predates that, `awp w label` resolves the workspace from the working
// directory anyway.
func workspacePreamble() string {
	return strings.Join([]string{
		"You are working in an awp workspace, which appears as a row on the user's deck.",
		"",
		"That row's title is yours to set, and setting it is expected rather than allowed:",
		"",
		"    awp w label \"$AWP_WORKSPACE\" 'what this work is, in a few words'",
		"",
		"Retitle when the one-line answer to \"what is this workspace\" changes — after the",
		"first exchange that settles what you are actually doing, and again if the work",
		"turns into something else. Write a phrase a person would say out loud, not a slug:",
		"the workspace's name is already the slug, and the title is what is read instead of",
		"it. Running the command with no text clears the title and the row goes back to its",
		"name.",
		"",
		"The title is presentation only. The directory, the session and the bookmark keep",
		"the workspace's name, so nothing you or anyone else runs resolves anything from it.",
	}, "\n") + "\n"
}

// agentPreamble is everything a coding agent in this repo is told at launch.
//
// Empty is a real answer — it means nothing applies here — and the caller writes
// no file rather than an empty one.
func agentPreamble(repoRoot string) string {
	sections := []string{workspacePreamble()}
	cfg, _ := config.Load(repoRoot)
	if watch.IsConfigured(cfg) {
		if loop := strings.TrimSpace(watch.GeneratePreamble(watch.Resolve(cfg))); loop != "" {
			sections = append(sections, loop+"\n")
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n")) + "\n"
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
	"github.com/andrewcohen/awp/internal/zmx"
)

// Where a prompt to a workspace's agent goes.
//
// Two substrates can be holding that agent — a zmx session the deck hosts as a
// pane, or a tmux window — and picking the wrong one does not fail. tmux is asked
// for a session, does not find one, makes it, and starts a coding agent in it: a
// second agent, with the same AWP_WORKSPACE, reporting status and recording gates
// for a workspace whose real agent is on screen somewhere else and never heard
// the prompt. #201 and #219 were each one call site making that mistake.
//
// So the decision is made once, here, and every surface that talks to an agent
// takes the answer as an argument. The deck's `A`, the diff's ctrl+s, and the
// review flow's opening prompt are the same question asked from three places.

// promptSender delivers a prompt to a workspace's agent.
//
// The signature is deckui.PaneBackend's SendPrompt, so a pane host satisfies it
// as-is: a host that can show you the agent is by construction the thing that can
// be sent to it.
type promptSender func(item deckui.Item, text string, reporter deckui.Reporter) error

// agentPromptSender resolves which substrate a prompt goes to.
//
// A pane host answers for its own agent and there is no fallback behind it. That
// is the point of asking the host first: it refuses when no agent is running
// ("press a to start one"), and refusing is the correct answer, since the thing a
// fallback would do is start the second agent.
//
// Without a host the question is still live, because a surface can be hosted by a
// deck it cannot see: standalone `awp diff` run from inside a zdeck pane has no
// host object at all, and its workspace's agent is a zmx session all the same. So
// it asks whether one is running and sends there if so.
//
// tmux last, and only when nothing on zmx claims the workspace — where it remains
// the whole answer for `awp deck`, including starting the agent when there is
// none, which zmx cannot do from here.
func agentPromptSender(panes paneHost, runner Runner, tmuxClient *tmux.Client, svc workspace.Service) promptSender {
	if panes != nil {
		return panes.SendPrompt
	}
	return func(item deckui.Item, text string, reporter deckui.Reporter) error {
		name, err := liveZmxAgent(runner, item.ProjectName, item.WorkspaceName)
		if err != nil {
			// Refusing to guess, the way the rename guard does with the same
			// question. A wrong guess here is the invisible second agent, and being
			// told the substrate cannot be read is recoverable in a way that is not.
			return err
		}
		if name != "" {
			return sendPromptToZmxAgent(zmxClientFor(runner), item, text, reporter)
		}
		return sendPromptToAgent(tmuxClient, svc, item, text, reporter)
	}
}

// agentSessionName is the zmx session a workspace's agent runs in.
//
// One spelling. It was two — the pane host composed it from its own spec table
// while the rename guard passed the kind constant — which agreed only because
// the label in that table happens to equal the constant, and nothing said so.
func agentSessionName(project, workspaceName string) string {
	return zmx.SessionName(project, workspaceName, panes[deckui.PaneKindAgent].label)
}

// sendPromptToZmxAgent pastes a prompt into the workspace's agent session.
//
// It will not start an agent that is not running. zmx has no way to create a
// session detached with a real command as its own process (see zmx.AttachCmd), so
// the honest answer is to say so and name the key that fixes it.
func sendPromptToZmxAgent(client zmx.Client, item deckui.Item, text string, reporter deckui.Reporter) error {
	if reporter == nil {
		reporter = noopReporter{}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("send prompt: nothing to send")
	}
	ws := strings.TrimSpace(item.WorkspaceName)
	if ws == "" {
		return errors.New("send prompt: select a workspace row first")
	}

	name := agentSessionName(item.ProjectName, ws)
	session, found, err := client.Lookup(context.Background(), name)
	if err != nil {
		return err
	}
	if !found || !session.Live() {
		return fmt.Errorf("send prompt: no agent running for %s — press enter on its row to start one", ws)
	}

	reporter.Step("Send prompt to agent")
	return client.Paste(context.Background(), name, text)
}

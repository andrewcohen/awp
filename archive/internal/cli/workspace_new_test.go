package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `awp workspace new`. The creation itself is openWorkspaceWithReporter's, already
// covered where the deck's create path is; what is new is the way in and the
// promise that it takes the pane-hosted branch — the one that does not start a tmux
// agent nobody can see (#219).

func TestNewNeedsAName(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--project", "proj"},
		{"--prompt", "do the thing"},
	} {
		app, _, _ := sendApp(t, nil)
		err := app.runWorkspaceNew(args)
		if err == nil {
			t.Errorf("%v: expected an error", args)
			continue
		}
		if !strings.Contains(err.Error(), "name") {
			t.Errorf("%v: the error should ask for a name, got %v", args, err)
		}
	}
}

// Two bare words is almost always an unquoted name, and creating a workspace called
// the first of them is worse than refusing: it is a jj workspace and a directory to
// undo.
func TestNewRefusesTwoNames(t *testing.T) {
	app, _, _ := sendApp(t, nil)
	err := app.runWorkspaceNew([]string{"fix", "the badge"})
	if err == nil {
		t.Fatal("expected two positional words to be refused")
	}
	if !strings.Contains(err.Error(), "quote") {
		t.Errorf("the error should suggest quoting, got %v", err)
	}
}

func TestNewHelpPrintsUsage(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.runWorkspaceNew([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"awp w new", projectFlag, "--prompt", "--bookmark", "does not switch you into it"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, out)
		}
	}
}

func TestNewNamesAnUnresolvableProject(t *testing.T) {
	app, _, _ := sendApp(t, nil)
	err := app.runWorkspaceNew([]string{"--project", "nosuchproject", "ws"})
	if err == nil {
		t.Fatal("expected an unresolvable project to be refused")
	}
	if !strings.Contains(err.Error(), "nosuchproject") {
		t.Errorf("the error should name the project, got %v", err)
	}
}

func TestNewIsDispatchedAndListed(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.Run([]string{"w", "new", "--help"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out.String(), "awp w new") {
		t.Errorf("`awp w new --help` did not reach the verb:\n%s", out)
	}

	usage := &bytes.Buffer{}
	other := NewApp(&fakeService{}, usage)
	if err := other.Run([]string{"w"}); err != nil {
		t.Fatalf("workspace usage: %v", err)
	}
	if !strings.Contains(usage.String(), "new") {
		t.Errorf("the workspace usage does not list new:\n%s", usage)
	}
}

// takeValueFlag, in both spellings and with its own error. Shared by --prompt and
// --bookmark, and the next flag that wants a value.
func TestTakeValueFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		rest int
	}{
		{[]string{"--prompt", "do it", "ws"}, "do it", 1},
		{[]string{"--prompt=do it", "ws"}, "do it", 1},
		{[]string{"ws"}, "", 1},
		{[]string{"ws", "--prompt", "later"}, "later", 1},
	} {
		got, rest, err := takeValueFlag(tc.args, "--prompt")
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got != tc.want {
			t.Errorf("%v: value = %q, want %q", tc.args, got, tc.want)
		}
		if len(rest) != tc.rest {
			t.Errorf("%v: %d remaining args, want %d (%v)", tc.args, len(rest), tc.rest, rest)
		}
	}

	if _, _, err := takeValueFlag([]string{"--prompt"}, "--prompt"); err == nil {
		t.Error("a flag with no value should be an error")
	}
}

// The two flags that break silently. PaneHosted false starts a tmux agent nobody
// can see (#219); Yes false asks a question on a stdin no agent is typing at, and
// hangs rather than failing.
func TestNewCreatesWithoutAttachingAndWithoutAsking(t *testing.T) {
	req := newWorkspaceRequest("fix-badge", "", "do the thing")

	if !req.PaneHosted {
		t.Error("the request is not pane-hosted, so creating will start a tmux agent nothing on the deck can see")
	}
	if !req.Yes {
		t.Error("the request will ask for confirmation, which hangs a caller with nobody at its stdin")
	}
	if req.NoSwitch {
		t.Error("NoSwitch is for the async job's tmux path; pane-hosted creation never reaches a switch at all")
	}
	if req.Name != "fix-badge" || req.Prompt != "do the thing" {
		t.Errorf("the request carries %+v, want the name and prompt it was given", req)
	}
}

// Whitespace in, nothing out — for the name, the bookmark and the prompt alike.
func TestNewRequestTrimsItsFields(t *testing.T) {
	req := newWorkspaceRequest("  ws  ", "  bm  ", "   ")
	if req.Name != "ws" || req.Bookmark != "bm" {
		t.Errorf("untrimmed fields: %+v", req)
	}
	if req.Prompt != "" {
		t.Errorf("a whitespace prompt should be no prompt, got %q", req.Prompt)
	}
}

// An empty --prompt is not a prompt. Passing one through would have the create path
// decide there is work to start and start an agent on nothing.
func TestNewTreatsABlankPromptAsNone(t *testing.T) {
	value, _, err := takeValueFlag([]string{"--prompt", "   ", "ws"}, "--prompt")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.TrimSpace(value) != "" {
		t.Fatalf("expected whitespace to trim to nothing, got %q", value)
	}
}

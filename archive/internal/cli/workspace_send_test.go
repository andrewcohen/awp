package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp workspace send`. What is new here is the way in — the argument parsing, the
// target resolution and the errors — not the delivery, which is
// agentPromptSender's and already has its own tests. These cover the new part, and
// deliberately stop at the point where a real agent session would be needed.

func sendApp(t *testing.T, entries []workspace.ListEntry) (*App, *fakeService, *bytes.Buffer) {
	t.Helper()
	svc := &fakeService{listEntries: entries}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	return app, svc, out
}

func TestSendNeedsAWorkspaceAndSomethingToSay(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"ws"},
		{"--project", "proj"},
		{"--project", "proj", "ws"},
	} {
		app, _, _ := sendApp(t, nil)
		err := app.runWorkspaceSend(args)
		if err == nil {
			t.Errorf("%v: expected an error", args)
			continue
		}
		if !strings.Contains(err.Error(), "awp w send") {
			t.Errorf("%v: the error should show the usage, got %v", args, err)
		}
	}
}

// Whitespace is not something to say. Without this an accidental trailing quote
// pastes a blank line at an agent, which reads as the deck having lost the message.
func TestSendRefusesAnEmptyMessage(t *testing.T) {
	app, _, _ := sendApp(t, nil)
	err := app.runWorkspaceSend([]string{"ws", "   "})
	if err == nil {
		t.Fatal("expected whitespace to be refused")
	}
	if !strings.Contains(err.Error(), "ws") {
		t.Errorf("the refusal should name the workspace, got %v", err)
	}
}

func TestSendHelpPrintsUsage(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.runWorkspaceSend([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"awp w send", projectFlag, "will not start one"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, out)
		}
	}
}

// An unresolvable project is named, and nothing is sent. The captain's most likely
// mistake is a project that does not exist, so this is the error it will read most.
func TestSendNamesAnUnresolvableProject(t *testing.T) {
	app, _, _ := sendApp(t, nil)
	err := app.runWorkspaceSend([]string{"--project", "nosuchproject", "ws", "hello"})
	if err == nil {
		t.Fatal("expected an unresolvable project to be refused")
	}
	if !strings.Contains(err.Error(), "nosuchproject") {
		t.Errorf("the error should name the project, got %v", err)
	}
}

// The workspace lookup is where a typo gets caught, with the list in hand, rather
// than later as a session that cannot be found.
func TestResolveWorkspaceItemFindsThePath(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "other", Path: "/repo/other"},
		{Name: "fix-badge", Path: "/repo/fix-badge"},
	}}

	item, err := resolveWorkspaceItem(svc, "proj", "/repo", "fix-badge")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if item.WorkspaceName != "fix-badge" || item.Path != "/repo/fix-badge" {
		t.Errorf("resolved to %+v, want the fix-badge row and its path", item)
	}
	if item.ProjectName != "proj" || item.RepoRoot != "/repo" {
		t.Errorf("resolved to %+v, want project proj at /repo", item)
	}
}

func TestResolveWorkspaceItemListsWhatExists(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "alpha", Path: "/repo/alpha"},
		{Name: "beta", Path: "/repo/beta"},
	}}

	_, err := resolveWorkspaceItem(svc, "proj", "/repo", "gamma")
	if err == nil {
		t.Fatal("expected an unknown workspace to be refused")
	}
	for _, want := range []string{"gamma", "proj", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got %v", want, err)
		}
	}
}

// A project with no workspaces says that, rather than printing "known: " with
// nothing after it — which reads as a truncated message.
func TestResolveWorkspaceItemSaysWhenThereAreNone(t *testing.T) {
	_, err := resolveWorkspaceItem(&fakeService{}, "proj", "/repo", "anything")
	if err == nil {
		t.Fatal("expected an error with no workspaces at all")
	}
	if strings.Contains(err.Error(), "known:") {
		t.Errorf("with no workspaces the error should not offer a list: %v", err)
	}
	if !strings.Contains(err.Error(), "none") {
		t.Errorf("the error should say the project has none, got %v", err)
	}
}

// projectNameFor is what the deck calls a repo, and the prompt sender matches
// session names built from it. A trailing separator must not change the answer, or
// a project named from a path with one addresses a different session than the same
// project named without.
func TestProjectNameIgnoresATrailingSeparator(t *testing.T) {
	if got, want := projectNameFor("/a/b/proj/"), "proj"; got != want {
		t.Errorf("projectNameFor with a trailing slash = %q, want %q", got, want)
	}
	if got, want := projectNameFor("/a/b/proj"), "proj"; got != want {
		t.Errorf("projectNameFor = %q, want %q", got, want)
	}
}

// The verb is reachable as `awp w send`, and listed where the other subcommands
// are. A verb the usage does not mention is one the captain cannot be told about.
func TestSendIsDispatchedAndListed(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.Run([]string{"w", "send", "--help"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out.String(), "awp w send") {
		t.Errorf("`awp w send --help` did not reach the verb:\n%s", out)
	}

	usage := &bytes.Buffer{}
	other := NewApp(&fakeService{}, usage)
	if err := other.Run([]string{"w"}); err != nil {
		t.Fatalf("workspace usage: %v", err)
	}
	if !strings.Contains(usage.String(), "send") {
		t.Errorf("the workspace usage does not list send:\n%s", usage)
	}
}

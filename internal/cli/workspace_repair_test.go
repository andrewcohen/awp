package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
)

// `awp workspace repair`. The prompt itself is the deck's and is tested there; what
// matters here is that this verb asks for the same one, decides the tone the same
// way, and refuses clearly when it cannot find a PR.

// The distinction #171 exists for: whose PR it is changes what the agent is asked to
// do. A reviewer told to fix and push starts rebasing someone else's branch.
func TestRepairToneFollowsWhosePRItIs(t *testing.T) {
	open := deckui.PRStatus{Number: 7, State: deckui.PRStateOpen, CIState: deckui.PRCIFailing}

	mine := deckui.PRRepairPrompt(open, "", true)
	theirs := deckui.PRRepairPrompt(open, "", false)

	if mine == "" || theirs == "" {
		t.Fatalf("a failing open PR should produce both prompts (mine=%d theirs=%d chars)", len(mine), len(theirs))
	}
	if mine == theirs {
		t.Fatal("the owner's prompt and the reviewer's are identical, so #171 is back")
	}
	// The reviewer's prompt forbids pushing rather than merely not asking for it.
	// Asserting the absence of the word was wrong: the prohibition is what stops an
	// agent that would otherwise infer "fix it" from "here is what is broken", and
	// the prohibition has to say the word to forbid it.
	lower := strings.ToLower(theirs)
	if !strings.Contains(lower, "do not modify files") || !strings.Contains(lower, "push") {
		t.Errorf("the reviewer's prompt does not forbid changing the branch:\n%s", theirs)
	}
	if !strings.Contains(lower, "report back") {
		t.Errorf("the reviewer's prompt does not ask for a report:\n%s", theirs)
	}
	// The owner's asks for the opposite.
	if !strings.Contains(strings.ToLower(mine), "push") {
		t.Errorf("the owner's prompt never mentions pushing:\n%s", mine)
	}
	if got, want := repairTone(true), "fix-and-push"; got != want {
		t.Errorf("repairTone(true) = %q, want %q", got, want)
	}
	if got, want := repairTone(false), "investigate-only"; got != want {
		t.Errorf("repairTone(false) = %q, want %q", got, want)
	}
}

// PRIsMine is the same question the deck's `C r` asks. An unlinked bookmark or an
// unset prefix answers "mine", because with nothing to compare against the safer
// wrong answer is to offer to fix rather than to silently decline.
func TestPRIsMineFollowsTheBookmarkPrefix(t *testing.T) {
	for _, tc := range []struct {
		bookmark, prefix string
		want             bool
	}{
		{"andrew/fix-badge", "andrew", true},
		{"someone/fix-badge", "andrew", false},
		{"", "andrew", true},
		{"anything", "", true},
	} {
		if got := deckui.PRIsMine(tc.bookmark, tc.prefix); got != tc.want {
			t.Errorf("PRIsMine(%q, %q) = %v, want %v", tc.bookmark, tc.prefix, got, tc.want)
		}
	}
}

// A closed or merged PR has nothing to repair, and the prompt is empty. The verb has
// to treat that as an answer rather than sending a blank message.
func TestNothingToRepairIsAnAnswerNotAnError(t *testing.T) {
	for _, state := range []deckui.PRState{deckui.PRStateMerged, deckui.PRStateClosed} {
		if got := deckui.PRRepairPrompt(deckui.PRStatus{Number: 7, State: state}, "", true); got != "" {
			t.Errorf("a %s PR produced a repair prompt:\n%s", state, got)
		}
	}
	// An open PR with nothing wrong, likewise.
	if got := deckui.PRRepairPrompt(deckui.PRStatus{Number: 7, State: deckui.PRStateOpen}, "", true); got != "" {
		t.Errorf("a healthy open PR produced a repair prompt:\n%s", got)
	}
}

// A workspace with neither a bookmark nor a pinned PR has no PR to repair, and the
// error says how to give it one rather than only that it failed.
func TestRepairSaysHowToLinkAPR(t *testing.T) {
	_, err := cachedPRStatusFor("/repo", "proj", workspace.ListEntry{Name: "ws"})
	if err == nil {
		t.Fatal("expected an error for a workspace with no bookmark and no PR")
	}
	// Either message is acceptable — an empty cache is caught first — but it has to
	// name the workspace or say what to do.
	msg := err.Error()
	if !strings.Contains(msg, "proj") && !strings.Contains(msg, "ws") {
		t.Errorf("the error names neither the project nor the workspace: %v", err)
	}
}

func TestRepairNeedsOneWorkspace(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"one", "two"},
	} {
		app, _, _ := sendApp(t, nil)
		err := app.runWorkspaceRepair(args)
		if err == nil {
			t.Errorf("%v: expected an error", args)
			continue
		}
		if !strings.Contains(err.Error(), "awp w repair") {
			t.Errorf("%v: the error should show the usage, got %v", args, err)
		}
	}
}

func TestRepairHelpPrintsUsage(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.runWorkspaceRepair([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"awp w repair", "--dry-run", "investigate and report"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, out)
		}
	}
}

func TestRepairIsDispatchedAndListed(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.Run([]string{"w", "repair", "--help"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out.String(), "awp w repair") {
		t.Errorf("`awp w repair --help` did not reach the verb:\n%s", out)
	}

	usage := &bytes.Buffer{}
	other := NewApp(&fakeService{}, usage)
	if err := other.Run([]string{"w"}); err != nil {
		t.Fatalf("workspace usage: %v", err)
	}
	if !strings.Contains(usage.String(), "repair") {
		t.Errorf("the workspace usage does not list repair:\n%s", usage)
	}
}

// workspaceEntry hands back the whole entry, not just a path: the bookmark decides
// the tone and the PR number is the fallback way to find the status.
func TestWorkspaceEntryCarriesTheBookmarkAndPR(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "ws", Path: "/repo/ws", Bookmark: "andrew/fix", PRNumber: 42},
	}}

	entry, err := workspaceEntry(svc, "proj", "ws")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if entry.Bookmark != "andrew/fix" || entry.PRNumber != 42 {
		t.Errorf("entry = %+v, want the bookmark and PR number carried through", entry)
	}
}

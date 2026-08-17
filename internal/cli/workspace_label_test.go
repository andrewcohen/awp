package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckdata"
	"github.com/andrewcohen/awp/internal/deckui"
)

// The display name. What matters is which of the three candidate labels wins, that
// clearing works, and that nothing downstream starts resolving things from it — the
// last of which is guarded structurally in internal/workspace/display_name_test.go.

// A label someone chose wins over the PR title. It is the more deliberate of the two:
// you typed it, or asked for it, where the PR title is GitHub's words about the
// change and is the default for the overwhelming majority of rows that have no label.
func TestALabelBeatsThePRTitleAndTheName(t *testing.T) {
	item := deckui.Item{ProjectName: "proj", WorkspaceName: "pr-1234-fix", RepoRoot: "/r", PRNumber: 7}
	withPR := deckdata.View{
		All: []deckui.Item{item},
		PRStatusByRepo: prStatusForDeckdata(map[string]map[string]deckui.PRStatus{
			"/r": {"head": {Number: 7, Title: "Fix the badge refresh", State: deckui.PRStateOpen}},
		}),
	}

	// No label: the PR title, which is the existing behaviour and must not regress.
	if got := withPR.DisplayLabel(item); !strings.Contains(got, "Fix the badge refresh") {
		t.Errorf("without a label the row reads %q, want the PR title", got)
	}

	// With one: the label.
	labelled := item
	labelled.DisplayName = "make the badge stop lying"
	if got := withPR.DisplayLabel(labelled); got != "make the badge stop lying" {
		t.Errorf("with a label the row reads %q, want the label", got)
	}

	// And with neither, the name — the case every workspace was in before this field.
	bare := deckdata.View{All: []deckui.Item{item}}
	if got := bare.DisplayLabel(item); got != "pr-1234-fix" {
		t.Errorf("with no label and no PR the row reads %q, want the workspace name", got)
	}
}

// Whitespace is not a label. Without the trim a row could be labelled with spaces and
// read as blank, which looks like the deck having lost the workspace's name.
func TestAWhitespaceLabelIsNoLabel(t *testing.T) {
	item := deckui.Item{WorkspaceName: "ws", DisplayName: "   "}
	if got := (deckdata.View{}).DisplayLabel(item); got != "ws" {
		t.Errorf("a whitespace label rendered as %q, want the workspace name", got)
	}
}

func TestLabelNeedsAWorkspace(t *testing.T) {
	app, _, _ := sendApp(t, nil)
	err := app.runWorkspaceLabel(nil)
	if err == nil {
		t.Fatal("expected an error with no workspace named")
	}
	if !strings.Contains(err.Error(), "awp w label") {
		t.Errorf("the error should show the usage, got %v", err)
	}
}

func TestLabelHelpSaysItChangesNothingElse(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.runWorkspaceLabel([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"awp w label", "changes nothing but what you read", "Renaming keeps the label"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage does not say %q:\n%s", want, out)
		}
	}
}

func TestLabelIsDispatchedAndListed(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.Run([]string{"w", "label", "--help"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out.String(), "awp w label") {
		t.Errorf("`awp w label --help` did not reach the verb:\n%s", out)
	}

	usage := &bytes.Buffer{}
	other := NewApp(&fakeService{}, usage)
	if err := other.Run([]string{"w"}); err != nil {
		t.Fatalf("workspace usage: %v", err)
	}
	if !strings.Contains(usage.String(), "label") {
		t.Errorf("the workspace usage does not list label:\n%s", usage)
	}
}

// `awp w new --label` documents itself, since that is the flag the captain uses and
// the one a person would otherwise not know exists.
func TestNewDocumentsTheLabelFlag(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.runWorkspaceNew([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out.String(), "--label") {
		t.Errorf("`awp w new --help` does not mention --label:\n%s", out)
	}
	if !strings.Contains(out.String(), "has to be a directory") {
		t.Errorf("the usage does not explain why the two differ:\n%s", out)
	}
}

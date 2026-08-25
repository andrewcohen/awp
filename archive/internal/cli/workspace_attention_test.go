package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckdata"
	"github.com/andrewcohen/awp/internal/deckui"
)

// `awp workspace attention`. The predicate is deckdata's and is tested there; the
// property worth pinning here is that this command is a printer for it rather than a
// second opinion — same View, same Scope, same Wants — plus the shape of what it
// prints, which becomes an interface the moment an agent parses it.

// The guard against re-derivation. If someone later computes attention here, this
// stops agreeing with the deck and nothing else would notice: both would produce a
// plausible list.
func TestAttentionRowsComeFromTheDecksOwnScope(t *testing.T) {
	// Unread matters: workspace.Classify only calls a "waiting" agent
	// attention-worthy when it has said something you have not read. A fixture
	// without it produces an empty scope, which is why the assertion below refuses
	// to pass on one.
	items := []deckui.Item{
		{ProjectName: "proj", WorkspaceName: "waiting", RepoRoot: "/r", Path: "/r/waiting", Status: "waiting", Unread: true, Active: true},
		{ProjectName: "proj", WorkspaceName: "quiet", RepoRoot: "/r", Path: "/r/quiet", Status: "idle"},
	}
	view := deckdata.View{All: items, Scope: deckdata.ScopeAttention}

	// What the deck would show, straight from the read model.
	var want []string
	for _, it := range view.Items() {
		want = append(want, it.WorkspaceName)
	}
	if len(want) == 0 {
		t.Fatal("the fixture produced no attention rows, so this test proves nothing — pick a status that wants attention")
	}

	// And the same construction this command uses, over the same items.
	got := attentionRowsFor(items, nil)
	if len(got) != len(want) {
		t.Fatalf("attention printed %d rows, the deck's scope has %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Workspace != want[i] {
			t.Errorf("row %d is %q, the deck's scope has %q — the order or membership differs", i, got[i].Workspace, want[i])
		}
	}
}

// Every row carries a reason. A row in the list with an empty why is the list saying
// "something" and leaving the reader to guess, which is what a captain would then
// guess wrong about.
func TestEveryAttentionRowSaysWhy(t *testing.T) {
	items := []deckui.Item{
		{ProjectName: "proj", WorkspaceName: "waiting", RepoRoot: "/r", Path: "/r/waiting", Status: "waiting", Active: true},
		{ProjectName: "proj", WorkspaceName: "errored", RepoRoot: "/r", Path: "/r/errored", Status: "error"},
	}
	for _, r := range attentionRowsFor(items, nil) {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("%s/%s is in the list with no reason", r.Project, r.Workspace)
		}
	}
}

// The JSON shape is the part an agent depends on, so the field names are pinned. A
// rename here is a breaking change to something that cannot read a changelog.
func TestAttentionJSONFieldNames(t *testing.T) {
	blob, err := json.Marshal(attentionRow{Project: "p", Workspace: "w", Reason: "why", PR: 7, Virtual: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"project"`, `"workspace"`, `"reason"`, `"pr"`, `"virtual"`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("the JSON has no %s field: %s", want, blob)
		}
	}
	// PR and Virtual are omitempty, so an ordinary row stays terse.
	blob, err = json.Marshal(attentionRow{Project: "p", Workspace: "w", Reason: "why"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, unwanted := range []string{`"pr"`, `"virtual"`} {
		if strings.Contains(string(blob), unwanted) {
			t.Errorf("an ordinary row still carries %s: %s", unwanted, blob)
		}
	}
}

// An empty list says so rather than printing nothing, which is indistinguishable
// from the command having failed silently.
func TestAttentionSaysWhenNothingWantsYou(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printAttention(out, nil, false); err != nil {
		t.Fatalf("print: %v", err)
	}
	if !strings.Contains(out.String(), "nothing wants your attention") {
		t.Errorf("an empty list printed %q", out)
	}
}

// A virtual row — a PR whose review is requested from you, with no local workspace —
// says so, because there is nothing there to send a prompt to.
func TestAttentionMarksARowWithNoWorkspace(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printAttention(out, []attentionRow{{Project: "p", Workspace: "pr-9", Reason: "needs your review", Virtual: true}}, false); err != nil {
		t.Fatalf("print: %v", err)
	}
	if !strings.Contains(out.String(), "no local workspace") {
		t.Errorf("a virtual row does not say it has no workspace: %q", out)
	}
}

func TestAttentionHelpAndUnknownArgs(t *testing.T) {
	app, _, out := sendApp(t, nil)
	if err := app.runWorkspaceAttention([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out.String(), "awp w attention") {
		t.Errorf("usage does not name the command:\n%s", out)
	}

	app2, _, _ := sendApp(t, nil)
	err := app2.runWorkspaceAttention([]string{"--nope"})
	if err == nil {
		t.Fatal("expected an unknown argument to be refused")
	}
	if !strings.Contains(err.Error(), "--nope") {
		t.Errorf("the error should name the argument, got %v", err)
	}
}

func TestAttentionIsListedInTheWorkspaceUsage(t *testing.T) {
	usage := &bytes.Buffer{}
	app := NewApp(&fakeService{}, usage)
	if err := app.Run([]string{"w"}); err != nil {
		t.Fatalf("workspace usage: %v", err)
	}
	if !strings.Contains(usage.String(), "attention") {
		t.Errorf("the workspace usage does not list attention:\n%s", usage)
	}
}

package zmx

import (
	"context"
	"strings"
	"testing"
)

// recorder captures the argv of every zmx call, so a test can state the command
// rather than the effect.
type recorder struct{ calls [][]string }

func (r *recorder) run(_ context.Context, _, bin string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{bin}, args...))
	return "", nil
}

// TestIdentityPrefersTheLabels. The name cannot answer this for a session whose
// name had to be shortened to exist, which is the case the labels were added
// for — so when both are present the labels win, and the test uses a name whose
// parts disagree with them to prove which one was read.
func TestIdentityPrefersTheLabels(t *testing.T) {
	s := Session{
		Name: "awp.alpha.pr-2336-dev-mlw-4f2a.agent",
		Labels: map[string]string{
			LabelProject:   "alpha",
			LabelWorkspace: "pr-2336-dev-mlwzqyrmxslo",
			LabelKind:      "agent",
		},
	}
	project, workspace, kind, ok := s.Identity()
	if !ok {
		t.Fatal("a labelled session was not recognised as awp's")
	}
	if project != "alpha" || workspace != "pr-2336-dev-mlwzqyrmxslo" || kind != "agent" {
		t.Errorf("identity is %q/%q/%q, want the labels' full workspace name", project, workspace, kind)
	}
}

// TestIdentityFallsBackToTheName, which is every session that existed before the
// labels did — and, for a moment, every pane's session too: the deck runs the
// attach, so awp cannot label a session it has not yet seen appear.
func TestIdentityFallsBackToTheName(t *testing.T) {
	s := Session{Name: "awp.alpha.docs-tidy.agent"}
	project, workspace, kind, ok := s.Identity()
	if !ok {
		t.Fatal("an unlabelled awp session was not recognised")
	}
	if project != "alpha" || workspace != "docs-tidy" || kind != "agent" {
		t.Errorf("identity is %q/%q/%q, want it read out of the name", project, workspace, kind)
	}
}

// TestIdentityDeclinesSomeoneElsesSession. `zmx ls` lists every session on the
// machine; a shell someone started by hand is not a workspace, and claiming it
// would put a row in the deck for something awp knows nothing about.
func TestIdentityDeclinesSomeoneElsesSession(t *testing.T) {
	for _, name := range []string{"dev", "notes", "awp", "awp.alpha.agent", "awp.a.b.c.d"} {
		if _, _, _, ok := (Session{Name: name}).Identity(); ok {
			t.Errorf("claimed %q as an awp session", name)
		}
	}
}

// TestALabelledSessionNeedsBothParts: a workspace with no project is not an
// address, and half an identity would match the wrong row rather than no row.
// Falling back to the name is the honest answer for a partial set.
func TestALabelledSessionNeedsBothParts(t *testing.T) {
	s := Session{
		Name:   "awp.alpha.docs-tidy.agent",
		Labels: map[string]string{LabelWorkspace: "somewhere-else"},
	}
	_, workspace, _, ok := s.Identity()
	if !ok || workspace != "docs-tidy" {
		t.Errorf("identity is %q (ok=%v), want the name's answer for a half-labelled session", workspace, ok)
	}
}

// TestLabelsAreWrittenSorted so the command built from a map is the same every
// time — a shuffled argv makes a log line that reads as a different call.
func TestLabelsAreWrittenSorted(t *testing.T) {
	rec := &recorder{}
	if err := New(rec.run).SetLabels(context.Background(), "awp.p.w.agent", IdentityLabels("p", "w", "agent")); err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("made %d calls, want one: %v", len(rec.calls), rec.calls)
	}
	got := strings.Join(rec.calls[0], " ")
	want := "zmx set awp.p.w.agent awp_kind=agent awp_project=p awp_workspace=w"
	if got != want {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// TestLabellingNeedsAName mirrors Kill's guard: an empty name is a caller that
// lost track of which session it meant, and zmx would read the first label as
// the name and label something else.
func TestLabellingNeedsAName(t *testing.T) {
	rec := &recorder{}
	if err := New(rec.run).SetLabels(context.Background(), "  ", IdentityLabels("p", "w", "agent")); err == nil {
		t.Fatal("labelled a session with no name")
	}
	if len(rec.calls) != 0 {
		t.Errorf("still ran %v", rec.calls)
	}
}

// TestNothingToLabelRunsNothing. A caller with an empty set has said everything
// it had to say, and `zmx set <name>` with no pairs is a call that can only fail.
func TestNothingToLabelRunsNothing(t *testing.T) {
	rec := &recorder{}
	if err := New(rec.run).SetLabels(context.Background(), "awp.p.w.agent", nil); err != nil {
		t.Fatalf("SetLabels with nothing to set: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("ran %v for an empty label set", rec.calls)
	}
}

// TestLsLabelsBecomeTheIdentity is the end of the read path: zmx prints labels as
// extra key=value fields on the `ls` line, and parseSession keeps anything it
// does not recognise. This pins that the two halves meet — the keys awp writes
// are the keys it reads back.
func TestLsLabelsBecomeTheIdentity(t *testing.T) {
	line := "  name=awp.alpha.pr-2336-dev-mlw-4f2a.agent\tpid=42\tclients=0\tcreated=1786124270\t" +
		"cmd=claude\tawp_project=alpha\tawp_workspace=pr-2336-dev-mlwzqyrmxslo\tawp_kind=agent"
	s, ok := parseSession(line)
	if !ok {
		t.Fatal("the ls line did not parse")
	}
	project, workspace, kind, ok := s.Identity()
	if !ok || project != "alpha" || workspace != "pr-2336-dev-mlwzqyrmxslo" || kind != "agent" {
		t.Errorf("identity is %q/%q/%q (ok=%v), want the labels off the ls line", project, workspace, kind, ok)
	}
}

package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
)

func TestBuildMiniDeckRowsFiltersAndSorts(t *testing.T) {
	all := map[string]map[string]workspace.Entry{
		"/repos/zeta": {
			"feature-x": {Name: "feature-x", Path: "/ws/zeta/feature-x", Status: "waiting", Unread: true},
			"idle-old":  {Name: "idle-old", Path: "/ws/zeta/idle-old", Status: "idle"},
		},
		"/repos/alpha": {
			"running":  {Name: "running", Path: "/ws/alpha/running", Status: "working"},
			"notified": {Name: "notified", Path: "/ws/alpha/notified", Status: "idle", Unread: true},
			"unknown":  {Name: "unknown", Path: "/ws/alpha/unknown", Status: ""},
		},
	}
	snap := deckTmuxSnapshot{
		known: true,
		liveByName: map[string]string{
			DeckSessionName("alpha", "running"):   "$1",
			DeckSessionName("zeta", "feature-x"):  "$2",
			DeckSessionName("alpha", "notified"):  "$3",
		},
		agentShell: map[string]bool{},
	}
	rows := buildMiniDeckRows(all, snap, nil)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows after filter, got %d: %+v", len(rows), rows)
	}
	want := []struct {
		project, workspace string
	}{
		{"alpha", "notified"},
		{"alpha", "running"},
		{"zeta", "feature-x"},
	}
	for i, w := range want {
		if rows[i].Project != w.project || rows[i].Workspace != w.workspace {
			t.Fatalf("row %d: want %s/%s got %s/%s",
				i, w.project, w.workspace, rows[i].Project, rows[i].Workspace)
		}
	}
}

func TestBuildMiniDeckRowsDropsStaleActiveRows(t *testing.T) {
	all := map[string]map[string]workspace.Entry{
		"/repos/redwood": {
			// Status was written long ago; the agent process is gone.
			"react-compiler": {Name: "react-compiler", Path: "/ws/r/rc", Status: "working"},
			// Session still alive and agent pane is the real agent.
			"keep-me": {Name: "keep-me", Path: "/ws/r/keep", Status: "working"},
			// Session exists but the agent pane fell back to a shell.
			"agent-died": {Name: "agent-died", Path: "/ws/r/dead", Status: "working"},
			// idle+unread with no session: the Stop hook fired and the
			// user hasn't acknowledged the turn. Surface it — mini-deck
			// recreates the session on jump.
			"pinged-but-dead": {Name: "pinged-but-dead", Path: "/ws/r/pinged", Status: "idle", Unread: true},
			// Status exited — never surfaces in mini-deck, even with unread.
			"finished": {Name: "finished", Path: "/ws/r/finished", Status: "exited", Unread: true},
		},
	}
	keepSession := DeckSessionName("redwood", "keep-me")
	deadSession := DeckSessionName("redwood", "agent-died")
	snap := deckTmuxSnapshot{
		known: true,
		liveByName: map[string]string{
			keepSession: "$1",
			deadSession: "$2",
		},
		agentShell: map[string]bool{
			deadSession: true,
		},
	}
	rows := buildMiniDeckRows(all, snap, nil)
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Workspace] = true
	}
	if got["react-compiler"] {
		t.Error("expected stale react-compiler (no session) to be dropped")
	}
	if got["agent-died"] {
		t.Error("expected stale agent-died (agent pane back at shell) to be dropped")
	}
	if !got["pinged-but-dead"] {
		t.Error("expected idle+unread with no session to be kept (unread is the user's signal; session is recreated on jump)")
	}
	if got["finished"] {
		t.Error("expected exited row to be dropped even when unread")
	}
	if !got["keep-me"] {
		t.Error("expected keep-me (session live, agent running) to be kept")
	}
	if len(rows) != 2 {
		t.Fatalf("expected keep-me and pinged-but-dead to survive, got %d rows: %+v", len(rows), rows)
	}
}

// Regression: idle+unread with a live session whose agent pane has
// fallen back to a bare shell (user quit Claude after a turn finished)
// must still surface. The unread bit is the durable signal that there's
// an unacknowledged turn to read, independent of whether the agent
// process is still alive.
func TestBuildMiniDeckRowsKeepsIdleUnreadWithDeadAgentShell(t *testing.T) {
	all := map[string]map[string]workspace.Entry{
		"/repos/redwood": {
			"finished-turn": {Name: "finished-turn", Path: "/ws/r/ft", Status: "idle", Unread: true},
		},
	}
	session := DeckSessionName("redwood", "finished-turn")
	snap := deckTmuxSnapshot{
		known: true,
		liveByName: map[string]string{
			session: "$1",
		},
		agentShell: map[string]bool{
			session: true,
		},
	}
	rows := buildMiniDeckRows(all, snap, nil)
	if len(rows) != 1 || rows[0].Workspace != "finished-turn" {
		t.Fatalf("expected finished-turn to survive freshness check (idle+unread is durable), got %+v", rows)
	}
}

// The mini-deck filter mirrors the regular deck's attention scope, which
// has no name-based exclusion. A "default" row that satisfies
// MiniIncluded (e.g. an agent really is running there) must surface,
// otherwise the two scopes drift out of sync.
func TestBuildMiniDeckRowsKeepsDefaultWorkspaces(t *testing.T) {
	all := map[string]map[string]workspace.Entry{
		"/repos/redwood": {
			"default":   {Name: "default", Path: "/ws/r/default", Status: "working"},
			"feature-x": {Name: "feature-x", Path: "/ws/r/fx", Status: "working"},
		},
	}
	snap := deckTmuxSnapshot{
		known: true,
		liveByName: map[string]string{
			DeckSessionName("redwood", "default"):   "$1",
			DeckSessionName("redwood", "feature-x"): "$2",
		},
		agentShell: map[string]bool{},
	}
	rows := buildMiniDeckRows(all, snap, nil)
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Workspace] = true
	}
	if !got["default"] {
		t.Error("expected default workspace to surface when it matches the attention filter")
	}
	if !got["feature-x"] {
		t.Error("expected feature-x to surface")
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
}

func TestBuildMiniDeckRowsKeepsAllWhenTmuxUnknown(t *testing.T) {
	all := map[string]map[string]workspace.Entry{
		"/repos/redwood": {
			"working-row": {Name: "working-row", Path: "/ws/r/w", Status: "working"},
		},
	}
	// snap.known == false (fast path / no tmux) → trust stored status.
	snap := deckTmuxSnapshot{liveByName: map[string]string{}, agentShell: map[string]bool{}}
	rows := buildMiniDeckRows(all, snap, nil)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (snapshot unknown trusts state), got %d", len(rows))
	}
}

func TestMiniIncludedRules(t *testing.T) {
	cases := []struct {
		name   string
		status string
		unread bool
		want   bool
	}{
		{"working unread irrelevant", "working", false, true},
		{"in_progress alias", "in_progress", false, true},
		{"waiting requires unread (else it's a seen prompt)", "WAITING", false, false},
		{"waiting with unread is a fresh ping", "waiting", true, true},
		{"idle without unread is quiet", "idle", false, false},
		{"idle with unread is a finished turn to read", "idle", true, true},
		{"empty status without unread", "", false, false},
		{"empty status with unread", "", true, true},
		{"exited never surfaces even when unread", "exited", true, false},
		{"exited never surfaces when not unread", "exited", false, false},
		{"error treated like exited", "error", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deckui.MiniIncluded(c.status, c.unread); got != c.want {
				t.Errorf("MiniIncluded(%q, %v) = %v, want %v", c.status, c.unread, got, c.want)
			}
		})
	}
}

func TestRunMiniDeckRejectsArgs(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"mini-deck", "extra"}); err == nil ||
		!strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("expected mini-deck arg error, got %v", err)
	}
}

func TestRunMiniDeckCallsWorkflow(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	called := false
	app.miniDeck = func(runner Runner, in io.Reader, out io.Writer) error {
		called = true
		return nil
	}
	if err := app.Run([]string{"mini-deck"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !called {
		t.Fatal("expected mini-deck workflow to be called")
	}
}

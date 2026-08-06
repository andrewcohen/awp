package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// deckIndent is a literal string standing in for a computed width, so the two
// can drift. They only stay equal because of this.
func TestDeckIndentMatchesTextCol(t *testing.T) {
	if len(deckIndent) != deckTextCol {
		t.Errorf("deckIndent is %d spaces, deckTextCol is %d — lines that lead with text would sit off the body column",
			len(deckIndent), deckTextCol)
	}
}

// colOfFirst returns the column the given text starts in, on the first
// rendered line that contains it, with ANSI stripped.
//
// The column, not the byte offset: the status dot is three bytes and one cell,
// so measuring alignment with strings.Index alone reports text as two columns
// further right than the terminal draws it.
func colOfFirst(t *testing.T, out, want string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		plain := ansi.Strip(line)
		if i := strings.Index(plain, want); i >= 0 {
			return lipgloss.Width(plain[:i])
		}
	}
	return -1
}

// badgeOf is the left half of a top row: everything before the scope label.
func badgeOf(row string) string {
	return strings.TrimSpace(strings.SplitN(row, "scope:", 2)[0])
}

// topRow returns the deck's first content row: the badge, then the scope label.
func topRow(t *testing.T, m Model) string {
	t.Helper()
	(&m).clampDeckViewport()
	lines := strings.Split(m.renderList(m.width), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least two rendered lines, got %d", len(lines))
	}
	return ansi.Strip(lines[1])
}

func TestTopRowPutsTheBadgeLeftAndTheScopeRight(t *testing.T) {
	items := []Item{
		{ProjectName: "frontend", WorkspaceName: "dashboard", Status: "waiting", Unread: true},
		{ProjectName: "frontend", WorkspaceName: "feat", Status: "idle"},
	}
	m := New(items, nil)
	m.width, m.height = 100, 40
	(&m).clampDeckViewport()
	out := m.renderList(m.width)

	line := strings.Split(out, "\n")[1]
	if got := lipgloss.Width(line); got != m.width {
		t.Errorf("top row is %d cols, want %d (the scope label is off the right edge)", got, m.width)
	}
	plain := ansi.Strip(line)
	if !strings.HasSuffix(strings.TrimRight(plain, " "), scopeLabel(m.scope)) {
		t.Errorf("top row does not end with the scope label: %q", plain)
	}
	// The badge leads with a status dot, and it is the dot that has to land on
	// the body's text column — the same column the rows' own dots sit in, and
	// the one the project headers start on.
	dotCol := colOfFirst(t, line, "●")
	headerCol := colOfFirst(t, out, "frontend")
	if dotCol < 0 || headerCol < 0 {
		t.Fatalf("expected both a badge dot and a 'frontend' header; dot=%d header=%d", dotCol, headerCol)
	}
	if dotCol != headerCol {
		t.Errorf("badge starts at col %d, project header name at col %d — must align", dotCol, headerCol)
	}
}

// The badge's job: say what wants you, in numbers, without words.
func TestTopRowBadgeIsDotsAndNumbers(t *testing.T) {
	items := []Item{
		{ProjectName: "web", WorkspaceName: "one", Status: "waiting", Unread: true},
		{ProjectName: "web", WorkspaceName: "two", Status: "waiting", Unread: true},
		{ProjectName: "web", WorkspaceName: "three", Status: "working"},
		{ProjectName: "api", WorkspaceName: "four", Status: "idle", Unread: true},
		{ProjectName: "api", WorkspaceName: "five", Status: "idle"},
	}
	m := New(items, nil)
	m.width, m.height = 120, 40

	row := topRow(t, m)
	if badge := badgeOf(row); badge != "● 2  ● 1  ● 1" {
		t.Errorf("badge = %q, want %q", badge, "● 2  ● 1  ● 1")
	}
	// The dot is the word. Spelling it out again turned three numbers into a
	// sentence across the top of the screen.
	for _, word := range []string{"waiting", "working", "unread", "awp deck"} {
		if strings.Contains(row, word) {
			t.Errorf("top row spells out %q: %q", word, row)
		}
	}
}

// The colours are the only thing distinguishing the three counts, so they have
// to be the row-status colours rather than a palette invented here.
func TestBadgeDotsWearTheRowStatusColours(t *testing.T) {
	m := New([]Item{
		{ProjectName: "web", WorkspaceName: "one", Status: "waiting", Unread: true},
		{ProjectName: "web", WorkspaceName: "two", Status: "working"},
		{ProjectName: "web", WorkspaceName: "three", Status: "idle", Unread: true},
	}, nil)
	m.width, m.height = 120, 40
	(&m).clampDeckViewport()

	badge := strings.SplitN(strings.Split(m.renderList(m.width), "\n")[1], "scope:", 2)[0]
	for _, want := range []struct {
		status string
		label  string
	}{
		{"waiting", "waiting"},
		{"working", "working"},
		{"idle", "unread"},
	} {
		dot := statusGlyph(want.status, false, true)
		if !strings.Contains(badge, dot) {
			t.Errorf("%s count is not wearing the %s row's dot (%q): %q", want.label, want.status, dot, badge)
		}
	}
}

// Switching scope filters the rows. It must not change what the badge says is
// waiting, or the number is reporting the filter rather than the work — and the
// filter is already named on the same row, a few cols to the right.
func TestBadgeCountsEveryWorkspaceWhateverTheScope(t *testing.T) {
	items := []Item{
		{ProjectName: "web", WorkspaceName: "one", Status: "waiting", Unread: true},
		{ProjectName: "web", WorkspaceName: "two", Status: "waiting", Unread: true},
		// Neither of these shows in the attention scope, and neither has a PR
		// so neither shows in the inbox.
		{ProjectName: "api", WorkspaceName: "three", Status: "idle"},
		{ProjectName: "api", WorkspaceName: "four", Status: "idle"},
	}
	want := ""
	for _, scope := range []Scope{ScopeAll, ScopeAttention, ScopeInbox} {
		m := New(items, nil)
		m.width, m.height = 120, 40
		m.scope = scope

		got := badgeOf(topRow(t, m))
		if want == "" {
			want = got
		}
		if got != want {
			t.Errorf("scope %v badges %q, but %q in the first scope — the count must not follow the filter",
				scope, got, want)
		}
		if got != "● 2" {
			t.Errorf("scope %v: expected 2 waiting and nothing else, got %q", scope, got)
		}
	}
}

func TestTopRowIsJustTheScopeWhenNothingWants(t *testing.T) {
	m := New([]Item{
		{ProjectName: "web", WorkspaceName: "one", Status: "idle"},
		{ProjectName: "web", WorkspaceName: "two", Status: "exited", Unread: true},
	}, nil)
	m.width, m.height = 120, 40

	// No badge, and no sentence saying there is nothing — there is no neutral
	// way to say "no", and the scope label already proves the frame rendered.
	row := topRow(t, m)
	if badge := badgeOf(row); badge != "" {
		t.Errorf("expected the badge gone entirely, got %q", badge)
	}
	if !strings.Contains(row, "scope:") {
		t.Errorf("expected the scope label to stay, got %q", row)
	}
}

func TestNoWorkspacesMessageSitsOnTheBodyColumn(t *testing.T) {
	m := New(nil, nil)
	m.width, m.height = 100, 40
	(&m).clampDeckViewport()
	out := m.renderList(m.width)

	if got := colOfFirst(t, out, "No workspaces found."); got != deckTextCol+1 {
		// +1 for the panel's own left padding.
		t.Errorf("empty-state message at col %d, want %d (the deck's text column)", got, deckTextCol+1)
	}
}

func TestCountAttentionSkipsWorkspacesBeingCreated(t *testing.T) {
	// An optimistic row is a create in flight — its own spinner, nothing to act
	// on. Counting it would badge the deck for work the user just started.
	got := countAttention([]Item{
		{WorkspaceName: "real", Status: "waiting", Unread: true},
		{WorkspaceName: "pending", Status: "waiting", Unread: true, Optimistic: true},
	})
	if got.Waiting != 1 {
		t.Errorf("Waiting = %d, want 1 (the optimistic row must not count)", got.Waiting)
	}
}

// The counts add up to the workspaces, which is what makes three bare numbers
// readable as a breakdown rather than as three unrelated figures.
func TestCountAttentionBucketsAreExclusive(t *testing.T) {
	items := []Item{
		{Status: "working", Unread: true}, // stale flag from an earlier turn
		{Status: "waiting", Unread: true},
		{Status: "idle", Unread: true},
		{Status: "idle"},
		{Status: "exited", Unread: true},
	}
	c := countAttention(items)
	if total := c.Waiting + c.Working + c.Notified; total != 3 {
		t.Errorf("counts total %d (waiting=%d working=%d unread=%d), want 3 — the read idle and the exited row count for nothing, and the working row counts once",
			total, c.Waiting, c.Working, c.Notified)
	}
}

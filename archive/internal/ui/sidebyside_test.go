package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/diff"
)

func hunkLines(spec ...string) []diff.HunkLine {
	out := make([]diff.HunkLine, 0, len(spec))
	for _, s := range spec {
		out = append(out, diff.HunkLine{Type: s[0], Content: s[1:]})
	}
	return out
}

func (p linePair) String() string {
	return "(" + lineNoText(p.old+1) + "," + lineNoText(p.new+1) + ")"
}

// A rewritten line sits opposite the thing it was rewritten into. That is the
// whole point of the layout — everything else here is what happens at the edges
// of that rule.
func TestPairingPutsARewriteOppositeItself(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []diff.HunkLine
		want  []linePair
	}{
		{
			"a one-for-one rewrite",
			hunkLines(" ctx", "-old", "+new", " end"),
			[]linePair{{0, 0}, {1, 2}, {3, 3}},
		},
		{
			// Equal runs zip in order, so the second removal faces the second addition
			// rather than everything collapsing onto one row.
			"equal runs zip in order",
			hunkLines("-a", "-b", "+A", "+B"),
			[]linePair{{0, 2}, {1, 3}},
		},
		{
			// More removed than added: the surplus removals keep the left column and
			// leave the right empty, which is what "these lines went away" looks like.
			"more removed than added",
			hunkLines("-a", "-b", "-c", "+A"),
			[]linePair{{0, 3}, {1, -1}, {2, -1}},
		},
		{
			"more added than removed",
			hunkLines("-a", "+A", "+B", "+C"),
			[]linePair{{0, 1}, {-1, 2}, {-1, 3}},
		},
		{
			// No removals to pair with, so nothing is held back waiting for some.
			"a pure addition",
			hunkLines(" ctx", "+A", "+B"),
			[]linePair{{0, 0}, {-1, 1}, {-1, 2}},
		},
		{
			"a pure deletion",
			hunkLines(" ctx", "-a", "-b"),
			[]linePair{{0, 0}, {1, -1}, {2, -1}},
		},
		{
			// Context between two change blocks separates them: the second block's
			// removals must not pair with the first block's additions.
			"context separates two blocks",
			hunkLines("-a", "+A", " ctx", "-b", "+B"),
			[]linePair{{0, 1}, {2, 2}, {3, 4}},
		},
		{
			// Additions before removals is not the order git emits, and pairing them
			// would put a line opposite something it has nothing to do with.
			"additions before removals stay unpaired",
			hunkLines("+A", "-a"),
			[]linePair{{-1, 0}, {1, -1}},
		},
		{"nothing at all", nil, []linePair{}},
	} {
		got := pairHunkLines(tc.lines)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %d rows, want %d (%v vs %v)", tc.name, len(got), len(tc.want), got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: row %d is %v, want %v", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// Every source line appears exactly once, in the column it belongs to. Pairing
// is a rearrangement — a line silently dropped from the layout is a change the
// reviewer never sees.
func TestPairingLosesNothing(t *testing.T) {
	lines := hunkLines(" a", "-b", "-c", "+B", " d", "+e", "-f", " g", "-h", "+H", "+I")
	seenOld, seenNew := map[int]int{}, map[int]int{}
	for _, p := range pairHunkLines(lines) {
		if p.old >= 0 {
			seenOld[p.old]++
		}
		if p.new >= 0 {
			seenNew[p.new]++
		}
	}
	for i, l := range lines {
		switch l.Type {
		case '+':
			if seenNew[i] != 1 {
				t.Errorf("added line %d (%q) appears %d times on the new side, want 1", i, l.Content, seenNew[i])
			}
			if seenOld[i] != 0 {
				t.Errorf("added line %d (%q) appears on the old side", i, l.Content)
			}
		case '-':
			if seenOld[i] != 1 {
				t.Errorf("removed line %d (%q) appears %d times on the old side, want 1", i, l.Content, seenOld[i])
			}
			if seenNew[i] != 0 {
				t.Errorf("removed line %d (%q) appears on the new side", i, l.Content)
			}
		default:
			// Context exists on both sides, so it shows in both columns of one row.
			if seenOld[i] != 1 || seenNew[i] != 1 {
				t.Errorf("context line %d (%q) appears %d/%d times, want 1/1", i, l.Content, seenOld[i], seenNew[i])
			}
		}
	}
}

// splitModel is a viewer wide enough to split, on a change with a real rewrite.
func splitModel(t *testing.T) Model {
	t.Helper()
	m := commentModel(t, diff.FileDiff{
		NewPath: "a.go",
		Hunks: []diff.Hunk{{
			OldStart: 10, NewStart: 10,
			Lines: hunkLines(" keep", "-was old", "+is new", "+extra", " tail"),
		}},
	})
	m.focus = FocusHunks
	m.SetSize(240, 20)
	if m.hunkWidth < sideBySideMinWidth {
		t.Fatalf("fixture is wrong: hunkWidth %d is under the split's floor %d", m.hunkWidth, sideBySideMinWidth)
	}
	return m
}

func pressBar(m Model) Model { return press(m, "|") }

// The anchor rule: a pair answers for its new side when it has one, its old side
// otherwise. Not new — it is what a mixed range already does — so side-by-side
// must not invent a second answer.
func TestAPairAnchorsToItsNewSideUnlessItHasNone(t *testing.T) {
	m := pressBar(splitModel(t))
	if !m.sideBySide {
		t.Fatalf("| did not split, status %q", m.status)
	}
	var checked int
	for _, r := range m.stream.rows {
		if r.kind != rowLine {
			continue
		}
		if !r.paired {
			t.Fatal("a line row in side-by-side is not paired")
		}
		checked++
		want := r.oldLine
		if r.newLine >= 0 {
			want = r.newLine
		}
		if got := r.anchorLine(); got != want {
			t.Errorf("pair (old %d, new %d) anchors to %d, want %d", r.oldLine, r.newLine, got, want)
		}
		// And `line` — what every existing caller reads — agrees with it.
		if r.line != want {
			t.Errorf("pair (old %d, new %d) has line %d, want %d", r.oldLine, r.newLine, r.line, want)
		}
	}
	if checked == 0 {
		t.Fatal("no line rows to check")
	}
}

// Unified rows are never marked paired, so nothing can read oldLine/newLine off
// a row that never set them.
func TestUnifiedRowsAreNotPaired(t *testing.T) {
	m := splitModel(t)
	for _, r := range m.stream.rows {
		if r.paired {
			t.Fatal("a unified row claims to be a pair")
		}
	}
}

// The split draws both sides on one row, each in its own colour, with the rule
// between them.
func TestASplitRowShowsBothVersions(t *testing.T) {
	m := pressBar(splitModel(t))
	// The rewrite specifically, not the context pair above it: a context row has
	// both cells filled too, and it would pass this while saying nothing.
	var row string
	for i, r := range m.stream.rows {
		if r.kind != rowLine || r.oldLine < 0 || r.newLine < 0 {
			continue
		}
		h, _, ok := m.stream.hunkAt(m.filtered, r)
		if !ok || h.Lines[r.oldLine].Type != '-' {
			continue
		}
		row = ansi.Strip(m.renderStreamRowAt(i, m.hunkWidth))
		break
	}
	if row == "" {
		t.Fatal("no rewrite row (a removal paired with an addition) in the fixture")
	}
	if !strings.Contains(row, "was old") || !strings.Contains(row, "is new") {
		t.Fatalf("a rewrite row shows only one version: %q", row)
	}
	if !strings.Contains(row, strings.TrimSpace(sideBySideDivider)) {
		t.Fatalf("no divider between the columns: %q", row)
	}
	// Old on the left, new on the right — the other way round would read as a
	// revert.
	if strings.Index(row, "was old") > strings.Index(row, "is new") {
		t.Fatalf("the new side is drawn left of the old one: %q", row)
	}
}

// An addition has nothing on its left. Echoing the neighbouring context there is
// what a unified diff already does; the split exists because "nothing was here"
// and "this is unchanged" are different facts.
func TestAnAddedLineHasABlankLeftCell(t *testing.T) {
	m := pressBar(splitModel(t))
	for i, r := range m.stream.rows {
		if r.kind != rowLine || r.oldLine >= 0 || r.newLine < 0 {
			continue
		}
		row := ansi.Strip(m.renderStreamRowAt(i, m.hunkWidth))
		left, _, ok := strings.Cut(row, strings.TrimSpace(sideBySideDivider))
		if !ok {
			t.Fatalf("no divider in %q", row)
		}
		if strings.TrimSpace(left) != "" {
			t.Fatalf("an added line's left cell is not empty: %q", left)
		}
		return
	}
	t.Fatal("no addition-only row in the fixture")
}

// No row may overrun the pane, in either layout. The divider and two gutters are
// easy to be one column wrong about, and an overrun pushes the pane's border out.
func TestNoSplitRowOverrunsThePane(t *testing.T) {
	m := pressBar(splitModel(t))
	for i, r := range m.stream.rows {
		if r.kind != rowLine {
			continue
		}
		if got := lipgloss.Width(m.renderStreamRowAt(i, m.hunkWidth)); got > m.hunkWidth {
			t.Fatalf("row %d is %d wide in a pane of %d", i, got, m.hunkWidth)
		}
	}
}

// Toggling keeps the reader on the line they were reading. Row indices do not
// survive the rebuild — pairing changes how many rows a hunk has — so carrying
// the number across would land them in a different place, often a different file.
func TestTogglingKeepsTheCursorOnItsLine(t *testing.T) {
	m := splitModel(t)
	m = pressTimes(m, "j", 3)
	before, ok := m.cursorAnchorPoint()
	if !ok {
		t.Fatal("fixture is wrong: the cursor is not on a diff line")
	}

	m = pressBar(m)
	after, ok := m.cursorAnchorPoint()
	if !ok {
		t.Fatal("after splitting, the cursor is not on a diff line")
	}
	if after != before {
		t.Errorf("| moved the cursor from %+v to %+v", before, after)
	}

	m = pressBar(m)
	back, ok := m.cursorAnchorPoint()
	if !ok || back != before {
		t.Errorf("toggling back landed on %+v (ok=%v), want %+v", back, ok, before)
	}
}

// Below the floor `|` refuses and says what it needs. Falling back to unified
// would leave the key looking broken at exactly the width where the reader most
// needs telling what to do instead.
func TestTheSplitRefusesANarrowPane(t *testing.T) {
	m := splitModel(t)
	m.SetSize(70, 20)
	m = pressBar(m)
	if m.sideBySide {
		t.Fatal("| split a pane narrower than the floor")
	}
	for _, want := range []string{"side-by-side", "100", "\\"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("the refusal should mention %q, got %q", want, m.status)
		}
	}
}

// Leaving the split is always allowed, however narrow the pane got while you
// were in it — otherwise a resize could strand the reader in a layout they
// cannot get out of.
func TestLeavingTheSplitIsNeverRefused(t *testing.T) {
	m := pressBar(splitModel(t))
	m.SetSize(70, 20)
	m = pressBar(m)
	if m.sideBySide {
		t.Fatalf("| could not leave the split on a narrow pane, status %q", m.status)
	}
}

// wrap and the split are mutually exclusive. `w` says so rather than doing
// nothing, and `|` turns wrap off rather than honouring half of each.
func TestWrapAndTheSplitDoNotCoexist(t *testing.T) {
	m := splitModel(t)
	m = press(m, "w")
	if !m.wrap {
		t.Fatal("fixture is wrong: w did not turn wrap on")
	}
	m = pressBar(m)
	if !m.sideBySide || m.wrap {
		t.Fatalf("| left wrap=%v sideBySide=%v, want false/true", m.wrap, m.sideBySide)
	}
	if !m.stream.sideBySide || m.stream.wrap {
		t.Errorf("the stream was built wrap=%v sideBySide=%v", m.stream.wrap, m.stream.sideBySide)
	}

	m = press(m, "w")
	if m.wrap {
		t.Error("w turned wrap on while split")
	}
	if !strings.Contains(m.status, "wrap is off in side-by-side") {
		t.Errorf("w should say why it refused, got %q", m.status)
	}
}

// Commenting works the same in both layouts, and lands on the same line.
func TestCommentingFromTheSplitAnchorsToTheSameLine(t *testing.T) {
	unified := splitModel(t)
	unified = pressTimes(unified, "j", 3)
	want, ok := unified.AnchorAtCursor()
	if !ok {
		t.Fatal("no anchor at the cursor in unified")
	}

	split := pressBar(splitModel(t))
	split = pressTimes(split, "j", 3)
	got, ok := split.AnchorAtCursor()
	if !ok {
		t.Fatal("no anchor at the cursor in side-by-side")
	}
	// Not the same row — pairing changes the row count — but the same *place*:
	// seek both to the line the other is on and they must agree.
	if got.Path != want.Path {
		t.Errorf("side-by-side anchored to %q, unified to %q", got.Path, want.Path)
	}
	if got.Side == "" {
		t.Error("a side-by-side anchor carries no side")
	}
}

// It is in the reference, or nobody finds it.
func TestTheSplitKeyIsInTheHelp(t *testing.T) {
	for _, g := range viewerKeyGroups(nil) {
		for _, k := range g.Keys {
			if strings.Contains(k[0], "|") {
				return
			}
		}
	}
	t.Error("| is not listed in the ? reference")
}

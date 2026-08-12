package deckui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// modalSources is every modal_*.go in the package, tests excluded.
func modalSources(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("modal_*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		t.Fatal("no modal sources found; this guard is checking nothing")
	}
	return out
}

// funcBodies is the source text of every function or method named name in src.
func funcBodies(t *testing.T, src, name string) []string {
	t.Helper()
	text, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, text, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		out = append(out, string(text[fn.Body.Pos()-1:fn.Body.End()-1]))
	}
	return out
}

// The box seam.
//
// A child that reads m.width / m.height has assumed it owns the terminal, and
// only one child can be right about that. These tests are what stop the
// assumption growing back: the split's whole mechanism is handing a child a box
// smaller than the screen, and a renderer that ignores it does not fail — it
// just draws over its neighbour.

// TestNoModalReadsTheDecksOwnSize is the guard the invariant needs, because the
// invariant is only as strong as every renderer remembering it. A new modal
// that reaches for m.width composes correctly, passes its own tests, and is
// wrong the first time it is asked to share the screen — the same shape as the
// repo-directory guard in internal/github/dir_test.go.
func TestNoModalReadsTheDecksOwnSize(t *testing.T) {
	// The renderers, and only the renderers. The Model's own methods read its
	// dimensions legitimately — it is the thing that has them.
	for _, fn := range []string{"view", "renderPopover"} {
		for _, src := range modalSources(t) {
			for _, body := range funcBodies(t, src, fn) {
				for _, bad := range []string{"m.width", "m.height"} {
					if strings.Contains(body, bad) {
						t.Errorf("%s: %s reads %s instead of the box it was given", src, fn, bad)
					}
				}
			}
		}
	}
}

// TestEveryModalTakesABox walks the modal interfaces by reflection, so a
// renderer cannot opt out of the seam by keeping the old signature — it would
// stop satisfying the interface, which is the point of putting the box in the
// method set rather than in a field somebody has to remember to read.
func TestEveryModalTakesABox(t *testing.T) {
	boxType := reflect.TypeOf(box{})
	for name, iface := range map[string]reflect.Type{
		"bodyModal":    reflect.TypeOf((*bodyModal)(nil)).Elem(),
		"popoverModal": reflect.TypeOf((*popoverModal)(nil)).Elem(),
	} {
		found := false
		for i := range iface.NumMethod() {
			mt := iface.Method(i).Type
			for j := range mt.NumIn() {
				if mt.In(j) == boxType {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s renders without being told its box", name)
		}
	}
}

// TestAPickerFillsTheBoxItWasGiven — the mechanism itself, at a width that is
// not the terminal's. A picker sized from m.width would come out 120 wide here.
func TestAPickerFillsTheBoxItWasGiven(t *testing.T) {
	var opened string
	next, _ := openPickerModel(t, &opened).Update(keyO)
	m := next.(Model)
	m.width, m.height = 120, 40
	p, ok := m.active.(*openPicker)
	if !ok {
		t.Fatalf("o did not open the project picker (active=%T)", m.active)
	}

	// Narrow enough that the picker is single-column, so the left pane is the
	// whole box and its width is the box's.
	left, right := p.view(&m, box{w: 48, h: 20})
	if right != "" {
		t.Fatalf("expected a single column at 48 cols, got a right pane")
	}
	if got := lipgloss.Width(left); got != 48 {
		t.Errorf("the picker rendered %d columns into a 48-column box", got)
	}
	if got := lipgloss.Height(left); got > 20 {
		t.Errorf("the picker rendered %d rows into a 20-row box", got)
	}
}

// TestAFixedWidthPopoverShrinksToItsBox. The confirms each named a number of
// columns, which was safe only while the number could not be contradicted. Given
// a narrower box the number becomes a maximum.
func TestAFixedWidthPopoverShrinksToItsBox(t *testing.T) {
	m := New(waitingRows(), func(ActionRequest) error { return nil })
	m.width, m.height = 120, 40
	c, _, _ := newConfirmDelete(Item{ProjectName: "proj", WorkspaceName: "ws"})

	if got := lipgloss.Width(c.renderPopover(&m, box{w: 120, h: 40})); got != 60+borderCells {
		t.Errorf("in a wide box the confirm rendered %d columns, want its own %d", got, 60+borderCells)
	}
	if got := lipgloss.Width(c.renderPopover(&m, box{w: 40, h: 40})); got > 40 {
		t.Errorf("in a 40-column box the confirm rendered %d columns", got)
	}
}

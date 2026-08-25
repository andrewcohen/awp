package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
)

// TestTheCIPaneRunsTheSameScriptAsTheWindow: `i` has to watch the same run
// wherever it runs. The run is resolved by the script itself rather than in Go,
// so two copies would be two answers to "which run" the moment either is
// edited.
func TestTheCIPaneRunsTheSameScriptAsTheWindow(t *testing.T) {
	argv := panes[deckui.PaneKindCI].argv(deckui.Item{})
	want := []string{"bash", "-c", ciWatchScript}
	if !slices.Equal(argv, want) {
		t.Fatalf("ci pane argv = %v, want %v", argv, want)
	}
}

// TestTheCIScriptSurvivesBeingTypedAtAShell: the tmux path wraps the script in
// single quotes to send it as one command. A single quote inside would end that
// quoting early and the rest of the script would be read as shell — so this is
// the condition that wrapping is safe, asserted where the wrapping is not.
func TestTheCIScriptSurvivesBeingTypedAtAShell(t *testing.T) {
	if strings.Contains(ciWatchScript, "'") {
		t.Fatalf("ciWatchScript contains a single quote, which breaks the tmux path's quoting: %s", ciWatchScript)
	}
}

// TestTheCICommandIsWrittenDownOnce is the anti-drift half: a second string
// literal anywhere in the package that watches a run means someone wrote a
// second answer instead of reusing this one.
//
// String literals rather than raw text, so prose about `gh run watch` in a doc
// comment does not count as a copy of it.
func TestTheCICommandIsWrittenDownOnce(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob the package: %v", err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "gh run watch") {
				found = append(found, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	if len(found) != 1 {
		t.Fatalf("`gh run watch` appears in %d string literals (%v); it belongs only in ciWatchScript", len(found), found)
	}
}

// TestTheWatchPaneRunsThisAwp: a pane spawns the process itself rather than
// typing a line at a shell, so it can name the binary exactly. The running one
// is the right answer — a deck built to a temp path should open that build's
// watch view, not an older install's.
func TestTheWatchPaneRunsThisAwp(t *testing.T) {
	argv := panes[deckui.PaneKindWatch].argv(deckui.Item{})
	if len(argv) != 2 || argv[1] != "watch" {
		t.Fatalf("watch pane argv = %v, want [<awp> watch]", argv)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot resolve this test binary: %v", err)
	}
	if argv[0] != exe {
		t.Errorf("watch pane runs %q, want the running executable %q", argv[0], exe)
	}
}

// TestBothNewPanesAreEphemeral: each runs one foreground program you watch and
// then leave. A long-lived one would hold a finished `gh run watch` in a zmx
// session and show it again next time, which is worse than nothing.
func TestBothNewPanesAreEphemeral(t *testing.T) {
	for _, kind := range []string{deckui.PaneKindCI, deckui.PaneKindWatch} {
		if got := panes[kind].lifetime; got != ephemeral {
			t.Errorf("pane %q has lifetime %v, want ephemeral", kind, got)
		}
	}
}

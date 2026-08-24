package editor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheEditorRunsWhereItWasTold. The whole point of the dir parameter: an
// editor started in the wrong directory still opens the file, so nothing about the
// mistake is visible until :Explore, the fuzzy finder or the LSP answers about
// another project.
func TestTheEditorRunsWhereItWasTold(t *testing.T) {
	cmd := OpenExecCmd("/repo/qa", "nvim", "/repo/qa/a.go", 12)
	if cmd.Dir != "/repo/qa" {
		t.Errorf("cmd.Dir = %q, want /repo/qa", cmd.Dir)
	}
}

// TestNobodyOpensAnEditorFromNowhereByAccident.
//
// dir is required rather than optional because that is the only thing that makes
// the invariant hold at every call site — the same reasoning as
// internal/github/dir_test.go, and the same failure: it *was* optional, every
// caller left it off, and the editor inherited whatever directory awp had been
// started in.
//
// A required parameter can still be passed "", so the guard is that an empty one
// has to be a deliberate literal with a reason written beside it. One caller
// qualifies — the compose box edits a temp scratch file that belongs to no working
// copy — so the count is pinned rather than the practice banned. A new "" bumps it
// and lands here.
func TestNobodyOpensAnEditorFromNowhereByAccident(t *testing.T) {
	const repo = "../.."
	// The compose box's temp file. Named, so a second one is a test failure rather
	// than a number to update.
	allowed := map[string]bool{"internal/ui/comment_editor.go": true}

	var offenders []string
	fset := token.NewFileSet()
	err := filepath.Walk(repo, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file this package cannot parse is not evidence of anything; the build
			// is what fails on it.
			return nil //nolint:nilerr // parse failures are the compiler's business
		}
		rel, _ := filepath.Rel(repo, path)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "OpenExecCmd" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || lit.Value != `""` {
				return true
			}
			if allowed[filepath.ToSlash(rel)] {
				return true
			}
			offenders = append(offenders, filepath.ToSlash(rel))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repo, err)
	}
	if len(offenders) > 0 {
		t.Errorf("these open $EDITOR with no directory, so it inherits awp's own cwd: %v\n"+
			"pass the working copy the file lives in, or add a reason and allow it here", offenders)
	}
}

// The command carries no descriptors of its own.
//
// tea.ExecProcess fills in the terminal's for a command it hands the screen to,
// and creack/pty fills in the pty for one hosted in a pane — but each only where
// the field is nil. Naming os.Stdout here pinned the editor to the deck's own
// screen, so hosting it in a pane painted over the deck and left the pane blank,
// which reads as the editor never opening.
func TestOpenExecCmdLeavesItsStdioToWhoeverRunsIt(t *testing.T) {
	cmd := OpenExecCmd("/repo", "nvim", "/repo/a.go", 12)
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Errorf("stdio is pre-wired: in=%v out=%v err=%v", cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
}

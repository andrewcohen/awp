package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
	"github.com/andrewcohen/awp/internal/zmx"
)

// TestThePaneHostedReviewReturnsAfterEverythingItNeedsToDoFirst.
//
// A review workspace exists only to hold a reviewing agent, so the pane-hosted
// path stops before the tmux half and parks the brief for the pane to deliver.
// Everything the review actually consists of has to already have happened by
// then:
//
//   - PrepareWorkspace       — without it there is no workspace to review in
//   - RecordPROverride       — without it the workspace is not pinned to the PR,
//     so `awp review publish` later guesses
//   - writeReviewPromptFile  — the brief itself, on disk
//   - buildReviewPointerPrompt — the parked prompt points at that file, so it is
//     meaningless if written before the file exists
//
// Every one of those is a silent failure if it moves below the return: the flow
// reports success and the reviewer comes up with no workspace, no pin, or a
// prompt aimed at a file nobody wrote. Nothing about that errors, which is why
// this is pinned structurally rather than left to review — the same reason
// internal/github/dir_test.go walks its methods by reflection.
func TestThePaneHostedReviewReturnsAfterEverythingItNeedsToDoFirst(t *testing.T) {
	const file = "review.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	fn := findFunc(f, "runReviewOpts")
	if fn == nil {
		t.Fatal("runReviewOpts is gone; this guard is measuring nothing")
	}

	// Line of the `if opts.paneHosted {` that returns early.
	gate := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || gate != 0 {
			return true
		}
		sel, ok := ifs.Cond.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "paneHosted" {
			return true
		}
		if !endsInReturn(ifs.Body) {
			return true
		}
		gate = fset.Position(ifs.Pos()).Line
		return false
	})
	if gate == 0 {
		t.Fatal("found no `if opts.paneHosted { … return }` in runReviewOpts")
	}

	// Line of each essential call.
	at := map[string]int{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call.Fun)
		if name == "" {
			return true
		}
		if _, seen := at[name]; !seen {
			at[name] = fset.Position(call.Pos()).Line
		}
		return true
	})

	for _, step := range []string{
		"PrepareWorkspace",
		"RecordPROverride",
		"writeReviewPromptFile",
		"buildReviewPointerPrompt",
	} {
		line, ok := at[step]
		if !ok {
			t.Errorf("runReviewOpts no longer calls %s — a pane-hosted review would ship without it", step)
			continue
		}
		if line > gate {
			t.Errorf("%s is at %s:%d, below the pane-hosted return at line %d — a pane-hosted review would skip it, silently",
				step, file, line, gate)
		}
	}
}

// TestAParkedReviewBriefStartsTheReviewerNotACodingAgent.
//
// The two flavors differ by the dev-loop preamble: a coding agent is told to
// work in units, run gates and commit, and a reviewer must not be — it is
// reading someone else's change, and those instructions would have it start
// editing. Creating the session is the one moment the flavor is still ours to
// choose, because zmx ignores argv for a session that already exists.
func TestAParkedReviewBriefStartsTheReviewerNotACodingAgent(t *testing.T) {
	requireZmx(t)
	coding := argvForParked(t, workspace.PendingPrompt{Text: "build the thing"})
	review := argvForParked(t, workspace.PendingPrompt{Text: "review PR 12", Review: true})

	if strings.Join(coding, " ") == strings.Join(review, " ") {
		t.Fatalf("a review brief and a coding prompt started the same agent: %v", review)
	}
	if !lastArgIs(review, "review PR 12") {
		t.Errorf("the brief did not reach the reviewer as its argument: %v", review)
	}
	if !lastArgIs(coding, "build the thing") {
		t.Errorf("the prompt did not reach the coding agent as its argument: %v", coding)
	}
}

func argvForParked(t *testing.T, p workspace.PendingPrompt) []string {
	t.Helper()
	panes := zmxPanes{
		client: zmx.New((&fakeZmx{}).run),
		svcFor: func(string) workspace.Service { return &promptSvc{pending: p} },
	}
	cmd, _, err := panes.Open(paneItem(), deckui.PaneKindAgent, 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return cmd.Args
}

func lastArgIs(argv []string, want string) bool {
	return len(argv) > 0 && argv[len(argv)-1] == want
}

func findFunc(f *ast.File, name string) *ast.FuncDecl {
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func endsInReturn(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	_, ok := b.List[len(b.List)-1].(*ast.ReturnStmt)
	return ok
}

func calleeName(fun ast.Expr) string {
	switch e := fun.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

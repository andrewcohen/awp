package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepoConfig(t *testing.T, json string) string {
	t.Helper()
	dir := t.TempDir()
	// Isolate global config + the ~/.awp preamble file from the host.
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	if err := os.MkdirAll(filepath.Join(dir, ".awp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".awp", "config.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodingAgentInvocationInjectsForClaude(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)
	got := codingAgentInvocation(dir)
	if !strings.Contains(got, "--append-system-prompt") {
		t.Fatalf("claude + configured dev_loop should inject the loop, got %q", got)
	}
	if !strings.Contains(got, "--append-system-prompt-file ") {
		t.Fatalf("preamble should be passed by file path, got %q", got)
	}
}

// A pane execs the agent directly, so it needs the same instruction the tmux
// path sends. Without it an agent opened with `a` does not know to work in
// units, run gates or commit, and the dev-loop config reads as ignored.
func TestTheArgvFormCarriesThePreambleToo(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)
	argv := codingAgentArgv(dir)

	var path string
	for i, a := range argv {
		if a == appendPreambleFlag && i+1 < len(argv) {
			path = argv[i+1]
		}
	}
	if path == "" {
		t.Fatalf("the pane's agent got no preamble: %q", argv)
	}
	// The trap that makes the two forms irreducible: the shell form quotes the
	// path because tmux runs it through a shell. An argv element is passed to
	// exec verbatim, so a quote here becomes part of the filename.
	if strings.ContainsAny(path, "'\"") {
		t.Errorf("the preamble path is shell-quoted in an argv: %q — Claude will look for a file with quotes in its name", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the preamble path does not exist: %v", err)
	}
}

// Both forms have to agree about whether a preamble applies; only how they
// render it may differ.
func TestBothFormsAgreeOnWhetherAPreambleApplies(t *testing.T) {
	for _, tc := range []struct{ name, cfg string }{
		{"claude with a dev_loop", `{"agent":"claude","dev_loop":{"phases":["implement"],"gates":[{"name":"test","phase":"implement","match":"go test"}]}}`},
		{"claude with no dev_loop", `{"agent":"claude"}`},
		{"another agent", `{"agent":"pi","dev_loop":{"phases":["implement"],"gates":[{"name":"test","phase":"implement","match":"go test"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRepoConfig(t, tc.cfg)
			inShell := strings.Contains(codingAgentInvocation(dir), appendPreambleFlag)
			inArgv := false
			for _, a := range codingAgentArgv(dir) {
				if a == appendPreambleFlag {
					inArgv = true
				}
			}
			if inShell != inArgv {
				t.Errorf("the shell form says preamble=%v but the argv form says %v", inShell, inArgv)
			}
		})
	}
}

func TestCodingAgentInvocationSkipsNonClaude(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "pi",
		"dev_loop": {"gates": [{"name": "test", "phase": "x", "match": "go test"}]}
	}`)
	if strings.Contains(codingAgentInvocation(dir), "--append-system-prompt") {
		t.Fatal("non-claude agent must not get --append-system-prompt")
	}
}

// A repo with no dev_loop still gets a preamble — the workspace half of it.
//
// It used to get none, so its agents were never told the one thing every awp
// agent can do: title the row they appear on. The loop half is the part a
// dev_loop turns on, and it is absent here rather than the whole file being.
func TestARepoWithNoDevLoopStillGetsTheWorkspacePreamble(t *testing.T) {
	dir := writeRepoConfig(t, `{"agent": "claude"}`)
	if !strings.Contains(codingAgentInvocation(dir), appendPreambleFlag) {
		t.Fatal("a Claude agent in a repo without a dev_loop got no preamble at all")
	}
	text := agentPreamble(dir)
	if !strings.Contains(text, "awp w label") {
		t.Errorf("the preamble does not tell the agent how to title its workspace:\n%s", text)
	}
	if strings.Contains(text, "one small, independently committable unit") {
		t.Errorf("a repo with no dev_loop was given the loop instruction:\n%s", text)
	}
}

// And a repo with one gets both halves, in that order.
func TestARepoWithADevLoopGetsBothHalves(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)
	text := agentPreamble(dir)
	title := strings.Index(text, "awp w label")
	loop := strings.Index(text, "one small, independently committable unit")
	if title < 0 || loop < 0 {
		t.Fatalf("the preamble is missing a half (title=%d loop=%d):\n%s", title, loop, text)
	}
	if title > loop {
		t.Error("the loop instruction comes before the workspace's own")
	}
}

// preambleTextIn reads the preamble an argv (or a shell line, split on spaces)
// carries, for the tests about which flavor an agent was started with.
//
// The flavors are told apart by content rather than by presence: a reviewer used
// to get no preamble at all, and now gets the workspace section without the loop
// one — so "is there a flag" no longer answers the question those tests ask.
func preambleTextIn(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == appendPreambleFlag && i+1 < len(argv) {
			body, err := os.ReadFile(strings.Trim(argv[i+1], "'"))
			if err != nil {
				t.Fatalf("read the preamble at %q: %v", argv[i+1], err)
			}
			return string(body)
		}
	}
	return ""
}

// loopInstruction is the opening line of the dev-loop section, which is what a
// reviewer must not be told.
const loopInstruction = "one small, independently committable unit"

// titleInstruction is the workspace section's verb, which every agent in a
// workspace is told — reviewers included.
const titleInstruction = "awp w label"

// A reviewer gets the workspace section and not the loop one.
//
// Its row has a title like any other and is the better for being titled: a review
// workspace called `pr-2320-jordan-survey-s-a5f9` says nothing a person wants to
// read. What it must not be told is to work in units, run gates and commit.
func TestTheReviewerIsToldAboutItsRowAndNotAboutTheLoop(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)

	coding := preambleTextIn(t, codingAgentArgv(dir))
	if !strings.Contains(coding, loopInstruction) {
		t.Fatalf("the coding agent was not told about the loop, so this proves nothing:\n%s", coding)
	}

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"argv form", reviewAgentArgv(dir)},
		{"shell form", strings.Fields(reviewAgentInvocation(dir))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := preambleTextIn(t, tc.argv)
			if text == "" {
				t.Fatalf("the reviewer got no preamble at all: %v", tc.argv)
			}
			if strings.Contains(text, loopInstruction) {
				t.Errorf("the reviewer was told to work in units:\n%s", text)
			}
			if !strings.Contains(text, titleInstruction) {
				t.Errorf("the reviewer was not told it may title its row:\n%s", text)
			}
		})
	}
}

// The two flavors are separate files.
//
// Claude reads the path at startup rather than when awp hands it over, so one
// file rewritten per launch would let a coding agent starting a moment later
// replace the text under a reviewer — which would deliver the loop instruction to
// a reviewer as a race, the one thing the distinction exists to prevent.
func TestTheReviewersPreambleIsItsOwnFile(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)
	coding, ok := agentPreambleFile(dir)
	if !ok {
		t.Fatal("no coding preamble")
	}
	review, ok := reviewPreambleFile(dir)
	if !ok {
		t.Fatal("no review preamble")
	}
	if coding == review {
		t.Fatalf("both flavors write to %q", coding)
	}
	// And writing one does not disturb the other, which is the property the paths
	// are for.
	if _, ok := agentPreambleFile(dir); !ok {
		t.Fatal("rewriting the coding preamble failed")
	}
	body, err := os.ReadFile(review)
	if err != nil {
		t.Fatalf("read the reviewer's preamble: %v", err)
	}
	if strings.Contains(string(body), loopInstruction) {
		t.Errorf("a coding launch overwrote the reviewer's preamble:\n%s", body)
	}
}

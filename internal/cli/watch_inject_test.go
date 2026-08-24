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

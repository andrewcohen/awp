package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
)

// The preamble is the captain's whole job description, so what these check is that
// the parts a wrong captain would be missing are actually in it.

// TestThePreambleSaysItHasNoRepository. Everything else follows from this: an agent
// that thinks it has a working copy will try to read files, run gates and commit,
// which is the workspace agent's job and not this one's.
func TestThePreambleSaysItHasNoRepository(t *testing.T) {
	got := captainPreamble([]string{"alpha"})
	for _, want := range []string{"captain", "no repository", "no working copy"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble never says %q", want)
		}
	}
}

// TestThePreambleTellsItToNameItsTarget, which is the one rule that makes a captain
// command correct rather than aimed at whatever repo the deck started in.
func TestThePreambleTellsItToNameItsTarget(t *testing.T) {
	got := captainPreamble([]string{"alpha", "beta"})
	if !strings.Contains(got, projectFlag) {
		t.Errorf("the preamble never mentions %s", projectFlag)
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble does not list the project %q, so the captain cannot name it", want)
		}
	}
}

// With no workspaces anywhere it says so, rather than printing an empty list the
// captain would read as "there is nothing I am allowed to touch".
//
// It used to point at deck.project_roots, which was right when the list was every repo
// under those roots. The list is now the projects that have workspaces, so an empty one
// means no work exists yet — not that the config is wrong — and creating the first
// workspace is still something the captain can do.
func TestThePreambleSaysWhenThereIsNoWorkYet(t *testing.T) {
	got := captainPreamble(nil)
	if !strings.Contains(got, "no workspaces yet") {
		t.Errorf("with no workspaces the preamble should say so:\n%s", got)
	}
	if !strings.Contains(got, "still something you can do") {
		t.Errorf("the empty case should not read as a dead end:\n%s", got)
	}
}

// The list is what awp has, not what is on the machine, and it says which — otherwise a
// captain asked about a project it cannot see concludes the project does not exist.
func TestThePreambleSaysTheListIsNotEverything(t *testing.T) {
	got := captainPreamble([]string{"awp", "zmx"})
	for _, want := range []string{"work in them", "not everything on the machine", "you can still name it"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble never says %q, so the list reads as exhaustive:\n%s", want, got)
		}
	}
}

// TestThePreambleStatesEveryRefusalWithItsReason. Stated up front rather than left
// to be discovered: a captain that finds the boundary by trying to merge has spent a
// turn on it and cannot tell a refusal from a breakage.
func TestThePreambleStatesEveryRefusalWithItsReason(t *testing.T) {
	got := captainPreamble([]string{"alpha"})
	for _, want := range []string{"Merge a PR", "publish a review", "Delete a workspace", "prune", "Pin or group", "scope"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble never refuses %q", want)
		}
	}
	// And the reasons, since a rule with no reason is one an agent will argue with.
	for _, want := range []string{"hard to retract", "no other", "reading"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusals never explain %q", want)
		}
	}
}

// TestThePreambleNamesTheGapThatIsLeft. It used to say five verbs were unbuilt; they
// are built, so it says the one thing that is still missing instead — that `send` is
// one-way and no agent can answer back.
//
// The point of the assertion is not the wording but that *something* is said. An
// agent that believes it can ask another agent a question will ask one, wait, and
// report the silence as a failure.
func TestThePreambleNamesTheGapThatIsLeft(t *testing.T) {
	got := captainPreamble([]string{"alpha"})
	for _, want := range []string{"one-way", "answer you"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble never says %q, so the captain does not know it cannot be replied to", want)
		}
	}
	// And it no longer claims the built verbs are missing, which would have the
	// captain decline work it can do. "not built yet" was the last survivor: the
	// closing section went on offering it as a category after #383 removed the
	// paragraph that gave it meaning.
	for _, gone := range []string{"not there yet", "Do not guess at flags", "not built yet"} {
		if strings.Contains(got, gone) {
			t.Errorf("the preamble still says %q, but those verbs exist now", gone)
		}
	}
}

// The three kinds of "cannot" are named, because the user does something different
// with each: a refusal is the captain working correctly, a block is a workspace to go
// look at, and a failure is a bug to report. Collapsing them loses the distinction
// the user actually acts on.
func TestThePreambleDistinguishesRefusedFromBlockedFromFailed(t *testing.T) {
	got := captainPreamble([]string{"alpha"})
	for _, want := range []string{"refused", "blocked", "failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble never names %q as a kind of cannot", want)
		}
	}
}

// TestThePreambleOnlyNamesCommandsThatExist is the guard on the section above: every
// `awp ...` line the preamble offers as something the captain can run has to be a
// verb the CLI actually dispatches. A preamble that drifts ahead of the CLI is how
// the captain starts reporting our own gaps as failures.
func TestThePreambleOnlyNamesCommandsThatExist(t *testing.T) {
	// Asked of the dispatcher rather than compared against a list written here. A
	// hardcoded list is the thing that goes stale: it went stale the moment the five
	// verbs it predated landed, and a guard that fails when the code gets *better* is
	// one people learn to edit rather than read.
	for _, line := range strings.Split(captainPreamble([]string{"alpha"}), "\n") {
		// Only the indented command lines. Prose mentions awp by name too, and a
		// check that could not tell the two apart would be answered by rewording a
		// sentence.
		if !strings.HasPrefix(line, "  awp ") {
			continue
		}
		args := commandWords(line)
		if len(args) == 0 {
			continue
		}
		out := &bytes.Buffer{}
		app := NewApp(&fakeService{}, out)
		// --help so nothing runs: every verb answers it, and a verb that does not
		// exist fails at the dispatcher before anything else can.
		if err := app.Run(append(args, "--help")); err != nil && strings.Contains(err.Error(), "unknown") {
			t.Errorf("the preamble offers `awp %s`, which awp does not dispatch (%v): %q", strings.Join(args, " "), err, line)
		}
	}
}

// commandWords is the command part of a preamble usage line: the words after `awp`
// up to the first flag, placeholder, or the run of spaces that starts a description.
//
// The two-space split matters. Some lines put a description on the same row —
// "  awp workspace attention            what wants attention" — and taking every word
// would hand the dispatcher "attention what wants attention", which fails for the
// wrong reason and reads as the verb not existing.
func commandWords(line string) []string {
	// Everything before the first run of two or more spaces after the command.
	head := strings.TrimSpace(line)
	if i := strings.Index(head, "  "); i > 0 {
		head = head[:i]
	}
	fields := strings.Fields(head)
	if len(fields) == 0 || fields[0] != "awp" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || strings.HasPrefix(f, "<") || strings.HasPrefix(f, "[") || strings.HasPrefix(f, "'") {
			break
		}
		out = append(out, f)
	}
	return out
}

// claudeCaptainHome isolates the captain's home and config, with claude as the
// agent.
//
// The config has to be written rather than left to the default: config.DefaultAgent
// is "pi", and --append-system-prompt-file is Claude-specific, so a test that only
// moved HOME would be testing the no-preamble branch while claiming to test the
// preamble.
func claudeCaptainHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, "config", "awp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("make config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"agent":"claude"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	return home
}

// TestTheCaptainsAgentGetsThePreamble — the wiring. Without it the file is written
// and nothing reads it, which looks identical from the outside until you ask the
// captain what it is.
func TestTheCaptainsAgentGetsThePreamble(t *testing.T) {
	home := claudeCaptainHome(t)

	argv := captainAgentArgv()

	var path string
	for i, a := range argv {
		if a == captainPreambleFlag && i+1 < len(argv) {
			path = argv[i+1]
		}
	}
	if path == "" {
		t.Fatalf("the captain's agent is launched without a preamble: %v", argv)
	}
	if want := filepath.Join(home, ".awp", "captain", "preamble.md"); path != want {
		t.Errorf("the preamble is at %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the preamble: %v", err)
	}
	if !strings.Contains(string(body), "captain") {
		t.Errorf("the file the agent was pointed at is not the preamble:\n%s", body)
	}
}

// TestTheCaptainsAgentIsNotTheCodingAgent. The dev-loop preamble tells an agent to
// work in units, run gates and commit — instructions about a repository the captain
// does not have. Getting this wrong would have the captain trying to run this repo's
// gates from a directory with no code in it.
func TestTheCaptainsAgentIsNotTheCodingAgent(t *testing.T) {
	claudeCaptainHome(t)

	captain := strings.Join(captainAgentArgv(), " ")
	if strings.Contains(captain, "dev-loop") {
		t.Errorf("the captain was given the dev-loop preamble: %s", captain)
	}
}

// A non-Claude agent still gets a captain, just one that has to be told its job in
// the first message. --append-system-prompt-file is Claude's flag, and passing it to
// something else would make the pane fail to open at all — which is strictly worse
// than opening uninstructed.
func TestANonClaudeCaptainStillOpens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, "config", "awp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("make config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"agent":"someagent"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	argv := captainAgentArgv()
	if len(argv) == 0 {
		t.Fatal("a non-Claude captain got no command at all")
	}
	if argv[0] != "someagent" {
		t.Errorf("the captain launches %q, want the configured agent", argv[0])
	}
	if slices.Contains(argv, captainPreambleFlag) {
		t.Errorf("a non-Claude agent was passed %s, which it will not understand: %v", captainPreambleFlag, argv)
	}
}

// TestTheCaptainRunsInItsOwnDirectory, not the repo awp was launched from — the
// confusion the captain exists not to have.
func TestTheCaptainRunsInItsOwnDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := paneDir(deckui.CaptainItem(), deckui.PaneKindCaptain)
	if err != nil {
		t.Fatalf("resolve the captain's directory: %v", err)
	}
	if want := filepath.Join(home, ".awp", "captain"); dir != want {
		t.Errorf("the captain runs in %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the captain's directory was not created: %v", err)
	}
}

// Every other pane still refuses to guess a directory. The captain is an exception
// to that rule and this is what keeps it the only one.
func TestOnlyTheCaptainOpensWithoutAWorkingCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	noPath := deckui.Item{ProjectName: "repo", WorkspaceName: "stray"}
	for _, kind := range []string{deckui.PaneKindAgent, "editor", "vcs", deckui.PaneKindCI, deckui.PaneKindWatch, ""} {
		if _, err := paneDir(noPath, kind); err == nil {
			t.Errorf("the %q pane resolved a directory for a row with no working copy", kind)
		}
	}
}

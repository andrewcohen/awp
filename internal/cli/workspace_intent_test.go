package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
)

// intentRunner answers any command with a canned reply, recording what it
// was asked.
type intentRunner struct {
	out  string
	err  error
	name string
	args []string
}

func (r *intentRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	r.name = name
	r.args = args
	return r.out, r.err
}

// testAgentArgv stands in for what headlessIntentArgv would return, so
// these tests exercise the deciding without a config file on disk.
var testAgentArgv = []string{"claude"}

func projectsFixture() []deckui.ProjectItem {
	return []deckui.ProjectItem{
		{Name: "awp", Path: "/repos/awp"},
		{Name: "storefront", Path: "/repos/storefront"},
	}
}

func TestResolveWorkspaceIntentUsesTheAgentsAnswer(t *testing.T) {
	r := &intentRunner{out: `{"name":"sidebar-cursor-bug","label":"Sidebar cursor bug","prompt":"Fix the cursor in the sidebar.","project":"storefront"}`}
	got, err := resolveWorkspaceIntent(context.Background(), r, testAgentArgv, "fix the sidebar cursor bug", "/repos/awp", projectsFixture())
	if err != nil {
		t.Fatalf("resolveWorkspaceIntent: %v", err)
	}
	if got.Name != "sidebar-cursor-bug" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Label != "Sidebar cursor bug" {
		t.Errorf("Label = %q", got.Label)
	}
	if got.Prompt != "Fix the cursor in the sidebar." {
		t.Errorf("Prompt = %q", got.Prompt)
	}
	if got.Project != "storefront" || got.RepoRoot != "/repos/storefront" {
		t.Errorf("project = %q at %q, want storefront at /repos/storefront", got.Project, got.RepoRoot)
	}
}

// A project the model invented must not create a workspace in a directory
// nobody chose — it falls back to the row's own repository.
func TestResolveWorkspaceIntentIgnoresUnknownProject(t *testing.T) {
	r := &intentRunner{out: `{"name":"a-thing","label":"A thing","prompt":"do it","project":"backend"}`}
	got, err := resolveWorkspaceIntent(context.Background(), r, testAgentArgv, "do a thing", "/repos/awp", projectsFixture())
	if err != nil {
		t.Fatalf("resolveWorkspaceIntent: %v", err)
	}
	if got.RepoRoot != "/repos/awp" {
		t.Errorf("RepoRoot = %q, want the default /repos/awp", got.RepoRoot)
	}
}

// Whatever the model calls a name, what reaches the creation path has to be
// a usable directory name.
func TestResolveWorkspaceIntentSlugsTheName(t *testing.T) {
	r := &intentRunner{out: `{"name":"Fix The Sidebar!","label":"x","prompt":"y","project":"awp"}`}
	got, err := resolveWorkspaceIntent(context.Background(), r, testAgentArgv, "whatever", "/repos/awp", projectsFixture())
	if err != nil {
		t.Fatalf("resolveWorkspaceIntent: %v", err)
	}
	if got.Name != "fix-the-sidebar" {
		t.Errorf("Name = %q, want fix-the-sidebar", got.Name)
	}
}

// Fenced or prefaced JSON is what agents actually emit; dropping to the
// form over a code fence would make the box feel broken.
func TestParseIntentReplyToleratesFencesAndPreamble(t *testing.T) {
	out := "Sure, here you go:\n\n```json\n{\"name\":\"a\",\"label\":\"b\",\"prompt\":\"c\",\"project\":\"awp\"}\n```\n"
	reply, err := parseIntentReply(out)
	if err != nil {
		t.Fatalf("parseIntentReply: %v", err)
	}
	if reply.Name != "a" || reply.Project != "awp" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestParseIntentReplyRejectsNonJSON(t *testing.T) {
	if _, err := parseIntentReply("I could not work that out, sorry."); err == nil {
		t.Fatal("parseIntentReply accepted prose")
	}
}

// Every failure has to hand back something the structured form can be
// pre-filled from, or the fallback path opens an empty form and the user
// retypes their sentence.
func TestResolveWorkspaceIntentFailuresCarryAFallback(t *testing.T) {
	cases := []struct {
		name   string
		runner *intentRunner
	}{
		{"agent failed", &intentRunner{err: errors.New("boom")}},
		{"unparseable answer", &intentRunner{out: "no idea"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveWorkspaceIntent(context.Background(), tc.runner, testAgentArgv, "fix the sidebar cursor bug", "/repos/awp", projectsFixture())
			if err == nil {
				t.Fatal("expected an error")
			}
			if got.Name != "fix-the-sidebar-cursor-bug" {
				t.Errorf("fallback Name = %q", got.Name)
			}
			if got.Prompt != "fix the sidebar cursor bug" {
				t.Errorf("fallback Prompt = %q", got.Prompt)
			}
			if got.RepoRoot != "/repos/awp" {
				t.Errorf("fallback RepoRoot = %q", got.RepoRoot)
			}
		})
	}
}

func TestResolveWorkspaceIntentRequiresTextAndProject(t *testing.T) {
	r := &intentRunner{out: "{}"}
	if _, err := resolveWorkspaceIntent(context.Background(), r, testAgentArgv, "   ", "/repos/awp", nil); err == nil {
		t.Error("blank text was accepted")
	}
	if _, err := resolveWorkspaceIntent(context.Background(), r, testAgentArgv, "something", "", nil); err == nil {
		t.Error("missing default repo was accepted")
	}
}

// No agent is the offline case, and it must not reach the runner at all.
func TestResolveWorkspaceIntentWithNoAgentFallsBack(t *testing.T) {
	r := &intentRunner{out: `{"name":"nope"}`}
	got, err := resolveWorkspaceIntent(context.Background(), r, nil, "tidy up the deck", "/repos/awp", projectsFixture())
	if err == nil {
		t.Fatal("expected an error with no agent")
	}
	if r.name != "" {
		t.Errorf("ran %q with no agent configured", r.name)
	}
	if got.Name != "tidy-up-the-deck" || got.Project != "awp" {
		t.Errorf("fallback = %+v", got)
	}
}

// The agent is asked headlessly, with the prompt as an argument.
func TestResolveWorkspaceIntentInvokesTheAgentHeadlessly(t *testing.T) {
	r := &intentRunner{out: `{"name":"a","label":"b","prompt":"c","project":"awp"}`}
	if _, err := resolveWorkspaceIntent(context.Background(), r, []string{"claude", "--model", "opus"}, "do a thing", "/repos/awp", projectsFixture()); err != nil {
		t.Fatalf("resolveWorkspaceIntent: %v", err)
	}
	if r.name != "claude" {
		t.Errorf("ran %q, want claude", r.name)
	}
	if len(r.args) < 3 || r.args[0] != "--model" || r.args[1] != "opus" {
		t.Errorf("configured agent options were dropped: %q", r.args)
	}
	if r.args[len(r.args)-2] != intentPromptFlag {
		t.Errorf("args = %q, want the prompt flag before the prompt", r.args)
	}
}

// The gate decides which agents get a headless call. Guessing another
// agent's spelling of `-p` would run a binary that exists and does the
// wrong thing, so anything not Claude declines to the structured form.
func TestHeadlessIntentArgvGatesOnClaude(t *testing.T) {
	cases := []struct {
		invocation string
		wantOK     bool
		wantArgv   []string
	}{
		{"claude", true, []string{"claude"}},
		{"claude --model opus", true, []string{"claude", "--model", "opus"}},
		{"claude-next", true, []string{"claude-next"}},
		{"pi", false, nil},
		{"codex --yolo", false, nil},
		{"", false, nil},
		{"   ", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.invocation, func(t *testing.T) {
			argv, ok := headlessIntentArgv(tc.invocation)
			if ok != tc.wantOK {
				t.Fatalf("headlessIntentArgv(%q) ok = %v, want %v", tc.invocation, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if strings.Join(argv, " ") != strings.Join(tc.wantArgv, " ") {
				t.Errorf("argv = %q, want %q", argv, tc.wantArgv)
			}
		})
	}
}

// The resolver always produces a message, whatever happens underneath —
// the deck has no other way out of the in-flight state.
func TestIntentResolverAlwaysReportsSomething(t *testing.T) {
	resolve := intentResolverFromRoots(&intentRunner{err: errors.New("offline")}, []string{"/nonexistent-root"}, 2)
	msg := resolve("fix the sidebar cursor bug", "/repos/awp")()
	done, ok := msg.(deckui.IntentDoneMsg)
	if !ok {
		t.Fatalf("resolver produced %T, want deckui.IntentDoneMsg", msg)
	}
	if done.Text != "fix the sidebar cursor bug" {
		t.Errorf("Text = %q, want the text echoed back", done.Text)
	}
	if done.Intent.RepoRoot != "/repos/awp" {
		t.Errorf("Intent.RepoRoot = %q, want the fallback", done.Intent.RepoRoot)
	}
	if done.Intent.Prompt == "" || done.Intent.Name == "" {
		t.Errorf("fallback intent is not usable: %+v", done.Intent)
	}
}

// The prompt must offer the model only projects that exist, since anything
// else it answers is discarded.
func TestIntentPromptListsOnlyKnownProjects(t *testing.T) {
	p := intentPrompt("do a thing", projectsFixture(), "awp")
	for _, want := range []string{"awp", "storefront", "do a thing"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

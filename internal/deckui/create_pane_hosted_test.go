package deckui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestOnlyAnAgentHostingDeckSaysSo: the question is not "is there a pane
// backend" but "does it host the agent". A backend that only handles editors
// and shells leaves the agent to tmux, and the create flow must keep starting
// one.
func TestOnlyAnAgentHostingDeckSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend PaneBackend
		want    bool
	}{
		{"no backend", nil, false},
		{"hosts the agent", allKinds(), true},
		{"hosts everything but the agent", &fakePanes{handles: map[string]bool{"editor": true, "": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, func(ActionRequest) error { return nil })
			if tc.backend != nil {
				m = m.WithPaneBackend(tc.backend)
			}
			if got := m.hostsAgents(); got != tc.want {
				t.Fatalf("hostsAgents() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCreateTellsTheHandlerWhoHostsTheAgent is the deck's half of the
// two-agents bug: the handler cannot see m.panes, so unless the request says
// so it will start an agent in tmux for a deck that already has one.
func TestCreateTellsTheHandlerWhoHostsTheAgent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend PaneBackend
		want    bool
	}{
		{"tmux deck", nil, false},
		{"pane deck", allKinds(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan bool, 1)
			m := New(nil, func(r ActionRequest) error {
				got <- r.PaneHosted
				return nil
			})
			if tc.backend != nil {
				m = m.WithPaneBackend(tc.backend)
			}
			_, cmd := m.startCreateAction(NewWorkspaceRequest{Name: "qa"}, "/repo")
			runAll(cmd)
			select {
			case v := <-got:
				if v != tc.want {
					t.Fatalf("ActionRequest.PaneHosted = %v, want %v", v, tc.want)
				}
			default:
				t.Fatal("handler was never called")
			}
		})
	}
}

// TestTheAsyncCreateJobCarriesWhoHostsTheAgent: the job runs as a detached
// subprocess with no terminal, so it cannot start a hosted agent even in
// principle. It has to be told to park the prompt instead.
func TestTheAsyncCreateJobCarriesWhoHostsTheAgent(t *testing.T) {
	var spec AsyncJobSpec
	m := New(nil, func(ActionRequest) error { return nil }).
		WithPaneBackend(allKinds()).
		WithAsyncJobLauncher(func(s AsyncJobSpec) error { spec = s; return nil })
	_, cmd := m.startAsyncCreateAction(NewWorkspaceRequest{Name: "qa", Prompt: "fix tests"}, "/repo")
	runAll(cmd)
	if !spec.PaneHosted {
		t.Fatalf("async create spec did not say the deck hosts agents: %#v", spec)
	}
	if spec.Prompt != "fix tests" {
		t.Fatalf("the job still needs the prompt to park it; got %q", spec.Prompt)
	}
}

// TestTheReviewJobCarriesWhoHostsTheAgent: the second entry point with the
// same defect. A review workspace exists only to hold a reviewing agent, so a
// review that starts one in tmux puts the whole thing you asked for somewhere
// a pane deck cannot open.
func TestTheReviewJobCarriesWhoHostsTheAgent(t *testing.T) {
	var spec AsyncJobSpec
	m := New(nil, func(ActionRequest) error { return nil }).
		WithPaneBackend(allKinds()).
		WithAsyncJobLauncher(func(s AsyncJobSpec) error { spec = s; return nil })
	_, cmd := m.startReview(Item{RepoRoot: "/repo"}, 12, "andrew/thing")
	runAll(cmd)
	if spec.Action != "review" {
		t.Fatalf("expected a review job, got %q", spec.Action)
	}
	if !spec.PaneHosted {
		t.Fatalf("review spec did not say the deck hosts agents: %#v", spec)
	}
}

// TestTheHandlerPathCarriesItToo covers the synchronous fallback: every
// handler-bound action goes through dispatch, so review's no-async path and
// anything else that starts an agent get the same answer.
func TestTheHandlerPathCarriesItToo(t *testing.T) {
	got := make(chan bool, 1)
	m := New(nil, func(r ActionRequest) error { got <- r.PaneHosted; return nil }).
		WithPaneBackend(allKinds())
	runAll(m.dispatch(ActionReview, Item{RepoRoot: "/repo"}, "12"))
	select {
	case v := <-got:
		if !v {
			t.Fatal("dispatch did not tell the handler this deck hosts agents")
		}
	default:
		t.Fatal("handler was never called")
	}
}

// TestAPaneDeckDoesNotQuitAfterCreating: the tmux deck quits because its
// create ends in switch-client — the terminal already belongs to the new
// session. A deck that hosts its own panes is the outermost program, so
// quitting would drop the user at a shell.
func TestAPaneDeckDoesNotQuitAfterCreating(t *testing.T) {
	for _, tc := range []struct {
		name     string
		backend  PaneBackend
		wantQuit bool
	}{
		{"tmux deck", nil, true},
		{"pane deck", allKinds(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, func(ActionRequest) error { return nil })
			if tc.backend != nil {
				m = m.WithPaneBackend(tc.backend)
			}
			m = m.WithRefresher(func() tea.Cmd { return func() tea.Msg { return nil } })
			next, cmd := m.Update(actionResultMsg{action: ActionCreateWorkspace, item: Item{WorkspaceName: "qa"}})
			if _, ok := next.(Model); !ok {
				t.Fatalf("Update returned %T", next)
			}
			if got := isQuit(cmd); got != tc.wantQuit {
				t.Fatalf("quit after create = %v, want %v", got, tc.wantQuit)
			}
		})
	}
}

// runAll runs cmd and everything a tea.Batch fans out to, synchronously, so a
// test can see the side effects the deck's runtime would produce.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runAll(c)
		}
	}
}

// isQuit reports whether cmd is tea.Quit. Comparing the message it produces is
// the only way to ask: commands are opaque funcs.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

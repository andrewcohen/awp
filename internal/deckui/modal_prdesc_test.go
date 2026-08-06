package deckui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// prDescDeck is a deck on one row whose PR is #42, with a loader that answers
// however the caller says.
func prDescDeck(t *testing.T, load PRDescriptionLoader) Model {
	t.Helper()
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	m := New([]Item{item}, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/r": {"feat": {Number: 42, URL: "https://example/pr/42", State: PRStateOpen}},
		}, nil)
	if load != nil {
		m = m.WithPRDescriptionLoader(load)
	}
	m.width, m.height = 120, 40
	return m
}

func pressRune(m Model, r rune) (Model, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(Model), cmd
}

// `p d` opens in the deck. It used to cost a tmux window — one to make, one to
// switch to, one to come back from — for something you mostly want to glance at.
func TestPRMenuDOpensTheDescriptionInTheDeck(t *testing.T) {
	var askedFor int
	m := prDescDeck(t, func(item Item, number int) (PRDescription, error) {
		askedFor = number
		return PRDescription{Number: number, Title: "Add a thing", Author: "andrewcohen", State: "OPEN", Body: "why the thing"}, nil
	})

	m, _ = pressRune(m, 'p')
	m, cmd := pressRune(m, 'd')

	pd, ok := m.active.(*prDescModal)
	if !ok {
		t.Fatalf("expected the description modal after p d, got %T", m.active)
	}
	if !pd.loading {
		t.Fatal("the modal should open loading rather than blocking the keyboard on gh")
	}
	// It says it is loading rather than showing an empty box, which reads as a PR
	// with nothing written on it.
	if got := pd.prDescBody(80); !strings.Contains(got, "loading") {
		t.Fatalf("a loading modal should say so, got %q", got)
	}

	msg := execCmd(t, cmd)
	updated, _ := m.Update(msg)
	m = updated.(Model)
	pd, ok = m.active.(*prDescModal)
	if !ok {
		t.Fatalf("the modal closed while its fetch was in flight, active is %T", m.active)
	}
	if askedFor != 42 {
		t.Fatalf("fetched PR #%d, want the row's #42", askedFor)
	}
	if pd.loading {
		t.Fatal("the modal is still loading after its fetch came back")
	}
	if got := pd.prDescBody(80); !strings.Contains(got, "why the thing") {
		t.Fatalf("body should be the PR's, got %q", got)
	}
	if got := pd.prDescHeader(); !strings.Contains(got, "Add a thing") || !strings.Contains(got, "#42") {
		t.Fatalf("header should name the PR and its number, got %q", got)
	}
}

// A fetch that failed says what went wrong. An empty pane is indistinguishable
// from a PR with no description, so the one thing this must not do is look
// like a successful read of nothing.
func TestAFailedDescriptionFetchSaysSo(t *testing.T) {
	m := prDescDeck(t, func(Item, int) (PRDescription, error) {
		return PRDescription{}, errors.New("gh: not authenticated")
	})
	m, _ = pressRune(m, 'p')
	m, cmd := pressRune(m, 'd')
	updated, _ := m.Update(execCmd(t, cmd))
	pd := updated.(Model).active.(*prDescModal)

	body := pd.prDescBody(80)
	if !strings.Contains(body, "not authenticated") {
		t.Fatalf("the reason should be in the pane, got %q", body)
	}
	if !strings.Contains(body, "42") {
		t.Fatalf("the number should be there too — gh's error rarely carries it, got %q", body)
	}
}

// An empty description is stated, not left blank.
func TestAnEmptyDescriptionIsStated(t *testing.T) {
	pd := &prDescModal{number: 7, desc: PRDescription{Number: 7, Title: "t"}}
	if got := pd.prDescBody(80); !strings.Contains(got, "no description") {
		t.Fatalf("expected the pane to say the PR has no description, got %q", got)
	}
}

// A reply for a PR the reader has moved on from is dropped. Otherwise a slow
// fetch repaints itself over whichever description they opened next.
func TestALateFetchForAnotherPRIsIgnored(t *testing.T) {
	pd := &prDescModal{number: 42, loading: true}
	pd.update(&Model{}, prDescLoadedMsg{number: 7, desc: PRDescription{Body: "the wrong PR"}})
	if !pd.loading {
		t.Fatal("a reply for a different PR should not resolve this one")
	}
	if strings.Contains(pd.prDescBody(80), "the wrong PR") {
		t.Fatal("another PR's body was painted into this modal")
	}
}

// With no loader installed, `p d` says so instead of opening a box that can
// never fill — and points at the key that still works.
func TestWithoutALoaderTheDeckSaysHowToRead(t *testing.T) {
	m := prDescDeck(t, nil)
	m, _ = pressRune(m, 'p')
	m, _ = pressRune(m, 'd')
	if _, ok := m.active.(*prDescModal); ok {
		t.Fatal("expected no modal without a loader")
	}
	if !strings.Contains(m.status, "p D") {
		t.Fatalf("the refusal should name the key that does work, got %q", m.status)
	}
}

// `p D` keeps the tmux window, in a window named for what is in it. The session
// already has agent / editor / review / vcs; this used to join them as `pr`,
// which named the subject rather than which of the PR things you have open.
func TestPRMenuShiftDOpensTheDescriptionWindow(t *testing.T) {
	var req ActionRequest
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	m := New([]Item{item}, func(r ActionRequest) error { req = r; return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/r": {"feat": {Number: 42, State: PRStateOpen}},
		}, nil)
	m.width, m.height = 120, 40

	m, _ = pressRune(m, 'p')
	m, cmd := pressRune(m, 'D')
	if _, ok := m.active.(*prDescModal); ok {
		t.Fatal("D is the window, not the in-deck modal")
	}
	if cmd != nil {
		execCmd(t, cmd)
	}
	if req.Action != ActionOpenWindow {
		t.Fatalf("expected an open-window action, got %v", req.Action)
	}
	name, command, found := strings.Cut(req.Arg, ":")
	if !found {
		t.Fatalf("expected a name:command arg, got %q", req.Arg)
	}
	if name != prDescWindow {
		t.Fatalf("window name is %q, want %q", name, prDescWindow)
	}
	if !strings.Contains(command, "gh pr view 42") {
		t.Fatalf("the window should run gh against the row's PR, got %q", command)
	}
}

// Both keys refuse for the same reasons. They are one request with two
// destinations, so a row that cannot answer one cannot answer the other.
func TestBothDescriptionKeysRefuseAPRlessRow(t *testing.T) {
	for _, k := range []rune{'d', 'D'} {
		m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r"}},
			func(ActionRequest) error { return nil }).
			WithPRDescriptionLoader(func(Item, int) (PRDescription, error) {
				t.Fatalf("p %c fetched a description for a row with no PR", k)
				return PRDescription{}, nil
			})
		m.width, m.height = 120, 40
		m, _ = pressRune(m, 'p')
		m, _ = pressRune(m, k)
		if _, ok := m.active.(*prDescModal); ok {
			t.Fatalf("p %c opened a modal for a row with no PR", k)
		}
		if !strings.Contains(m.status, "no PR") {
			t.Fatalf("p %c should say the row has no PR, got %q", k, m.status)
		}
	}
}

// esc closes it, and so does `d` — the key that opened it, which is how every
// other overlay in the deck behaves.
func TestTheDescriptionModalClosesOnEscAndOnD(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}},
		{"d", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}},
	} {
		m := prDescDeck(t, func(Item, int) (PRDescription, error) { return PRDescription{Body: "b"}, nil })
		m, _ = pressRune(m, 'p')
		m, _ = pressRune(m, 'd')
		if _, ok := m.active.(*prDescModal); !ok {
			t.Fatalf("fixture is wrong: no modal to close for %s", tc.name)
		}
		updated, _ := m.Update(tc.key)
		if got := updated.(Model).active; got != nil {
			t.Errorf("%s should have closed the description, active is %T", tc.name, got)
		}
	}
}

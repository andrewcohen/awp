package deckui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The forge hub as a whole. The individual verbs are covered where the thing they
// open is — merge and set-number in model_test.go, the description in
// modal_prdesc_test.go, the review in modal_diff_test.go. What is left, and what
// these are for, is the hub itself: which key opens it, which no longer does, and
// that pressing it does not silently drop a verb.

func forgeDeck(t *testing.T) Model {
	t.Helper()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Path: "/r/ws"}},
		func(ActionRequest) error { return nil })
	m.width, m.height = 120, 40
	return m
}

// `p` is unbound, not a second door to the same menu. It was the hub's key until
// `C` took over, and leaving it wired would be two spellings of one action — which
// is how they start disagreeing about what they can do.
func TestPIsNotAForgeMenuAnyMore(t *testing.T) {
	m, _ := pressKey(forgeDeck(t), "p")
	if m.active != nil {
		t.Fatalf("p should be unbound, but it armed %T", m.active)
	}
}

func TestCArmsTheForgeHub(t *testing.T) {
	m, _ := pressKey(forgeDeck(t), "C")
	if _, ok := m.active.(forgeMenuModal); !ok {
		t.Fatalf("expected C to arm the forge hub, got %T", m.active)
	}
}

// Every verb the menu advertises is a key the handler answers. The menu is a
// separate list from the switch that runs the verbs, so a row added to one and not
// the other is a verb that reads as available and does nothing.
func TestEveryVerbTheHubListsIsAKeyItAnswers(t *testing.T) {
	for _, verb := range forgeMenu().verbs {
		if verb == menuCancelVerb {
			continue
		}
		m, _ := pressKey(forgeDeck(t), "C")
		before := m.status
		m, _ = pressKey(m, verb[0])
		// A verb either opens something or says why it cannot — an unhandled key
		// leaves the hub armed with nothing said, which is the state to catch.
		if _, stillArmed := m.active.(forgeMenuModal); stillArmed && m.status == before {
			t.Errorf("the hub lists %q (%s) but pressing it did nothing", verb[0], verb[1])
		}
	}
}

// esc leaves no message behind. You pressed it, so you know you cancelled; the
// design system's rule is that a cancellation clears the status rather than
// echoing it.
func TestCancellingTheHubSaysNothing(t *testing.T) {
	m, _ := pressKey(forgeDeck(t), "C")
	m.status = "something earlier"
	m, _ = pressKey(m, "esc")
	if m.active != nil {
		t.Fatalf("esc should close the hub, got %T", m.active)
	}
	if m.status != "" {
		t.Fatalf("a cancellation should leave nothing behind, got %q", m.status)
	}
}

// The overlay writes every verb down. The menu only appears once you have pressed
// the key, so `?` is the only place to read the hub's verbs without guessing that
// it exists.
func TestTheHelpOverlayNamesEveryForgeVerb(t *testing.T) {
	var help strings.Builder
	for _, g := range deckKeyGroups() {
		for _, k := range g.Keys {
			help.WriteString(k[0] + "\n")
		}
	}
	listed := ansi.Strip(help.String())
	for _, verb := range forgeMenu().verbs {
		if verb == menuCancelVerb {
			continue
		}
		if !strings.Contains(listed, "C "+verb[0]) {
			t.Errorf("the ? overlay never mentions C %s (%s)", verb[0], verb[1])
		}
	}
}

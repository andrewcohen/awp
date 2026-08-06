package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The `-` chord: switching what range the view is a review of.
//
// It lives here rather than in a host because it belongs to the open view — which
// range you want to read is a question you answer after you have started reading —
// and because there are two hosts. The deck's modal had it first and standalone
// `awp diff` did not, which made the same surface answer the same key two
// different ways depending on how you opened it. One implementation, installed by
// whoever knows how to load each range.

// ScopeOption is one entry in the `-` menu: a key, what to call it, and how to
// read it.
//
// Load is a whole diff reader rather than a revset because this package does not
// know what a revset is — it never shells out. The host supplies a closure per
// scope, the same shape it supplies LoadDiff.
type ScopeOption struct {
	// Key picks this scope while the chord is up.
	Key string
	// Label names it in the menu and in the chrome — "vs stack base".
	Label string
	// Load reads the diff for this scope.
	Load func() (string, error)
	// Base names what the diff is read against, for the chrome. Optional; a scope
	// diffed against the working copy has nothing to name.
	Base func() string
}

// WithScopes installs the `-` menu. The first option is the one the view opens
// on, so a host lists them in the order it wants them offered and puts its
// default first. Fewer than two options leaves the chord unavailable rather than
// offering a menu with one answer in it.
func (m *Model) WithScopes(opts []ScopeOption) {
	if len(opts) < 2 {
		return
	}
	m.scopes = opts
	m.scopeIndex = 0
}

// scopeMenuHint is the chord's footer while it is up: every scope with its key,
// the current one marked so the menu says where you are as well as where you can
// go.
func (m Model) scopeMenuHint() string {
	parts := make([]string, 0, len(m.scopes)+2)
	parts = append(parts, "scope:")
	for i, s := range m.scopes {
		label := s.Key + " " + s.Label
		if i == m.scopeIndex {
			label += " (current)"
		}
		parts = append(parts, label)
	}
	return strings.Join(append(parts, "esc cancel"), " · ")
}

// scopeKeysHint is the same menu on one line, for the `?` reference where there is
// no current scope to mark.
func (m Model) scopeKeysHint() string {
	parts := make([]string, 0, len(m.scopes))
	for _, s := range m.scopes {
		parts = append(parts, s.Key+" "+s.Label)
	}
	return strings.Join(parts, " · ")
}

// ScopeLabel is the current scope's name, empty when no scopes are installed.
// Hosts put it in their own chrome.
func (m Model) ScopeLabel() string {
	if m.scopeIndex < 0 || m.scopeIndex >= len(m.scopes) {
		return ""
	}
	return m.scopes[m.scopeIndex].Label
}

// handleScopeKey answers the key that follows `-`.
//
// Anything that is not a scope key cancels, including esc: a mistyped key must not
// fall through to the view and do something else instead.
func (m Model) handleScopeKey(key string) (tea.Model, tea.Cmd) {
	m.scopePick = false
	for i, s := range m.scopes {
		if s.Key != key {
			continue
		}
		if i == m.scopeIndex {
			// Already there. Reloading would drop the reading position to arrive at
			// the same diff.
			return m, nil
		}
		return m.switchScope(i)
	}
	return m, nil
}

// switchScope reads the diff at another scope.
//
// The reload goes through the ordinary load path, so the anchor ladder relocates
// comments and the cursor exactly as it does on a refresh tick — a different range
// is mostly the same code, and landing back where you were reading is the point.
// The fingerprint is cleared first because it identifies the diff text we last
// loaded, and a reload it matched would be dropped as a no-op.
func (m Model) switchScope(i int) (tea.Model, tea.Cmd) {
	s := m.scopes[i]
	m.scopeIndex = i
	m.LoadDiff = s.Load
	m.ResolveBase = s.Base
	m.baseLabel = ""
	m.fingerprint = 0
	m.status = s.Label + ": loading…"
	m.statusErr = false
	return m, tea.Batch(loadDiffCmd(m.LoadDiff), resolveBaseCmd(m.ResolveBase))
}

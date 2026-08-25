package ui

import (
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/charm"
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
	Load func(contextLines int) (string, error)
	// Base names what the diff is read against, for the chrome. Optional; a scope
	// diffed against the working copy has nothing to name.
	Base func() string

	// Choices makes this entry a submenu rather than a range of its own: picking
	// its key resolves this and offers what comes back as a list.
	//
	// It exists because the three fixed ranges and "some particular commit" are
	// not the same kind of answer. A range is a closure behind a letter, and there
	// are three of them; commits are however many the repo has, arrive with no
	// letter to be behind, and are not worth reading from disk until someone asks.
	// So the menu carries one entry that stands for all of them, and the list is
	// built when it is picked.
	//
	// Resolved rather than held for the same reason it is a list: the answer goes
	// stale — a commit made while the viewer was open belongs in it — and asking
	// at the moment of the question is cheaper than keeping it fresh.
	Choices func() ([]ScopeOption, error)
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
	// A picked revision is not one of the installed scopes, so it has no index to
	// be found at — its name is carried directly. Checked first because that is
	// exactly the case where scopeIndex is deliberately out of range.
	if m.chosenLabel != "" {
		return m.chosenLabel
	}
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
		if s.Choices != nil {
			return m.openScopeList(s)
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
	m.scopeIndex = i
	m.chosenLabel = ""
	return m.applyScope(m.scopes[i])
}

// applyScope is the reload itself, shared by the keyed scopes and a picked
// revision because the only thing that differs between them is which entry the
// menu should call current.
func (m Model) applyScope(s ScopeOption) (tea.Model, tea.Cmd) {
	m.LoadDiff = s.Load
	m.ResolveBase = s.Base
	m.baseLabel = ""
	m.fingerprint = 0
	m.status = s.Label + ": loading…"
	m.statusErr = false
	return m, tea.Batch(loadDiffCmd(m.LoadDiff, m.contextLines), resolveBaseCmd(m.ResolveBase))
}

// scopeUnavailable says why `-` has no menu to open.
//
// Two situations, and the wording keeps them apart because they are answered
// differently. No scopes at all is `awp diff -r <revset>`: you named the range on
// the command line, so there is nothing for the view to offer and the way to read
// another one is to say so. Exactly one is a host that installed a single range —
// a menu with one answer in it is not a menu, and naming the range you are already
// on is the useful half of what the menu would have said.
//
// A status line rather than silence, per AGENTS.md: a message names what was
// attempted and what the reader can do about it. This key gave neither, which is
// how a diagnosis that should have been a glance at the footer became a
// conversation.
func scopeUnavailable(scopes []ScopeOption) string {
	if len(scopes) == 0 {
		return "scope: this view was opened on one range (-r), so there is nothing to switch between"
	}
	return "scope: " + scopes[0].Label + " is the only range this view was given"
}

// scopeChoice is one resolved choice in the `- r` list.
//
// bubbles/list wants an Item, and list.DefaultDelegate renders Title over
// Description — which is the shape a change already has: what it is called and
// what it says.
type scopeChoice struct{ opt ScopeOption }

func (c scopeChoice) Title() string       { return c.opt.Key }
func (c scopeChoice) Description() string { return c.opt.Label }

// FilterValue is both halves, so `/` matches an id typed from memory as readily
// as a word remembered from the description.
func (c scopeChoice) FilterValue() string { return c.opt.Key + " " + c.opt.Label }

// openScopeList resolves a submenu entry and puts its choices up as a list.
//
// A failure to resolve is a status line rather than an empty list: "nothing to
// pick" and "the command that would have told us failed" are answered
// differently, and an empty picker says neither.
func (m Model) openScopeList(s ScopeOption) (tea.Model, tea.Cmd) {
	opts, err := s.Choices()
	if err != nil {
		m.fail("scope: %v", err)
		return m, nil
	}
	if len(opts) == 0 {
		m.fail("scope: no revisions to pick from")
		return m, nil
	}
	items := make([]list.Item, 0, len(opts))
	for _, o := range opts {
		items = append(items, scopeChoice{opt: o})
	}
	delegate := list.NewDefaultDelegate()
	l := list.New(items, &delegate, m.width, max(minBodyHeight, m.bodyHeight))
	charm.ApplyListTheme(&l, &delegate)
	l.Title = s.Label
	l.SetShowStatusBar(false)
	m.scopeList = l
	m.scopeListing = true
	return m, nil
}

// handleScopeListKey is the keyboard while the `- r` list is up.
//
// The list owns it, the way the help overlay owns it: nothing behind it is
// navigable, so a key that fell through would move a cursor nobody can see. Only
// the keys that leave are held back — and enter is not one of them while a filter
// is being typed, where it means "accept the filter" to bubbles/list and would
// otherwise pick whatever the first match happened to be.
func (m Model) handleScopeListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtering := m.scopeList.SettingFilter()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		if filtering {
			break
		}
		m.scopeListing = false
		return m, nil
	case "enter":
		if filtering {
			break
		}
		choice, ok := m.scopeList.SelectedItem().(scopeChoice)
		if !ok {
			m.scopeListing = false
			return m, nil
		}
		m.scopeListing = false
		return m.switchToChoice(choice.opt)
	}
	var cmd tea.Cmd
	m.scopeList, cmd = m.scopeList.Update(msg)
	return m, cmd
}

// switchToChoice reads the diff at a range that was picked rather than bound to a
// key.
//
// scopeIndex goes to none of them, because a picked revision is not one of the
// installed scopes and claiming an index would make `-` report the wrong entry as
// current. chosenLabel carries the name instead, and ScopeLabel prefers it — so
// the chrome says which commit you are reading, which is the whole point of
// having picked one.
func (m Model) switchToChoice(s ScopeOption) (tea.Model, tea.Cmd) {
	m.scopeIndex = -1
	m.chosenLabel = s.Label
	return m.applyScope(s)
}

// renderScopeList is the list in place of the panes, on the height the host
// budgeted — the same bargain the help overlay makes, and for the same reason:
// the footer must not move because something opened over the body.
func (m Model) renderScopeList(width, height int) string {
	m.scopeList.SetSize(width, height)
	return m.scopeList.View()
}

// ScopeMenu is the menu the chord is waiting on: each entry as key and label, the
// current one marked, and whether the chord is up at all.
//
// Exported because the two hosts draw it in two different places. Standalone
// `awp diff` gives its own footer over to it for the keypress it lives (see
// renderFooter). The deck floats it as a bordered popover instead, the way every
// other menu in the deck is drawn — which it has to, because the deck's footer is
// not always on screen: in a split each half renders its own chrome and the
// viewer's footer is not among it, so a menu that lived in the footer was
// invisible in exactly the layout where two ranges are most worth switching
// between.
//
// Pairs rather than an assembled string, because the host that renders a keymap
// beside its description already knows how (charm.KeyHelpView), and a hint built
// here would be a second opinion about how a keymap looks. The footer's one-line
// form stays private, since only one host wants it.
func (m Model) ScopeMenu() (verbs [][2]string, up bool) {
	if !m.scopePick {
		return nil, false
	}
	out := make([][2]string, 0, len(m.scopes))
	for i, s := range m.scopes {
		label := s.Label
		if i == m.scopeIndex {
			label += " (current)"
		}
		out = append(out, [2]string{s.Key, label})
	}
	return out, true
}

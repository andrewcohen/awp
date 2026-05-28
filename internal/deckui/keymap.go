package deckui

import "github.com/charmbracelet/bubbles/key"

// deckKeyMap is the single source of truth for the deck row-mode key
// bindings. Both Update (via key.Matches) and renderHelp / deckKeyGroups
// read from this struct so a rebind in one place is reflected everywhere.
//
// Scope: row-mode (the main switch in Update). Modal-specific close
// patterns ("esc / q / ctrl+c → close") inside the picker and overlay
// handlers still use msg.String() — they're an internal closing
// convention rather than a user-discoverable binding, and centralising
// them adds noise without leverage.
type deckKeyMap struct {
	Help          key.Binding
	Jobs          key.Binding
	Quit          key.Binding
	Filter        key.Binding
	Find          key.Binding
	Down          key.Binding
	Up            key.Binding
	ScopeCycle    key.Binding
	Enter         key.Binding
	AgentWindow   key.Binding
	EditorWindow  key.Binding
	ReviewWindow  key.Binding
	ReviewMainWin key.Binding
	VCSWindow     key.Binding
	ShellWindow   key.Binding
	CIWindow      key.Binding
	LastSession   key.Binding
	Delete        key.Binding
	Rename        key.Binding
	LinkBookmark  key.Binding
	NewMenu       key.Binding
	Open          key.Binding
	UserActions   key.Binding
	EditState     key.Binding
	PRMenu        key.Binding
	OpenURL       key.Binding
	SendPrompt    key.Binding
}

func newDeckKeyMap() deckKeyMap {
	return deckKeyMap{
		Help:          key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help overlay")),
		Jobs:          key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "jobs overlay")),
		Quit:          key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q/esc", "quit · esc clears filter first")),
		Filter:        key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter rows · esc clears")),
		Find:          key.NewBinding(key.WithKeys("f", "F"), key.WithHelp("f", "find: project → workspace easymotion jump")),
		Down:          key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("↓/j", "move cursor")),
		Up:            key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("↑/k", "move cursor")),
		ScopeCycle:    key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "cycle scope (all → attention → open PR)")),
		Enter:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "summon (create or focus the workspace tmux session)")),
		AgentWindow:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "agent window (re-attach without re-prompting)")),
		EditorWindow:  key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "editor window ($EDITOR)")),
		ReviewWindow:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "review window: tuicr -r @")),
		ReviewMainWin: key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "review window: tuicr -r main..@")),
		VCSWindow:     key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "vcs window (jjui)")),
		ShellWindow:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shell window")),
		CIWindow:      key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "ci window (gh run watch)")),
		LastSession:   key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "switch to last tmux session")),
		Delete:        key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete workspace (or project for default row)")),
		Rename:        key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "rename workspace")),
		LinkBookmark:  key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "link a bookmark to the selected workspace")),
		NewMenu:       key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new workspace")),
		Open:          key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open: fuzzy-pick a project from configured roots")),
		UserActions:   key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "user actions menu")),
		EditState:     key.NewBinding(key.WithKeys(","), key.WithHelp(",", "edit raw state JSON in $EDITOR")),
		PRMenu:        key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "PR menu (o open · r repair · s set PR #)")),
		OpenURL:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "open captured dev-server URL")),
		SendPrompt:    key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "send a typed prompt to the workspace's agent")),
	}
}

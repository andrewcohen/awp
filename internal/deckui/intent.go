package deckui

import tea "charm.land/bubbletea/v2"

// WorkspaceIntent is what one sentence of free text resolves to: the four
// things `awp workspace new` needs, worked out from "fix the sidebar cursor
// bug" or "look at PR 2320".
//
// Project and RepoRoot are the same answer in two spellings — the name a
// person reads on the confirm step, and the directory the workspace is
// actually created in. Both are always set: a resolver that cannot decide
// returns the deck row's own repository rather than nothing, because there
// is no such thing as a workspace with no project.
type WorkspaceIntent struct {
	Name     string // directory-safe slug
	Label    string // human-readable, what the deck shows
	Prompt   string // what the agent is started on
	Project  string // display name
	RepoRoot string // absolute path the workspace is created under
}

// IntentResolver turns free text into a WorkspaceIntent, asynchronously,
// emitting an IntentDoneMsg. defaultRepoRoot is the selected row's
// repository — the answer when the text names no project, and the answer
// when it names one that does not exist.
//
// Async because it is a model call: seconds, not milliseconds, and the deck
// keeps rendering while it is in flight. Nil when this deck has no resolver
// (no agent configured for it), in which case the free-text box is skipped
// entirely and `n` opens the structured form directly.
type IntentResolver func(text string, defaultRepoRoot string) tea.Cmd

// IntentDoneMsg carries a resolution back to the deck.
//
// Text is echoed back so a late reply to a cancelled or superseded box can
// be recognized and dropped; without it a resolution that arrives after the
// user has moved on would open a form they did not ask for.
//
// Err is not a dead end. Every failure here — offline, no agent, a timeout,
// output that would not parse — lands the user in the structured form with
// their text as the prompt, which is the form they would have used anyway.
type IntentDoneMsg struct {
	Text   string
	Intent WorkspaceIntent
	Err    error
}

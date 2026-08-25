package cli

import (
	tea "charm.land/bubbletea/v2"
)

// runeKey is the key press a user typing s would produce.
//
// Bubble Tea v2 dropped the KeyRunes type: a printable key arrives as a
// KeyPressMsg carrying the rune in Code and the text it inserts in Text.
// Multi-rune s is text with no single code — that is what pasting or a
// programmatic insert looks like — so only Text is set.
func runeKey(s string) tea.KeyPressMsg {
	if r := []rune(s); len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
	return tea.KeyPressMsg{Text: s}
}

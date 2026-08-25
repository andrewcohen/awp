package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// The `+` / `_` context width.
//
// A hunk on its own answers "what changed" and not "what does this do". Three
// lines of surrounding code is enough to find the line and rarely enough to judge
// it, so the amount is a thing the reader sets while reading, like the scope menu
// and the side-by-side toggle.
//
// Done by re-asking jj with a bigger --context rather than splicing file content
// into the parsed hunks. jj already knows how to widen a hunk, merge two that grow
// into each other, and stop at the start and end of a file — and it answers for
// the revision being read, where the working copy on disk is a different question
// for any scope that does not end at @.

// stepContext moves one rung along contextSteps and reloads.
//
// The cursor is not saved here. A reload with a different context is a reload like
// any other, and diffLoadedMsg already keeps the reader in place by anchoring on
// the file and line rather than the row index — which is the only thing that could
// work, since widening the context changes how many rows every hunk has.
func (m *Model) stepContext(delta int) tea.Cmd {
	next := contextStepIndex(m.contextLines) + delta
	if next < 0 || next >= len(contextSteps) {
		// Refused out loud. A key that quietly did nothing at the end of the ladder
		// reads as a key that has stopped working, and the number is the thing the
		// reader wants to know either way.
		m.status = fmt.Sprintf("context: %d lines — %s", m.contextLines, contextLimitReason(delta))
		return nil
	}

	m.contextLines = contextSteps[next]
	m.status = fmt.Sprintf("context: %s · + more · _ less", contextLabel(m.contextLines))
	m.refreshing = true
	return loadDiffCmd(m.LoadDiff, m.contextLines)
}

// contextStepIndex is which rung a line count sits on, or the nearest one below it
// for a count that is not a rung at all — which only a host setting the field
// directly could produce, and which should still leave the keys working.
func contextStepIndex(lines int) int {
	for i := len(contextSteps) - 1; i >= 0; i-- {
		if contextSteps[i] <= lines {
			return i
		}
	}
	return 0
}

// contextLimitReason says which end of the ladder was hit, in terms of what is on
// screen rather than of the ladder.
func contextLimitReason(delta int) string {
	if delta > 0 {
		return "as much as + offers"
	}
	return "the changed lines only"
}

// contextLabel is the count as chrome. Zero has a name worth using — a diff of
// nothing but the changes — and one line is not "1 lines".
func contextLabel(lines int) string {
	switch lines {
	case 0:
		return "changed lines only"
	case 1:
		return "1 line"
	default:
		return fmt.Sprintf("%d lines", lines)
	}
}

// contextChrome is the footer's word for the context width, and empty at the
// default.
//
// Only when it is not the default, the same rule the pane header follows for which
// emulator is behind it: a reader who has not pressed anything is looking at the
// diff jj would have printed, and a footer segment saying so would be paid for on
// every view to answer a question almost nobody has asked.
func (m Model) contextChrome() string {
	if m.contextLines == contextDefault {
		return ""
	}
	return "context " + contextLabel(m.contextLines)
}

// ContextChrome is contextChrome for a host that composes its own footer — the
// deck's modal. Exported for the same reason Base and ScopeLabel are: the viewer
// owns the state and the host owns the row it is written on.
func (m Model) ContextChrome() string { return m.contextChrome() }

// Compile-time proof that the keys and the loader agree on the type of the thing
// being passed: the count reaches jj or the feature is decoration.
var _ func(int) tea.Cmd = (&Model{}).stepContext

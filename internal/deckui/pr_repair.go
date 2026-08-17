package deckui

// The repair prompt, for callers outside the deck.
//
// prRepairPrompt has always lived in here because the deck was the only thing that
// could ask for it: `C r` reads the row's cached PR status, builds the prompt and
// hands it to the send-prompt form. A captain has no row and no form, and it wants
// the same sentence.
//
// Exported as wrappers rather than by renaming the originals. The two internals are
// referenced by the deck and its tests under their own names, and the interesting
// thing about this seam is not what it is called but that there is exactly one of
// it: a second copy of the repair prompt would drift from this one silently, and
// the drift would be an agent being asked to do the wrong job — which is #171,
// where a reviewer got the author's chores.

// PRRepairPrompt is the prompt asking an agent to deal with what is wrong with a
// PR: merge conflicts, failing CI, an out-of-date base, review feedback.
//
// Empty when the PR is not open, or when nothing is wrong — callers must treat that
// as "nothing to repair" rather than sending a blank message.
//
// mine is what decides whether this is a fix or a look. True asks the agent to
// resolve things and push, because the PR is yours and you mean to ship it. False
// is review mode: investigate and report, but change no files and push nothing.
// Reviewing someone else's PR should not have an agent start rebasing their branch.
func PRRepairPrompt(s PRStatus, localCommitID string, mine bool) string {
	return prRepairPrompt(s, localCommitID, mine)
}

// PRIsMine says whether a workspace's bookmark looks like your own work, by the
// repo's configured bookmark prefix.
//
// The same question `C r` asks, and the answer that picks between the two tones
// above. An empty prefix or an unlinked bookmark answers true: with nothing to
// compare against, the honest default is that this is your branch, and the cost of
// being wrong that way is a prompt that offers to fix rather than one that quietly
// declines to.
func PRIsMine(bookmark, bookmarkPrefix string) bool {
	return itemIsMyPR(Item{Bookmark: bookmark}, bookmarkPrefix)
}

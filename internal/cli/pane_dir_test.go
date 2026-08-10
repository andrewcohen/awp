package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/zmx"
)

// TestAPaneWillNotGuessItsDirectory: Open used to fall back to the source repo
// when the row had no working copy, and to awp's own cwd when it had neither.
// Both start a real program somewhere plausible-looking and wrong — a coding
// agent in the repo it was supposed to be reviewing a workspace of, editing the
// tree everything else shares.
//
// The rows that arrive without a Path are a workspace still being created and an
// unmanaged row (a session with no state entry, which under a pane host is a
// leftover tmux one). Neither has a directory to be right about.
func TestAPaneWillNotGuessItsDirectory(t *testing.T) {
	for _, tc := range []struct {
		name string
		item deckui.Item
	}{
		{
			"still being created",
			deckui.Item{ProjectName: "repo", WorkspaceName: "pr-1234-fix", RepoRoot: "/repo"},
		},
		{
			"unmanaged, so not even a repo",
			deckui.Item{ProjectName: "repo", WorkspaceName: "stray"},
		},
	} {
		for _, kind := range []string{deckui.PaneKindAgent, "editor", "vcs", deckui.PaneKindCI, deckui.PaneKindWatch, ""} {
			panes := zmxPanes{client: zmx.New((&fakeZmx{}).run)}
			cmd, _, err := panes.Open(tc.item, kind, 80, 24)
			if err == nil {
				t.Errorf("%s: the %q pane opened in %q instead of refusing", tc.name, kind, cmd.Dir)
				continue
			}
			// Actionable: name what was attempted and what to do about it.
			for _, want := range []string{tc.item.WorkspaceName, "working copy"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: the %q pane's error %q does not mention %q", tc.name, kind, err, want)
				}
			}
		}
	}
}

// TestAPaneWithAWorkingCopyOpensInIt is the control, and the one thing the
// fallback was ever incidentally right about.
func TestAPaneWithAWorkingCopyOpensInIt(t *testing.T) {
	panes := zmxPanes{client: zmx.New((&fakeZmx{}).run)}
	for _, kind := range []string{deckui.PaneKindAgent, deckui.PaneKindCI, ""} {
		cmd, _, err := panes.Open(paneItem(), kind, 80, 24)
		if err != nil {
			t.Fatalf("the %q pane: %v", kind, err)
		}
		if cmd.Dir != paneItem().Path {
			t.Errorf("the %q pane opened in %q, want the workspace's %q", kind, cmd.Dir, paneItem().Path)
		}
	}
}

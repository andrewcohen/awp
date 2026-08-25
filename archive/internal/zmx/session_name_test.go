package zmx

import (
	"strings"
	"testing"
)

// TestTheReportedNameNowFits. `x d` on a workspace named after a PR's head
// branch produced awp.alpha.pr-2336-dev-mlwzqyrmxslo.action_dev — 47 bytes
// against a ceiling of 46, so the pane could not open at all. One byte, and no
// amount of shortening awp's own contribution would have held: the workspace name
// alone is 24 and the fixed parts leave it 23.
func TestTheReportedNameNowFits(t *testing.T) {
	name := SessionName("alpha", "pr-2336-dev-mlwzqyrmxslo", "action_dev")
	if len(name) > MaxSessionName {
		t.Errorf("the reported name is still %d bytes: %q", len(name), name)
	}
	// The kind is what reopens the pane and what finds the action's command, so it
	// has to survive whole.
	if !strings.HasSuffix(name, ".action_dev") {
		t.Errorf("name %q lost its kind", name)
	}
}

// TestEveryNameFits over inputs that have all appeared: a long project, a
// workspace named after a branch, an action name someone would plausibly write,
// and the pathological case of all three at once.
func TestEveryNameFits(t *testing.T) {
	projects := []string{"awp", "alpha", "Obsidian_Vault", "some-organisation-monorepo-with-a-long-name"}
	workspaces := []string{
		"default",
		"collection-large-page",
		"pr-2336-dev-mlwzqyrmxslo",
		"pr-12345-somebody-else/an-extremely-descriptive-branch-name",
	}
	kinds := []string{"", "ci", "vcs", "agent", "watch", "editor", "action_dev", "action_storybook", "action_integration-tests-with-a-long-name"}
	for _, p := range projects {
		for _, w := range workspaces {
			for _, k := range kinds {
				name := SessionName(p, w, k)
				if len(name) > MaxSessionName {
					t.Errorf("%q/%q/%q is %d bytes: %q", p, w, k, len(name), name)
				}
			}
		}
	}
}

// TestNamesThatFitAreUntouched. Shortening is a last resort, and every name in
// the socket directory today is one of these — a spelling that changed for no
// reason would leave every running session unrecognised and awp would start a
// second of each.
func TestNamesThatFitAreUntouched(t *testing.T) {
	for _, tc := range []struct{ project, workspace, kind, want string }{
		{"alpha", "default", "agent", "awp.alpha.default.agent"},
		{"awp", "default", "editor", "awp.awp.default.editor"},
		{"alpha", "collection-large-page", "action_dev", "awp.alpha.collection-large-page.action_dev"},
		{"alpha", "pr-2336-dev-mlwzqyrmxslo", "agent", "awp.alpha.pr-2336-dev-mlwzqyrmxslo.agent"},
	} {
		if got := SessionName(tc.project, tc.workspace, tc.kind); got != tc.want {
			t.Errorf("SessionName(%q, %q, %q) = %q, want it unchanged as %q", tc.project, tc.workspace, tc.kind, got, tc.want)
		}
	}
}

// TestTwoWorkspacesNeverShareAName is what the fingerprint is for. These two
// agree on every character a plain truncation would keep, so truncating alone
// would address one session from two workspaces — and `a` on one would open the
// other's agent, which is worse than the pane not opening.
func TestTwoWorkspacesNeverShareAName(t *testing.T) {
	a := SessionName("alpha", "pr-2336-dev-mlwzqyrmxslo", "agent")
	b := SessionName("alpha", "pr-2336-dev-qqtnvbdlrxzz", "agent")
	if a == b {
		t.Fatalf("two workspaces share the session %q", a)
	}
	// And the same input keeps giving the same answer, because the name is an
	// address: a create and the pane that opens it later have to agree.
	if again := SessionName("alpha", "pr-2336-dev-mlwzqyrmxslo", "agent"); again != a {
		t.Errorf("the same workspace named two sessions: %q then %q", a, again)
	}
}

// TestEveryKindsStemFindsItsWorkspace. The stem is deliberately kind-dependent —
// it gets whatever room the kind leaves, so a name is only shortened when it has
// to be, and a longer kind shortens the stem further. What has to hold instead is
// that the workspace can still recognise every one of them, which is the question
// the deck actually asks.
func TestEveryKindsStemFindsItsWorkspace(t *testing.T) {
	const project, workspace = "alpha", "pr-12345-somebody-else/a-very-long-branch-name"
	for _, kind := range []string{"", "ci", "agent", "editor", "action_integration-tests"} {
		name := SessionName(project, workspace, kind)
		stem, _, ok := SplitSessionName(name)
		if !ok {
			t.Fatalf("a generated name did not split: %q", name)
		}
		if !StemMatches(project, workspace, stem) {
			t.Errorf("kind %q gave stem %q, which its own workspace does not recognise", kind, stem)
		}
	}
}

// TestAStemMatchesNoOtherWorkspace, at any length. A stem that matched two
// workspaces would hand one row the other's sessions — an `a` press opening
// somebody else's agent, which is worse than a pane that will not open.
func TestAStemMatchesNoOtherWorkspace(t *testing.T) {
	const project = "alpha"
	others := []string{
		"pr-2336-dev-qqtnvbdlrxzz",
		"pr-2336-dev",
		"pr-2336",
		"collection-large-page",
		"default",
	}
	for _, kind := range []string{"", "agent", "action_integration-tests"} {
		stem, _, ok := SplitSessionName(SessionName(project, "pr-2336-dev-mlwzqyrmxslo", kind))
		if !ok {
			t.Fatal("a generated name did not split")
		}
		for _, other := range others {
			if StemMatches(project, other, stem) {
				t.Errorf("stem %q (kind %q) also matched workspace %q", stem, kind, other)
			}
		}
	}
}

// TestSplitKeepsTheWholeKind, including a shortened one: the split is at the last
// dot, and shortening never introduces one. A kind cut short here would resolve to
// no user action, which is the failure the reduction is supposed to prevent.
func TestSplitKeepsTheWholeKind(t *testing.T) {
	name := SessionName("alpha", "pr-2336-dev-mlwzqyrmxslo", "action_integration-tests")
	_, kind, ok := SplitSessionName(name)
	if !ok {
		t.Fatalf("%q did not split", name)
	}
	if kind != SessionKind("action_integration-tests") {
		t.Errorf("kind came back as %q, want the reduction %q", kind, SessionKind("action_integration-tests"))
	}
}

// TestTheKindReductionIsIdempotent, so a caller can apply it to a kind of either
// provenance — one it built from the config, or one it read back off a session —
// and compare the two. That is the whole mechanism keeping a shortened kind
// resolvable to the action it came from.
func TestTheKindReductionIsIdempotent(t *testing.T) {
	for _, kind := range []string{"agent", "action_dev", "action_integration-tests-with-a-long-name"} {
		once := SessionKind(kind)
		if twice := SessionKind(once); twice != once {
			t.Errorf("SessionKind(%q) = %q, then %q — not idempotent", kind, once, twice)
		}
	}
}

// TestASplitRefusesWhatIsNotOurs: `zmx ls` lists every session on the machine,
// and a stem lookup on a name awp did not make would file someone's shell under
// a workspace.
func TestASplitRefusesWhatIsNotOurs(t *testing.T) {
	for _, name := range []string{"", "dev", "awp", ".agent", "notes.agent", "awpx.p.w.agent"} {
		if _, _, ok := SplitSessionName(name); ok {
			t.Errorf("split %q as one of ours", name)
		}
	}
}

package ui

import (
	"errors"
	"strings"
	"testing"
)

// pressRun presses a key and drives whatever command it returned, the way the
// program would.
func pressRun(m Model, s string) Model {
	updated, cmd := pressKeyUI(m, s)
	return run(updated, cmd)
}

// mergeModel is a viewer whose merge seam records what it was asked for. The dry
// run reports the plan, the real run whatever the test wants to have happened.
func mergeModel(t *testing.T, report string, err error) (Model, *[]bool) {
	t.Helper()
	var asked []bool
	m := commentModel(t, fileWith("a.go", 1, "one", "two"))
	m.MergePR = func(dry bool) (string, error) {
		asked = append(asked, dry)
		if dry {
			return "Runs:\n  gh pr merge 7 --squash", nil
		}
		return report, err
	}
	return m, &asked
}

// merges is how many times the seam was asked to merge for real.
func merges(asked []bool) int {
	n := 0
	for _, dry := range asked {
		if !dry {
			n++
		}
	}
	return n
}

// TestMergeShowsTheCallBeforeMakingIt. The review surface is where the decision
// to merge gets made, but a merge is irreversible and outward-facing, so `M` is
// the plan and nothing else — the same bargain publishing makes.
func TestMergeShowsTheCallBeforeMakingIt(t *testing.T) {
	m, asked := mergeModel(t, "merged", nil)
	m = pressRun(m, "M")
	if !m.merging {
		t.Fatalf("expected the merge prompt open, status %q", m.status)
	}
	if merges(*asked) != 0 {
		t.Fatalf("M merged something before being confirmed: %v", *asked)
	}
	body := stripANSI(m.Body(80, 18))
	for _, want := range []string{"Merge PR", "gh pr merge 7 --squash", "y MERGES IT"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the confirm screen is missing %q:\n%s", want, body)
		}
	}
}

// TestMergeNeedsAYes — and n leaves without one.
func TestMergeNeedsAYes(t *testing.T) {
	m, asked := mergeModel(t, "merged", nil)
	m = pressRun(m, "M")
	m = pressRun(m, "n")
	if m.merging {
		t.Error("n left the prompt open")
	}
	if merges(*asked) != 0 {
		t.Errorf("n merged it anyway: %v", *asked)
	}
	if m.status != "" {
		// Cancelling says nothing — the prompt disappearing is the message.
		t.Errorf("n reported %q", m.status)
	}
}

// TestMergeGoesThroughOnY, and reports what gh said.
func TestMergeGoesThroughOnY(t *testing.T) {
	m, asked := mergeModel(t, "Squashed and merged PR #7", nil)
	m = pressRun(m, "M")
	m = pressRun(m, "y")
	if merges(*asked) != 1 {
		t.Fatalf("expected exactly one merge, got %v", *asked)
	}
	if m.mergeBusy {
		t.Error("still busy after the merge answered")
	}
	if !strings.Contains(m.status, "Squashed and merged") {
		t.Errorf("the footer does not say what happened: %q", m.status)
	}
	if body := stripANSI(m.Body(80, 18)); !strings.Contains(body, "Squashed and merged PR #7") {
		t.Errorf("the report is missing gh's answer:\n%s", body)
	}
}

// TestAFailedMergeStaysOnScreen. gh's refusal names the condition — not up to
// date with base, a required check still running — over several lines, which one
// footer segment cannot carry and is the part worth reading.
func TestAFailedMergeStaysOnScreen(t *testing.T) {
	m, _ := mergeModel(t, "gh said:\nPull request is not mergeable", errors.New("exit 1"))
	m = pressRun(m, "M")
	m = pressRun(m, "y")
	if !m.merging {
		t.Fatal("a failure closed the prompt")
	}
	if !m.statusErr {
		t.Error("a failed merge does not read as a failure")
	}
	body := stripANSI(m.Body(80, 18))
	for _, want := range []string{"not mergeable", "failed:"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report is missing %q:\n%s", want, body)
		}
	}
}

// TestARefusalArrivesBeforeTheConfirmBox. The dry run is where a refusal that can
// be known in advance belongs — the reviewer finds out before confirming, and
// there is nothing left to say yes to.
func TestARefusalArrivesBeforeTheConfirmBox(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "one"))
	merged := false
	m.MergePR = func(dry bool) (string, error) {
		if dry {
			return "", errors.New("this workspace isn't linked to a PR")
		}
		merged = true
		return "", nil
	}
	m = pressRun(m, "M")
	if m.mergeStage != mergeReporting {
		t.Errorf("a refused plan still offers a confirm screen (stage %v)", m.mergeStage)
	}
	if body := stripANSI(m.Body(80, 18)); !strings.Contains(body, "isn't linked to a PR") {
		t.Errorf("the refusal is not on screen:\n%s", body)
	}
	// And enter on that screen closes it rather than merging: there is no plan
	// behind it to confirm.
	m = enter(m)
	if merged {
		t.Error("a refused plan was merged anyway")
	}
	if m.merging {
		t.Error("the refusal report would not close")
	}
}

// TestNoPRSaysSo rather than opening a box whose only outcome is an error. A nil
// seam covers both reasons — no PR, or a host that offers no merging — because
// from the keyboard they are one fact.
func TestNoPRSaysSo(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "one"))
	m = pressRun(m, "M")
	if m.merging {
		t.Fatal("M opened a prompt with no seam wired")
	}
	if !m.statusErr || !strings.Contains(m.status, "PR") {
		t.Errorf("status %q (err=%v), want it to name the missing PR", m.status, m.statusErr)
	}
}

// TestOneMergePerConfirmation. gh has already been asked by the time the report
// is up, and a second `y` on it would issue a second merge.
func TestOneMergePerConfirmation(t *testing.T) {
	m, asked := mergeModel(t, "merged", nil)
	m = pressRun(m, "M")
	m, cmd := pressKeyUI(m, "y")
	// A second y before the first has answered. The in-flight merge is what
	// refuses it, so nothing else has to remember to.
	m, again := pressKeyUI(m, "y")
	if again != nil {
		t.Error("a second y while merging issued another command")
	}
	if m = run(m, cmd); m.mergeBusy {
		t.Error("still busy after the merge answered")
	}
	if merges(*asked) != 1 {
		t.Fatalf("expected one merge, got %v", *asked)
	}
}

// TestTheMergePromptOwnsTheKeyboard. `q` and `esc` there mean "don't merge", and
// a host that took them first would close the whole view on someone who was
// declining to — leaving them unsure which of the two had happened.
func TestTheMergePromptOwnsTheKeyboard(t *testing.T) {
	m, _ := mergeModel(t, "merged", nil)
	if m.Filtering() {
		t.Fatal("the viewer claims the keyboard before M")
	}
	m = pressRun(m, "M")
	if !m.Filtering() {
		t.Error("the merge prompt does not claim the keyboard")
	}
}

package review

import "testing"

// Praise is a kind you can ask for by name, from the CLI and from an agent.
func TestPraiseIsAKindYouCanName(t *testing.T) {
	for _, in := range []string{"praise", "Praise", "  PRAISE  "} {
		if got := ParseKind(in); got != KindPraise {
			t.Errorf("ParseKind(%q) = %q, want %q", in, got, KindPraise)
		}
	}
}

// Kinds() is the compose box's tab cycle and the list every other surface reads,
// so a kind missing from it is a kind you cannot reach with the keyboard.
func TestEveryKindIsInTheCycle(t *testing.T) {
	want := map[Kind]bool{KindComment: true, KindSuggestion: true, KindQuestion: true, KindPraise: true}
	got := map[Kind]bool{}
	for _, k := range Kinds() {
		if got[k] {
			t.Errorf("%q appears twice in Kinds()", k)
		}
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%q is not in Kinds() — tab cannot reach it", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("Kinds() offers %q, which is not a kind this test knows about — add it here and to the surfaces that switch on a kind", k)
		}
	}
}

// tab reaches every kind and comes back. A cycle that skipped one would make it
// unreachable from the compose box even though ParseKind accepts it.
func TestTabCyclesThroughEveryKindAndWraps(t *testing.T) {
	all := Kinds()
	seen := map[Kind]bool{}
	k := KindComment
	for range all {
		seen[k] = true
		k = k.Next()
	}
	if k != KindComment {
		t.Errorf("cycling %d times landed on %q, want back at %q", len(all), k, KindComment)
	}
	for _, want := range all {
		if !seen[want] {
			t.Errorf("tab never reaches %q", want)
		}
	}
	// An empty kind is a record written before kinds existed; tab has to move it
	// somewhere real rather than sitting on nothing.
	if got := Kind("").Next(); got == "" {
		t.Error("tab on an unset kind went nowhere")
	}
}

// Praise is the one kind that asks for nothing, which is the whole reason it has
// a name — every other kind puts something on the author's list.
func TestOnlyPraiseAsksForNothing(t *testing.T) {
	for _, k := range Kinds() {
		want := k != KindPraise
		if got := k.WantsAction(); got != want {
			t.Errorf("%q: WantsAction() = %v, want %v", k, got, want)
		}
	}
	// An unset kind is a plain comment, which does want triage. Reading it as
	// praise would silently drop pre-kinds findings out of the badge.
	if !Kind("").WantsAction() {
		t.Error("a record written before kinds existed must still count as a finding")
	}
}

// The badge counts what the change owes its author. Nine compliments and one bug
// is one thing to fix; a badge reading 10 turns every compliment into a
// complaint, which is the opposite of what writing one was for.
func TestPraiseDoesNotCountAsAnOpenFinding(t *testing.T) {
	comments := []Comment{
		{ID: "1", State: Open, Kind: KindSuggestion},
		{ID: "2", State: Open, Kind: KindPraise},
		{ID: "3", State: Open, Kind: KindPraise},
		{ID: "4", State: Open}, // pre-kinds: a plain comment, and it counts
		{ID: "5", State: Open, Kind: KindQuestion},
	}
	if got := OpenCount(comments); got != 3 {
		t.Errorf("OpenCount = %d, want 3 (the suggestion, the question, and the unlabelled remark)", got)
	}
	// A review of nothing but praise owes the author nothing at all.
	if got := OpenCount([]Comment{{ID: "1", State: Open, Kind: KindPraise}}); got != 0 {
		t.Errorf("a review of pure praise counted %d open findings, want 0", got)
	}
}

// A published body names the kind, so praise arrives on GitHub saying what it is
// — otherwise a compliment reads as one more item in a list of complaints.
func TestPraiseIsLabelledOnGitHub(t *testing.T) {
	c := Comment{Author: AuthorHuman, Kind: KindPraise, Body: "this is a nice way to do it"}
	got := c.PublishBody()
	if got != "Praise: this is a nice way to do it" {
		t.Errorf("PublishBody() = %q, want it prefixed with the kind", got)
	}
}

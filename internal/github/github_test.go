package github

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	gotName string
	gotArgs []string
	out     string
	err     error
}

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	f.gotName = name
	f.gotArgs = args
	return f.out, f.err
}

func TestFetchPRParses(t *testing.T) {
	r := &fakeRunner{out: `{"number":12,"headRefName":"feat/x","baseRefName":"main","title":"t","body":"b","url":"u"}`}
	c := New(r)
	pr, err := c.FetchPR(12)
	if err != nil {
		t.Fatalf("FetchPR err: %v", err)
	}
	if pr.Number != 12 || pr.HeadRef != "feat/x" || pr.BaseRef != "main" ||
		pr.Title != "t" || pr.Body != "b" || pr.URL != "u" {
		t.Fatalf("parsed wrong: %+v", pr)
	}
	if r.gotName != "gh" {
		t.Fatalf("expected gh, got %q", r.gotName)
	}
	found := false
	for _, a := range r.gotArgs {
		if a == "12" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 12 in args, got %v", r.gotArgs)
	}
}

func TestFetchPRParsesStatusFields(t *testing.T) {
	r := &fakeRunner{out: `{"number":42,"headRefName":"saltor/foo","baseRefName":"main","title":"t","body":"b","url":"https://github.com/o/r/pull/42","state":"OPEN","isDraft":true,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"FAILURE","status":"COMPLETED"}],"mergeStateStatus":"DIRTY"}`}
	pr, err := New(r).FetchPR(42)
	if err != nil {
		t.Fatalf("FetchPR err: %v", err)
	}
	if pr.State != PRStateOpen || !pr.IsDraft || pr.ReviewDecision != ReviewApproved ||
		pr.CIState != CIFailing || pr.MergeStateStatus != MergeStateDirty {
		t.Errorf("status fields not parsed: %+v", pr)
	}
	// gh args must request the new fields.
	joined := ""
	for _, a := range r.gotArgs {
		joined += " " + a
	}
	for _, want := range []string{"state", "isDraft", "reviewDecision", "statusCheckRollup", "mergeStateStatus"} {
		if !contains(joined, want) {
			t.Errorf("expected %q in args, got %q", want, joined)
		}
	}
}

func TestPRStatusFromInfo(t *testing.T) {
	info := PRInfo{
		Number: 7, HeadRef: "feat/x", Title: "t", URL: "u",
		State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewApproved,
		CIState: CIPassing, MergeStateStatus: MergeStateClean,
	}
	got := PRStatusFromInfo(info)
	want := PRStatus{
		Number: 7, HeadRefName: "feat/x", Title: "t", URL: "u",
		State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewApproved,
		CIState: CIPassing, MergeStateStatus: MergeStateClean,
	}
	if got != want {
		t.Errorf("PRStatusFromInfo: got %+v want %+v", got, want)
	}
}

func TestFetchPRRunnerError(t *testing.T) {
	r := &fakeRunner{err: errors.New("boom"), out: "bad"}
	_, err := New(r).FetchPR(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchPRParseError(t *testing.T) {
	r := &fakeRunner{out: "not json"}
	_, err := New(r).FetchPR(1)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestListPRStatusParses(t *testing.T) {
	r := &fakeRunner{out: `[
		{"number":1,"headRefName":"andrew/a","title":"Fix the thing","url":"https://github.com/o/r/pull/1","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"CLEAN"},
		{"number":2,"headRefName":"andrew/b","url":"https://github.com/o/r/pull/2","state":"MERGED","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[],"mergeStateStatus":"CLEAN"},
		{"number":3,"headRefName":"andrew/c","url":"https://github.com/o/r/pull/3","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[{"status":"IN_PROGRESS"},{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"BEHIND"},
		{"number":4,"headRefName":"andrew/d","url":"https://github.com/o/r/pull/4","state":"OPEN","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","statusCheckRollup":[{"conclusion":"FAILURE","status":"COMPLETED"}],"mergeStateStatus":"DIRTY"},
		{"number":5,"headRefName":"andrew/e","url":"https://github.com/o/r/pull/5","state":"CLOSED","isDraft":false,"reviewDecision":"","statusCheckRollup":[{"state":"PENDING"}],"mergeStateStatus":"UNKNOWN"}
	]`}
	c := New(r)
	got, err := c.ListPRStatus("/tmp/repo")
	if err != nil {
		t.Fatalf("ListPRStatus err: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 PRs, got %d", len(got))
	}
	want := []PRStatus{
		{Number: 1, HeadRefName: "andrew/a", Title: "Fix the thing", URL: "https://github.com/o/r/pull/1", State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewApproved, CIState: CIPassing, MergeStateStatus: MergeStateClean},
		{Number: 2, HeadRefName: "andrew/b", URL: "https://github.com/o/r/pull/2", State: PRStateMerged, IsDraft: false, ReviewDecision: ReviewApproved, CIState: CINone, MergeStateStatus: MergeStateClean},
		{Number: 3, HeadRefName: "andrew/c", URL: "https://github.com/o/r/pull/3", State: PRStateOpen, IsDraft: true, ReviewDecision: "", CIState: CIPending, MergeStateStatus: MergeStateBehind},
		{Number: 4, HeadRefName: "andrew/d", URL: "https://github.com/o/r/pull/4", State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewRequired, CIState: CIFailing, MergeStateStatus: MergeStateDirty},
		{Number: 5, HeadRefName: "andrew/e", URL: "https://github.com/o/r/pull/5", State: PRStateClosed, IsDraft: false, ReviewDecision: "", CIState: CIPending, MergeStateStatus: MergeStateUnknown},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("row %d: got %+v want %+v", i, got[i], w)
		}
	}
	// gh invocation: must include --state all and the rich --json field list.
	if r.gotName != "gh" {
		t.Fatalf("expected gh, got %q", r.gotName)
	}
	joined := ""
	for _, a := range r.gotArgs {
		joined += " " + a
	}
	for _, want := range []string{"--state", "all", "title", "url", "reviewDecision", "statusCheckRollup", "mergeStateStatus"} {
		if !contains(joined, want) {
			t.Errorf("expected %q in args, got %q", want, joined)
		}
	}
}

func TestListPRStatusRunnerError(t *testing.T) {
	r := &fakeRunner{err: errors.New("boom"), out: "bad"}
	if _, err := New(r).ListPRStatus(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestListPRStatusParseError(t *testing.T) {
	r := &fakeRunner{out: "not json"}
	if _, err := New(r).ListPRStatus(""); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestGetPRStatusParses(t *testing.T) {
	r := &fakeRunner{out: `{"number":1717,"headRefName":"old/branch","title":"Old PR","url":"https://github.com/o/r/pull/1717","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"BEHIND"}`}
	got, err := New(r).GetPRStatus("/tmp/repo", 1717)
	if err != nil {
		t.Fatalf("GetPRStatus err: %v", err)
	}
	want := PRStatus{
		Number: 1717, HeadRefName: "old/branch", Title: "Old PR",
		URL: "https://github.com/o/r/pull/1717", State: PRStateOpen,
		ReviewDecision: ReviewApproved, CIState: CIPassing, MergeStateStatus: MergeStateBehind,
	}
	if got != want {
		t.Errorf("GetPRStatus: got %+v want %+v", got, want)
	}
	// gh invocation: gh pr view 1717 --json ...
	if r.gotName != "gh" || len(r.gotArgs) < 3 || r.gotArgs[0] != "pr" || r.gotArgs[1] != "view" || r.gotArgs[2] != "1717" {
		t.Errorf("expected `gh pr view 1717 ...`, got %s %v", r.gotName, r.gotArgs)
	}
}

func TestGetPRStatusRejectsNonPositive(t *testing.T) {
	if _, err := New(&fakeRunner{}).GetPRStatus("/tmp/repo", 0); err == nil {
		t.Fatal("expected error for pr=0")
	}
	if _, err := New(&fakeRunner{}).GetPRStatus("/tmp/repo", -1); err == nil {
		t.Fatal("expected error for pr=-1")
	}
}

func TestGetPRStatusRunnerError(t *testing.T) {
	r := &fakeRunner{err: errors.New("boom"), out: "bad"}
	if _, err := New(r).GetPRStatus("/tmp/repo", 1); err == nil {
		t.Fatal("expected error")
	}
}

// scriptRunner returns a sequence of (out, err) pairs in order of Run
// invocation. Used when a single client method shells out multiple times
// (e.g. ListMergeQueuedHeads → `gh repo view` then `gh api graphql`).
type scriptRunner struct {
	calls  [][]string
	steps  []scriptStep
	cursor int
}

type scriptStep struct {
	out string
	err error
}

func (s *scriptRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	full := append([]string{name}, args...)
	s.calls = append(s.calls, full)
	if s.cursor >= len(s.steps) {
		return "", errors.New("scriptRunner: no more scripted responses")
	}
	step := s.steps[s.cursor]
	s.cursor++
	return step.out, step.err
}

func TestListMergeQueuedHeadsParses(t *testing.T) {
	repoView := `{"owner":{"login":"acme"},"name":"widgets"}`
	graphql := `{
		"data": {
			"repository": {
				"pullRequests": {
					"nodes": [
						{"headRefName": "andrew/a", "isInMergeQueue": true},
						{"headRefName": "andrew/b", "isInMergeQueue": false},
						{"headRefName": "andrew/c", "isInMergeQueue": true}
					]
				}
			}
		}
	}`
	s := &scriptRunner{steps: []scriptStep{
		{out: repoView},
		{out: graphql},
	}}
	got, err := New(s).ListMergeQueuedHeads("/tmp/repo")
	if err != nil {
		t.Fatalf("ListMergeQueuedHeads err: %v", err)
	}
	want := map[string]bool{"andrew/a": true, "andrew/c": true}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("expected %q in queued set, got %v", k, got)
		}
	}
	if len(s.calls) != 2 {
		t.Fatalf("expected 2 gh invocations, got %d: %v", len(s.calls), s.calls)
	}
	// First call: gh repo view --json owner,name
	if s.calls[0][0] != "gh" || s.calls[0][1] != "repo" || s.calls[0][2] != "view" {
		t.Errorf("first call shape: %v", s.calls[0])
	}
	// Second call: gh api graphql with owner/name vars and a query containing isInMergeQueue.
	joined := ""
	for _, a := range s.calls[1] {
		joined += " " + a
	}
	for _, want := range []string{"api", "graphql", "owner=acme", "name=widgets", "isInMergeQueue"} {
		if !contains(joined, want) {
			t.Errorf("expected %q in graphql call args, got %q", want, joined)
		}
	}
}

func TestListMergeQueuedHeadsRepoViewError(t *testing.T) {
	s := &scriptRunner{steps: []scriptStep{
		{err: errors.New("boom"), out: "bad"},
	}}
	if _, err := New(s).ListMergeQueuedHeads(""); err == nil {
		t.Fatal("expected error when gh repo view fails")
	}
}

func TestListMergeQueuedHeadsGraphqlError(t *testing.T) {
	s := &scriptRunner{steps: []scriptStep{
		{out: `{"owner":{"login":"a"},"name":"b"}`},
		{err: errors.New("boom"), out: "bad"},
	}}
	if _, err := New(s).ListMergeQueuedHeads(""); err == nil {
		t.Fatal("expected error when gh api graphql fails")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

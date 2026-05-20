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
		{"number":1,"headRefName":"andrew/a","url":"https://github.com/o/r/pull/1","state":"OPEN","isDraft":false,"isInMergeQueue":true,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"CLEAN"},
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
		{Number: 1, HeadRefName: "andrew/a", URL: "https://github.com/o/r/pull/1", State: PRStateOpen, IsDraft: false, IsInMergeQueue: true, ReviewDecision: ReviewApproved, CIState: CIPassing, MergeStateStatus: MergeStateClean},
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
	for _, want := range []string{"--state", "all", "url", "reviewDecision", "statusCheckRollup", "mergeStateStatus", "isInMergeQueue"} {
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

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

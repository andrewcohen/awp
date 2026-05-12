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
		{"number":1,"headRefName":"andrew/a","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}]},
		{"number":2,"headRefName":"andrew/b","state":"MERGED","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[]},
		{"number":3,"headRefName":"andrew/c","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[{"status":"IN_PROGRESS"},{"conclusion":"SUCCESS","status":"COMPLETED"}]},
		{"number":4,"headRefName":"andrew/d","state":"OPEN","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","statusCheckRollup":[{"conclusion":"FAILURE","status":"COMPLETED"}]},
		{"number":5,"headRefName":"andrew/e","state":"CLOSED","isDraft":false,"reviewDecision":"","statusCheckRollup":[{"state":"PENDING"}]}
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
		{Number: 1, HeadRefName: "andrew/a", State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewApproved, CIState: CIPassing},
		{Number: 2, HeadRefName: "andrew/b", State: PRStateMerged, IsDraft: false, ReviewDecision: ReviewApproved, CIState: CINone},
		{Number: 3, HeadRefName: "andrew/c", State: PRStateOpen, IsDraft: true, ReviewDecision: "", CIState: CIPending},
		{Number: 4, HeadRefName: "andrew/d", State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewRequired, CIState: CIFailing},
		{Number: 5, HeadRefName: "andrew/e", State: PRStateClosed, IsDraft: false, ReviewDecision: "", CIState: CIPending},
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
	for _, want := range []string{"--state", "all", "reviewDecision", "statusCheckRollup"} {
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

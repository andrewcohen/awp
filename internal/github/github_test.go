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

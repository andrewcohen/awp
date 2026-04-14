package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/workspace"
)

func TestParsePRNumber(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"123", 123, false},
		{"#456", 456, false},
		{"  789  ", 789, false},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := parsePRNumber(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePRNumber(%q) expected err", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePRNumber(%q) err: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parsePRNumber(%q)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	pr := github.PRInfo{Number: 42, Title: "add thing", Body: "does X"}
	got := buildReviewPrompt(pr, "main")
	if !strings.Contains(got, "PR #42") || !strings.Contains(got, "add thing") ||
		!strings.Contains(got, "does X") || !strings.Contains(got, "main..@") {
		t.Fatalf("unexpected prompt: %q", got)
	}

	empty := github.PRInfo{Number: 1, Title: "t", Body: "  "}
	got = buildReviewPrompt(empty, "develop")
	if !strings.Contains(got, "(no description)") {
		t.Fatalf("expected placeholder for empty body, got %q", got)
	}
}

func TestRunReviewDispatches(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})

	var gotPR int
	app.review = func(_ Runner, s workspace.Service, pr int, _ io.Reader, _ io.Writer) error {
		gotPR = pr
		if s != svc {
			t.Fatalf("review got wrong svc")
		}
		return nil
	}
	if err := app.Run([]string{"review", "321"}); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if gotPR != 321 {
		t.Fatalf("expected pr=321 got %d", gotPR)
	}
}

func TestRunReviewArgValidation(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	app.review = func(Runner, workspace.Service, int, io.Reader, io.Writer) error { return nil }

	if err := app.Run([]string{"review"}); err == nil {
		t.Fatal("expected error for missing PR arg")
	}
	if err := app.Run([]string{"review", "1", "2"}); err == nil {
		t.Fatal("expected error for extra args")
	}
	if err := app.Run([]string{"review", "notanumber"}); err == nil {
		t.Fatal("expected error for invalid PR arg")
	}
}

func TestRunReviewNoArgUsesPicker(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})

	app.runner = &prListRunner{
		out: `[{"number":7,"title":"fix","headRefName":"andrew/fix","author":{"login":"ac"},"isDraft":false}]`,
	}
	var pickedTitle string
	var pickedOptions []string
	app.picker = func(title string, options []string) (string, error) {
		pickedTitle = title
		pickedOptions = options
		return options[0], nil
	}
	var gotPR int
	app.review = func(_ Runner, _ workspace.Service, pr int, _ io.Reader, _ io.Writer) error {
		gotPR = pr
		return nil
	}

	if err := app.Run([]string{"review"}); err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if gotPR != 7 {
		t.Fatalf("expected pr=7 got %d", gotPR)
	}
	if pickedTitle == "" || len(pickedOptions) != 1 {
		t.Fatalf("picker not invoked properly: %q %v", pickedTitle, pickedOptions)
	}
	if !strings.Contains(pickedOptions[0], "#7") {
		t.Fatalf("label missing PR number: %q", pickedOptions[0])
	}
}

type prListRunner struct {
	out string
	err error
}

func (r *prListRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	if name == "gh" && len(args) > 0 && args[0] == "pr" && args[1] == "list" {
		return r.out, r.err
	}
	return "", nil
}

func TestShellSingleQuote(t *testing.T) {
	if got := shellSingleQuote("main"); got != "'main'" {
		t.Fatalf("got %q", got)
	}
	if got := shellSingleQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("got %q", got)
	}
}

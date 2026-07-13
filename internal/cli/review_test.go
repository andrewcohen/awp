package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/config"
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
	pr := github.PRInfo{
		Number: 42,
		Title:  "add thing",
		Body:   "does X",
		URL:    "https://github.com/acme/widget/pull/42",
	}
	got := buildReviewPrompt(
		pr,
		"main",
		"abc123..def456",
		"gh:acme/widget/pr/42",
		"/Users/x/Library/Application Support/tuicr/reviews/sessions/abcd.json",
		"/Users/x/Library/Application Support/tuicr",
		[]github.PRComment{
			{Author: "octocat", Kind: "inline", Path: "internal/foo/bar.go", Line: 42, Body: "nil deref here\nsecond line"},
			{Author: "hubot", Kind: "review", Body: "LGTM overall"},
			{Author: "carol", Kind: "comment", Body: "needs a test"},
		},
		[]priorSession{
			{Path: "/data/reviews/sessions/old.json", HeadSHA: "16d77d5f2c1401bb6f9530d2305df8570d6bc3d1", Comments: 4, Updated: "2026-07-09T17:40:48Z"},
		},
	)
	if !strings.Contains(got, "PR #42") || !strings.Contains(got, "add thing") ||
		!strings.Contains(got, "does X") || !strings.Contains(got, "abc123..def456") {
		t.Fatalf("unexpected prompt header: %q", got)
	}
	// Load-bearing guidance lines — assert each so a future trim doesn't
	// silently delete the bits that fix real reported failures (session
	// resolution via abs path, --username rationale, type taxonomy,
	// volume target, no-test-ping rule, closing summary).
	mustContain := []string{
		"/Users/x/Library/Application Support/tuicr/reviews/sessions/abcd.json",
		"gh:acme/widget/pr/42",
		// Session resolution / recovery now points at the forge-aware
		// `tuicr review list` rather than the old jq index.json hack, and
		// uses the PR's owner/repo coordinate.
		"tuicr review list --repo acme/widget",
		"tuicr review list --all",
		`--username "awp-agent"`,
		"3-8 comments",
		"Do not send a test ping",
		"Closing summary",
		"Report back in chat",
		"a concrete failure mode you can name",
		// Existing-comments section: each comment rendered, multi-line
		// body collapsed to one line, and the non-redundancy guidance.
		"inline @octocat [internal/foo/bar.go:42]: nil deref here / second line",
		"review @hubot: LGTM overall",
		"comment @carol: needs a test",
		"Do not restate",
		// Prior-head carry-forward: section heading, the rendered session
		// line (path + short head + count), and the migration guidance.
		"Carrying forward comments from a prior head",
		"/data/reviews/sessions/old.json — head 16d77d5f, 4 comments",
		"Re-anchor each comment",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing guidance line %q", want)
		}
	}

	empty := github.PRInfo{Number: 1, Title: "t", Body: "  "}
	got = buildReviewPrompt(empty, "develop", "develop..@", "", "", "", nil, nil)
	if !strings.Contains(got, "(no description)") {
		t.Fatalf("expected placeholder for empty body, got %q", got)
	}
	// Empty session path triggers the recovery prose, not a literal
	// empty string in the template.
	if !strings.Contains(got, "not resolved") {
		t.Fatalf("expected recovery prose for empty session path, got %q", got)
	}
	// No comments → sentinel line, not an empty section.
	if !strings.Contains(got, "(none — no prior comments on this PR)") {
		t.Fatalf("expected sentinel for empty comments, got %q", got)
	}
	// No prior sessions → carry-forward sentinel, section stays inert.
	if !strings.Contains(got, "(none — no prior-head draft comments to carry forward)") {
		t.Fatalf("expected sentinel for empty prior sessions, got %q", got)
	}
}

func TestFormatPriorSessions(t *testing.T) {
	if got := formatPriorSessions(nil); got != "(none — no prior-head draft comments to carry forward)" {
		t.Errorf("empty: got %q", got)
	}
	got := formatPriorSessions([]priorSession{
		{Path: "/s/a.json", HeadSHA: "abcdef0123456789", Comments: 1, Updated: "2026-07-01T10:00:00Z"},
		{Path: "/s/b.json", HeadSHA: "short", Comments: 3},
	})
	// Singular "comment" for count 1, plural for >1; head truncated to 8;
	// updated appended only when present.
	if !strings.Contains(got, "/s/a.json — head abcdef01, 1 comment, updated 2026-07-01T10:00:00Z") {
		t.Errorf("singular/updated line wrong: %q", got)
	}
	if !strings.Contains(got, "/s/b.json — head short, 3 comments") || strings.Contains(got, "head short, 3 comments, updated") {
		t.Errorf("plural/no-updated line wrong: %q", got)
	}
}

func TestReviewPromptFileAndPointer(t *testing.T) {
	// The prompt is written under ~/.awp/review-prompts/<repo>/<ws>.md, not
	// inside the workspace tree (that dir is symlinked to the shared source
	// .awp during prep). Pin HOME so the test writes into a temp dir.
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoRoot := "/src/example-repo"
	wsName := "pr-7-feature"
	instructions := "Please review PR #7: a thing\n\nlong instructions ...\n"
	path, err := writeReviewPromptFile(repoRoot, wsName, instructions)
	if err != nil {
		t.Fatalf("writeReviewPromptFile: %v", err)
	}
	if want := config.ReviewPromptPath(repoRoot, wsName); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if !strings.HasPrefix(path, filepath.Join(home, ".awp", "review-prompts")) {
		t.Errorf("path %q not under ~/.awp/review-prompts", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != instructions {
		t.Errorf("file content = %q, want %q", got, instructions)
	}

	// Same workspace name in a different repo must not collide.
	otherPath, err := writeReviewPromptFile("/src/other-repo", wsName, "other\n")
	if err != nil {
		t.Fatalf("writeReviewPromptFile (other repo): %v", err)
	}
	if otherPath == path {
		t.Errorf("cross-repo prompts collided at %q", path)
	}

	pr := github.PRInfo{Number: 7, Title: "a thing"}
	pointer := buildReviewPointerPrompt(pr, path)
	// The pointer must be short and must name the PR plus the file path,
	// so the agent knows what to read.
	if !strings.Contains(pointer, "PR #7") || !strings.Contains(pointer, "a thing") {
		t.Errorf("pointer missing PR header: %q", pointer)
	}
	if !strings.Contains(pointer, path) {
		t.Errorf("pointer missing prompt path %q: %q", path, pointer)
	}
	if lines := strings.Count(pointer, "\n"); lines > 12 {
		t.Errorf("pointer prompt is not tiny (%d lines): %q", lines, pointer)
	}
}

func TestResolveDiffRange(t *testing.T) {
	cases := []struct {
		name      string
		runner    Runner
		wsPath    string
		baseRef   string
		headSHA   string
		want      string
		describe  string
		precision string
	}{
		{
			name:    "happy path uses merge-base against origin/<base>",
			wsPath:  "/some/ws",
			baseRef: "main",
			headSHA: "791d740",
			runner: scriptedRunner{out: map[string]string{
				"git merge-base origin/main 791d740": "abc123\n",
			}},
			want: "abc123..791d740",
		},
		{
			name:    "falls back to bare ref when origin/<base> missing",
			wsPath:  "/some/ws",
			baseRef: "main",
			headSHA: "791d740",
			runner: scriptedRunner{
				out: map[string]string{
					"git merge-base main 791d740": "abc999\n",
				},
				errs: map[string]bool{
					"git merge-base origin/main 791d740": true,
				},
			},
			want: "abc999..791d740",
		},
		{
			name:    "falls back to ref-name range when all git calls error",
			wsPath:  "/some/ws",
			baseRef: "main",
			headSHA: "791d740",
			runner: scriptedRunner{errs: map[string]bool{
				"git merge-base origin/main 791d740": true,
				"git merge-base main 791d740":        true,
			}},
			want: "main..@",
		},
		{
			name:    "empty headSHA falls back without calling git",
			wsPath:  "/some/ws",
			baseRef: "main",
			headSHA: "",
			runner:  scriptedRunner{},
			want:    "main..@",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveDiffRange(c.runner, c.wsPath, c.baseRef, c.headSHA)
			if got != c.want {
				t.Errorf("resolveDiffRange = %q, want %q", got, c.want)
			}
		})
	}
}

// scriptedRunner answers Run by joining the command into a key and
// looking it up in `out` (or returning an error if listed in `errs`).
// Used by TestResolveDiffRange — full Runner contract is overkill here.
type scriptedRunner struct {
	out  map[string]string
	errs map[string]bool
}

func (r scriptedRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	if r.errs[key] {
		return "", &runnerErr{key: key}
	}
	return r.out[key], nil
}

type runnerErr struct{ key string }

func (e *runnerErr) Error() string { return "scripted err: " + e.key }

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

func TestRewriteFetchURL(t *testing.T) {
	const fallback = "https://github.com/ForkOwner/awp.git"
	cases := []struct {
		name   string
		origin string
		want   string
	}{
		// SSH URL form — the one that bit us with private forks when
		// we used https://github.com/... and got prompted for a
		// username. Must preserve ssh:// + user so SSH-key auth works.
		{
			name:   "ssh URL form",
			origin: "ssh://git@github.com/andrewcohen/awp",
			want:   "ssh://git@github.com/ForkOwner/awp.git",
		},
		{
			name:   "ssh URL form with .git suffix",
			origin: "ssh://git@github.com/andrewcohen/awp.git",
			want:   "ssh://git@github.com/ForkOwner/awp.git",
		},
		{
			name:   "https URL form",
			origin: "https://github.com/andrewcohen/awp.git",
			want:   "https://github.com/ForkOwner/awp.git",
		},
		// SCP form: the most common shape (`git clone git@host:owner/repo`).
		// net/url can't parse this, so we detect it by the `<user>@<host>:`
		// pattern and rebuild manually.
		{
			name:   "scp form",
			origin: "git@github.com:andrewcohen/awp.git",
			want:   "git@github.com:ForkOwner/awp.git",
		},
		// Enterprise host stays preserved.
		{
			name:   "ssh URL on enterprise host",
			origin: "ssh://git@ghe.example.com/team/repo",
			want:   "ssh://git@ghe.example.com/ForkOwner/awp.git",
		},
		// Malformed input falls back to the safe default.
		{
			name:   "empty origin falls back",
			origin: "",
			want:   fallback,
		},
		{
			name:   "garbage origin falls back",
			origin: "not a url at all",
			want:   fallback,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteFetchURL(c.origin, "ForkOwner", "awp", fallback)
			if got != c.want {
				t.Errorf("rewriteFetchURL(%q, ForkOwner, awp) = %q, want %q", c.origin, got, c.want)
			}
		})
	}
}

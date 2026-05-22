package forge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	// Map of "name arg0 arg1 ..." → response.
	responses map[string]fakeResponse
	// Last call captured for assertions.
	lastName string
	lastArgs []string
}

type fakeResponse struct {
	out string
	err error
}

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	f.lastName = name
	f.lastArgs = args
	key := name
	for _, a := range args {
		key += " " + a
	}
	if r, ok := f.responses[key]; ok {
		return r.out, r.err
	}
	// Match prefix: tests often only care about "git config --get remote.origin.url".
	for k, r := range f.responses {
		if strings.HasPrefix(key, k) {
			return r.out, r.err
		}
	}
	return "", nil
}

func TestParseHost(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"git@github.com:foo/bar.git", "github.com", false},
		{"git@gitlab.example.com:foo/bar.git", "gitlab.example.com", false},
		{"https://github.com/foo/bar.git", "github.com", false},
		{"https://gitlab.com/foo/bar.git", "gitlab.com", false},
		{"ssh://git@gitlab.example.com:2222/foo/bar.git", "gitlab.example.com", false},
		{"", "", true},
		{"not-a-url", "", true},
	}
	for _, c := range cases {
		got, err := parseHost(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseHost(%q) expected error", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHost(%q) err: %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("parseHost(%q)=%q want %q", c.raw, got, c.want)
		}
	}
}

func TestDetectGitHub(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"git remote get-url origin": {out: "git@github.com:foo/bar.git\n"},
	}}
	f, err := Detect(r, "")
	if err != nil {
		t.Fatalf("Detect err: %v", err)
	}
	if f.Name() != "github" {
		t.Fatalf("expected github forge, got %q", f.Name())
	}
}

func TestDetectGitLab(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"git remote get-url origin": {out: "https://gitlab.example.com/foo/bar.git\n"},
	}}
	f, err := Detect(r, "")
	if err != nil {
		t.Fatalf("Detect err: %v", err)
	}
	if f.Name() != "gitlab" {
		t.Fatalf("expected gitlab forge, got %q", f.Name())
	}
}

func TestDetectRemoteError(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"git remote get-url origin": {err: errors.New("not a repo"), out: ""},
	}}
	if _, err := Detect(r, ""); err == nil {
		t.Fatal("expected error when remote URL lookup fails")
	}
}

func TestDetectUnsupportedHostErrors(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"git remote get-url origin": {out: "git@bitbucket.org:foo/bar.git\n"},
	}}
	_, err := Detect(r, "")
	if err == nil {
		t.Fatal("expected error for unsupported host")
	}
	if !strings.Contains(err.Error(), "bitbucket.org") {
		t.Errorf("error should mention the host, got %q", err.Error())
	}
}

func TestDetectOverrideShortCircuitsRemoteLookup(t *testing.T) {
	// Remote lookup is rigged to fail; override should win without
	// calling it. Simulates self-hosted GitLab on a non-"gitlab" host.
	r := &fakeRunner{responses: map[string]fakeResponse{
		"git remote get-url origin": {err: errors.New("should not be called"), out: "bad"},
	}}
	f, err := Detect(r, "gitlab")
	if err != nil {
		t.Fatalf("Detect err: %v", err)
	}
	if f.Name() != "gitlab" {
		t.Fatalf("expected gitlab forge, got %q", f.Name())
	}
}

func TestDetectInvalidOverride(t *testing.T) {
	r := &fakeRunner{}
	if _, err := Detect(r, "bitbucket"); err == nil {
		t.Fatal("expected error for unknown override")
	}
}

func TestGitHubFetchPR(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"gh pr view 12": {out: `{"number":12,"headRefName":"feat/x","baseRefName":"main","title":"t","body":"b","url":"u"}`},
	}}
	f := &GitHub{runner: r}
	pr, err := f.FetchPR(12)
	if err != nil {
		t.Fatalf("FetchPR err: %v", err)
	}
	if pr.Number != 12 || pr.HeadRef != "feat/x" || pr.BaseRef != "main" ||
		pr.Title != "t" || pr.Body != "b" || pr.URL != "u" {
		t.Fatalf("parsed wrong: %+v", pr)
	}
	if r.lastName != "gh" {
		t.Fatalf("expected gh, got %q", r.lastName)
	}
}

func TestGitHubFetchPRRunnerError(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"gh pr view 1": {err: errors.New("boom"), out: "bad"},
	}}
	if _, err := (&GitHub{runner: r}).FetchPR(1); err == nil {
		t.Fatal("expected error")
	}
}

func TestGitHubFetchPRParseError(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"gh pr view 1": {out: "not json"},
	}}
	if _, err := (&GitHub{runner: r}).FetchPR(1); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestGitHubListPRs(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"gh pr list": {out: `[{"number":7,"title":"fix","headRefName":"andrew/fix","author":{"login":"ac"},"isDraft":false}]`},
	}}
	prs, err := (&GitHub{runner: r}).ListPRs()
	if err != nil {
		t.Fatalf("ListPRs err: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 7 || prs[0].Author.Login != "ac" {
		t.Fatalf("unexpected: %+v", prs)
	}
}

func TestGitLabFetchPR(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"glab mr view 5": {out: `{"iid":5,"title":"add x","description":"why","source_branch":"feat/x","target_branch":"main","web_url":"https://gitlab.com/foo/bar/-/merge_requests/5"}`},
	}}
	pr, err := (&GitLab{runner: r}).FetchPR(5)
	if err != nil {
		t.Fatalf("FetchPR err: %v", err)
	}
	if pr.Number != 5 || pr.HeadRef != "feat/x" || pr.BaseRef != "main" ||
		pr.Title != "add x" || pr.Body != "why" ||
		pr.URL != "https://gitlab.com/foo/bar/-/merge_requests/5" {
		t.Fatalf("parsed wrong: %+v", pr)
	}
}

func TestGitLabListPRs(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"glab mr list": {out: `[
			{"iid":3,"title":"wip thing","source_branch":"andrew/x","author":{"username":"andrew"},"draft":true,"work_in_progress":false},
			{"iid":4,"title":"done thing","source_branch":"andrew/y","author":{"username":"andrew"},"draft":false,"work_in_progress":false}
		]`},
	}}
	prs, err := (&GitLab{runner: r}).ListPRs()
	if err != nil {
		t.Fatalf("ListPRs err: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("want 2 prs, got %d", len(prs))
	}
	if prs[0].Number != 3 || !prs[0].IsDraft || prs[0].Author.Login != "andrew" {
		t.Fatalf("first pr wrong: %+v", prs[0])
	}
	if prs[1].Number != 4 || prs[1].IsDraft {
		t.Fatalf("second pr wrong: %+v", prs[1])
	}
}

func TestForgeCommandsContainExpectedCLI(t *testing.T) {
	gh := &GitHub{}
	gl := &GitLab{}

	if !strings.Contains(gh.PRDescriptionCommand(9), "gh pr view 9") {
		t.Errorf("github PRDescriptionCommand missing expected substring: %q", gh.PRDescriptionCommand(9))
	}
	if !strings.Contains(gl.PRDescriptionCommand(9), "glab mr view 9") {
		t.Errorf("gitlab PRDescriptionCommand missing expected substring: %q", gl.PRDescriptionCommand(9))
	}
	if !strings.Contains(gh.CIWatchCommand(), "gh run watch") {
		t.Errorf("github CIWatchCommand missing gh run watch: %q", gh.CIWatchCommand())
	}
	if !strings.Contains(gl.CIWatchCommand(), "glab ci view") {
		t.Errorf("gitlab CIWatchCommand missing glab ci view: %q", gl.CIWatchCommand())
	}
}

func TestGitHubListPRStatus(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"gh pr list": {out: `[
			{"number":1,"headRefName":"andrew/a","url":"https://github.com/o/r/pull/1","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"CLEAN"},
			{"number":2,"headRefName":"andrew/b","url":"https://github.com/o/r/pull/2","state":"MERGED","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[],"mergeStateStatus":"CLEAN"},
			{"number":3,"headRefName":"andrew/c","url":"https://github.com/o/r/pull/3","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[{"status":"IN_PROGRESS"},{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"BEHIND"},
			{"number":4,"headRefName":"andrew/d","url":"https://github.com/o/r/pull/4","state":"OPEN","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","statusCheckRollup":[{"conclusion":"FAILURE","status":"COMPLETED"}],"mergeStateStatus":"DIRTY"},
			{"number":5,"headRefName":"andrew/e","url":"https://github.com/o/r/pull/5","state":"CLOSED","isDraft":false,"reviewDecision":"","statusCheckRollup":[{"state":"PENDING"}],"mergeStateStatus":"UNKNOWN"}
		]`},
	}}
	got, err := (&GitHub{runner: r}).ListPRStatus("/tmp/repo")
	if err != nil {
		t.Fatalf("ListPRStatus err: %v", err)
	}
	want := []PRStatus{
		{Number: 1, HeadRefName: "andrew/a", URL: "https://github.com/o/r/pull/1", State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewApproved, CIState: CIPassing, MergeStateStatus: MergeStateClean},
		{Number: 2, HeadRefName: "andrew/b", URL: "https://github.com/o/r/pull/2", State: PRStateMerged, IsDraft: false, ReviewDecision: ReviewApproved, CIState: CINone, MergeStateStatus: MergeStateClean},
		{Number: 3, HeadRefName: "andrew/c", URL: "https://github.com/o/r/pull/3", State: PRStateOpen, IsDraft: true, ReviewDecision: "", CIState: CIPending, MergeStateStatus: MergeStateBehind},
		{Number: 4, HeadRefName: "andrew/d", URL: "https://github.com/o/r/pull/4", State: PRStateOpen, IsDraft: false, ReviewDecision: ReviewRequired, CIState: CIFailing, MergeStateStatus: MergeStateDirty},
		{Number: 5, HeadRefName: "andrew/e", URL: "https://github.com/o/r/pull/5", State: PRStateClosed, IsDraft: false, ReviewDecision: "", CIState: CIPending, MergeStateStatus: MergeStateUnknown},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("row %d: got %+v want %+v", i, got[i], w)
		}
	}
}

func TestGitHubListPRStatusRunnerError(t *testing.T) {
	r := &fakeRunner{responses: map[string]fakeResponse{
		"gh pr list": {err: errors.New("boom"), out: "bad"},
	}}
	if _, err := (&GitHub{runner: r}).ListPRStatus(""); err == nil {
		t.Fatal("expected error")
	}
}

// scriptRunner returns scripted (out, err) pairs in order — used by
// ListMergeQueuedHeads which shells out twice per call (`gh repo view` then
// `gh api graphql`).
type scriptRunner struct {
	calls  [][]string
	steps  []fakeResponse
	cursor int
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

func TestGitHubListMergeQueuedHeads(t *testing.T) {
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
	s := &scriptRunner{steps: []fakeResponse{{out: repoView}, {out: graphql}}}
	got, err := (&GitHub{runner: s}).ListMergeQueuedHeads("/tmp/repo")
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
}

func TestGitHubListMergeQueuedHeadsRepoViewError(t *testing.T) {
	s := &scriptRunner{steps: []fakeResponse{{err: errors.New("boom"), out: "bad"}}}
	if _, err := (&GitHub{runner: s}).ListMergeQueuedHeads(""); err == nil {
		t.Fatal("expected error when gh repo view fails")
	}
}

func TestGitHubListMergeQueuedHeadsGraphqlError(t *testing.T) {
	s := &scriptRunner{steps: []fakeResponse{
		{out: `{"owner":{"login":"a"},"name":"b"}`},
		{err: errors.New("boom"), out: "bad"},
	}}
	if _, err := (&GitHub{runner: s}).ListMergeQueuedHeads(""); err == nil {
		t.Fatal("expected error when gh api graphql fails")
	}
}

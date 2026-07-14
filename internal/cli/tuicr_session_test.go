package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTuicrSessionSlug(t *testing.T) {
	cases := []struct {
		name string
		url  string
		num  int
		want string
	}{
		{
			name: "https github PR url",
			url:  "https://github.com/Fast-Growing-Trees-LLC/grove/pull/430",
			num:  430,
			want: "gh:Fast-Growing-Trees-LLC/grove/pr/430",
		},
		{
			name: "trailing slash tolerated",
			url:  "https://github.com/acme/widget/pull/1/",
			num:  1,
			want: "gh:acme/widget/pr/1",
		},
		{
			name: "non-pr path returns empty",
			url:  "https://github.com/acme/widget/issues/1",
			num:  1,
			want: "",
		},
		{
			name: "empty url returns empty",
			url:  "",
			num:  1,
			want: "",
		},
		{
			name: "negative pr number returns empty",
			url:  "https://github.com/acme/widget/pull/1",
			num:  0,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tuicrSessionSlug(c.url, c.num)
			if got != c.want {
				t.Errorf("tuicrSessionSlug(%q, %d) = %q, want %q", c.url, c.num, got, c.want)
			}
		})
	}
}

func TestResolveTuicrSessionPath(t *testing.T) {
	dir := t.TempDir()
	reviewsDir := filepath.Join(dir, "reviews")
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Session files must actually exist — resolution validates the path
	// before returning it (we never inject a --session path the agent
	// can't open).
	activeReal := filepath.Join(dir, "active-real.json")
	writeFile(t, activeReal, `{}`)
	idxReal := filepath.Join(reviewsDir, "sessions", "idx.json")
	if err := os.MkdirAll(filepath.Dir(idxReal), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, idxReal, `{}`)

	// 1. active_sessions.json hit — wins over index.json.
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"), `{
        "version": "1.0",
        "sessions": [
          {"slug": "gh:o/r/pr/1", "path": "`+activeReal+`"}
        ]
    }`)
	writeFile(t, filepath.Join(reviewsDir, "index.json"), `{
        "version": "2.0",
        "entries": {
          "gh:o/r/pr/1": [{"path": "sessions/idx.json"}]
        }
    }`)
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/1"); got != activeReal {
		t.Errorf("active_sessions.json hit: got %q want %q", got, activeReal)
	}

	// 2. only index.json has it — resolve relative path against
	//    <dataDir>/reviews/.
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"), `{"version":"1.0","sessions":[]}`)
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/1"); got != idxReal {
		t.Errorf("index.json hit: got %q want %q", got, idxReal)
	}

	// 3. neither has it — empty.
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/missing"); got != "" {
		t.Errorf("missing slug: got %q want empty", got)
	}

	// 3b. slug present but the file it points at is gone — rejected, not
	//     injected. This is the stale-index case the existence check fixes.
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"), `{
        "version": "1.0",
        "sessions": [
          {"slug": "gh:o/r/pr/9", "path": "`+filepath.Join(dir, "does-not-exist.json")+`"}
        ]
    }`)
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/9"); got != "" {
		t.Errorf("dangling path should be rejected: got %q want empty", got)
	}

	// 4. malformed JSON degrades to empty, not panic.
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"), `not json`)
	writeFile(t, filepath.Join(reviewsDir, "index.json"), `also not json`)
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/1"); got != "" {
		t.Errorf("malformed: got %q want empty", got)
	}
}

func TestAwaitTuicrSessionPathRespectsTimeout(t *testing.T) {
	dir := t.TempDir()
	// No files written — should time out promptly.
	start := time.Now()
	got := awaitTuicrSessionPath(context.Background(), dir, "gh:o/r/pr/1", 250*time.Millisecond)
	elapsed := time.Since(start)
	if got != "" {
		t.Errorf("expected empty result on timeout, got %q", got)
	}
	if elapsed < 200*time.Millisecond || elapsed > 800*time.Millisecond {
		t.Errorf("timeout window off: elapsed %s, want ~250ms", elapsed)
	}
}

func TestReadSessionHeadSHA(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.json")
	writeFile(t, good, `{"pr_session_key":{"number":7,"head_sha":"  abc123  "}}`)
	if got := readSessionHeadSHA(good); got != "abc123" {
		t.Errorf("head sha: got %q want %q", got, "abc123")
	}

	// Missing file, malformed JSON, and a non-PR session all degrade to "".
	if got := readSessionHeadSHA(filepath.Join(dir, "nope.json")); got != "" {
		t.Errorf("missing file: got %q want empty", got)
	}
	bad := filepath.Join(dir, "bad.json")
	writeFile(t, bad, `not json`)
	if got := readSessionHeadSHA(bad); got != "" {
		t.Errorf("malformed: got %q want empty", got)
	}
	local := filepath.Join(dir, "local.json")
	writeFile(t, local, `{"branch_name":"main"}`)
	if got := readSessionHeadSHA(local); got != "" {
		t.Errorf("non-PR session: got %q want empty", got)
	}
}

func TestFindPriorSessionsWithComments(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "reviews", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		writeFile(t, filepath.Join(sessions, name), body)
	}

	// Prior head, review-level comment — a match (older).
	write("old.json", `{
        "pr_session_key":{"number":42,"head_sha":"OLD"},
        "updated_at":"2026-07-01T10:00:00Z",
        "review_comments":[{"body":"x"}]
    }`)
	// Prior head, line comment nested under files — a match (newer).
	write("older-head-line.json", `{
        "pr_session_key":{"number":42,"head_sha":"MID"},
        "updated_at":"2026-07-05T10:00:00Z",
        "files":{"a.go":{"line_comments":[{"line":3},{"line":9}],"file_comments":[]}}
    }`)
	// Current head — excluded (it's the target, not a source).
	write("current.json", `{
        "pr_session_key":{"number":42,"head_sha":"NEW"},
        "updated_at":"2026-07-13T10:00:00Z",
        "review_comments":[{"body":"y"}]
    }`)
	// Prior head but no comments — excluded.
	write("empty.json", `{
        "pr_session_key":{"number":42,"head_sha":"BARE"},
        "updated_at":"2026-07-02T10:00:00Z",
        "review_comments":[],"files":{"a.go":{"line_comments":[],"file_comments":[]}}
    }`)
	// Different PR — excluded.
	write("otherpr.json", `{
        "pr_session_key":{"number":99,"head_sha":"ZZZ"},
        "updated_at":"2026-07-09T10:00:00Z",
        "review_comments":[{"body":"z"}]
    }`)
	// Malformed — skipped, not fatal.
	write("garbage.json", `not json at all`)

	got := findPriorSessionsWithComments(dir, 42, "NEW")
	if len(got) != 2 {
		t.Fatalf("expected 2 prior sessions, got %d: %+v", len(got), got)
	}
	// Newest first.
	if got[0].HeadSHA != "MID" || got[0].Comments != 2 {
		t.Errorf("first (newest): got head=%q comments=%d, want MID/2", got[0].HeadSHA, got[0].Comments)
	}
	if got[1].HeadSHA != "OLD" || got[1].Comments != 1 {
		t.Errorf("second: got head=%q comments=%d, want OLD/1", got[1].HeadSHA, got[1].Comments)
	}

	// Guards: empty data dir and non-positive PR number yield nil.
	if got := findPriorSessionsWithComments("", 42, "NEW"); got != nil {
		t.Errorf("empty dataDir: got %+v want nil", got)
	}
	if got := findPriorSessionsWithComments(dir, 0, "NEW"); got != nil {
		t.Errorf("zero PR: got %+v want nil", got)
	}
}

func TestSessionCommentsForHead(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "reviews", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) { writeFile(t, filepath.Join(sessions, name), body) }

	// Two current-head sessions (unusual but tolerated): counts sum. A
	// prior-head and other-PR session are ignored.
	write("cur1.json", `{"pr_session_key":{"number":5,"head_sha":"NEW"},"review_comments":[{"b":1}]}`)
	write("cur2.json", `{"pr_session_key":{"number":5,"head_sha":"NEW"},"files":{"a.go":{"line_comments":[{"l":1},{"l":2}]}}}`)
	write("old.json", `{"pr_session_key":{"number":5,"head_sha":"OLD"},"review_comments":[{"b":1}]}`)
	write("otherpr.json", `{"pr_session_key":{"number":6,"head_sha":"NEW"},"review_comments":[{"b":1}]}`)

	if got := sessionCommentsForHead(dir, 5, "NEW"); got != 3 {
		t.Errorf("NEW head: got %d want 3", got)
	}
	if got := sessionCommentsForHead(dir, 5, "MISSING"); got != 0 {
		t.Errorf("absent head: got %d want 0", got)
	}
	// Guards.
	if got := sessionCommentsForHead("", 5, "NEW"); got != 0 {
		t.Errorf("empty dataDir: got %d want 0", got)
	}
	if got := sessionCommentsForHead(dir, 5, ""); got != 0 {
		t.Errorf("empty head: got %d want 0", got)
	}
}

func TestShortSHA(t *testing.T) {
	cases := map[string]string{
		"16d77d5f2c1401bb6f9530d2305df8570d6bc3d1": "16d77d5f",
		"abc":      "abc",
		"":         "",
		"abcdefgh": "abcdefgh",
	}
	for in, want := range cases {
		if got := shortSHA(in); got != want {
			t.Errorf("shortSHA(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAwaitTuicrSessionPathForHead(t *testing.T) {
	dir := t.TempDir()
	reviewsDir := filepath.Join(dir, "reviews")
	sessionsDir := filepath.Join(reviewsDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "gh:o/r/pr/7"
	sess := filepath.Join(sessionsDir, "s.json")
	writeFile(t, sess, `{"pr_session_key":{"number":7,"head_sha":"aaaa1111bbbb2222"}}`)
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"),
		`{"version":"1.0","sessions":[{"slug":"`+slug+`","path":"`+sess+`"}]}`)

	// Matching head resolves immediately.
	if got := awaitTuicrSessionPathForHead(context.Background(), dir, slug, "aaaa1111bbbb2222", time.Second); got != sess {
		t.Errorf("matching head: got %q want %q", got, sess)
	}

	// Empty wantHead degrades to any-non-empty-path behavior.
	if got := awaitTuicrSessionPathForHead(context.Background(), dir, slug, "", time.Second); got != sess {
		t.Errorf("empty wantHead: got %q want %q", got, sess)
	}

	// A head that never appears times out and returns "" (the caller then
	// falls back to naming whatever session tuicr currently shows).
	start := time.Now()
	if got := awaitTuicrSessionPathForHead(context.Background(), dir, slug, "deadbeefdeadbeef", 200*time.Millisecond); got != "" {
		t.Errorf("non-matching head: got %q want empty", got)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("expected to wait ~200ms before timing out, waited %s", elapsed)
	}

	// Cancelled context bails out promptly rather than waiting the timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := awaitTuicrSessionPathForHead(ctx, dir, slug, "deadbeefdeadbeef", 5*time.Second); got != "" {
		t.Errorf("cancelled ctx: got %q want empty", got)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

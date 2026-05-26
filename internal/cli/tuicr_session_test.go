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
		name  string
		url   string
		num   int
		want  string
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

	// 1. active_sessions.json hit — wins over index.json.
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"), `{
        "version": "1.0",
        "sessions": [
          {"slug": "gh:o/r/pr/1", "path": "/abs/active.json"}
        ]
    }`)
	writeFile(t, filepath.Join(reviewsDir, "index.json"), `{
        "version": "2.0",
        "entries": {
          "gh:o/r/pr/1": [{"path": "sessions/idx.json"}]
        }
    }`)
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/1"); got != "/abs/active.json" {
		t.Errorf("active_sessions.json hit: got %q want /abs/active.json", got)
	}

	// 2. only index.json has it — resolve relative path against
	//    <dataDir>/reviews/.
	writeFile(t, filepath.Join(reviewsDir, "active_sessions.json"), `{"version":"1.0","sessions":[]}`)
	wantAbs := filepath.Join(reviewsDir, "sessions", "idx.json")
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/1"); got != wantAbs {
		t.Errorf("index.json hit: got %q want %q", got, wantAbs)
	}

	// 3. neither has it — empty.
	if got := resolveTuicrSessionPath(dir, "gh:o/r/pr/missing"); got != "" {
		t.Errorf("missing slug: got %q want empty", got)
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

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

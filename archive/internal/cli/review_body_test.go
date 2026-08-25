package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The failure this repairs: an agent escaped every backtick for a shell quoting
// context it guessed wrong, so seven findings were published to a real PR reading
// "Pin the \`graphql_client\` git dep" — visible backslashes where code spans were
// meant.
func TestCommentBodyUnescapesAUniformlyEscapedBody(t *testing.T) {
	// One backslash before each backtick, which is what the store actually held.
	in := "Pin the \\`graphql_client\\` dep to a rev"
	got, err := commentBody(in, "", nil)
	if err != nil {
		t.Fatalf("commentBody: %v", err)
	}
	if strings.Contains(got, "\\`") {
		t.Fatalf("escapes survived: %q", got)
	}
	if !strings.Contains(got, "`graphql_client`") {
		t.Fatalf("expected a code span, got %q", got)
	}
}

// A body that uses plain backticks AND escaped ones is expressing intent — it wants
// a literal backtick shown somewhere — so it is left exactly as written.
func TestCommentBodyLeavesAMixedBodyAlone(t *testing.T) {
	in := "use `code` and write \\` for a literal one"
	got, err := commentBody(in, "", nil)
	if err != nil {
		t.Fatalf("commentBody: %v", err)
	}
	if got != in {
		t.Fatalf("a mixed body was rewritten:\n got %q\nwant %q", got, in)
	}
}

func TestCommentBodyLeavesAnOrdinaryBodyAlone(t *testing.T) {
	in := "this drops the error from `spawnSync`"
	got, err := commentBody(in, "", nil)
	if err != nil {
		t.Fatalf("commentBody: %v", err)
	}
	if got != in {
		t.Fatalf("got %q, want %q", got, in)
	}
}

// A file goes through no shell, so its bytes are the author's — including any
// backslash they chose to write.
func TestCommentBodyFromAFileIsVerbatim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	in := "every \\` here is deliberate \\`really\\`"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := commentBody("ignored", path, nil)
	if err != nil {
		t.Fatalf("commentBody: %v", err)
	}
	if got != in {
		t.Fatalf("a file body was rewritten:\n got %q\nwant %q", got, in)
	}
}

func TestCommentBodyFromStdin(t *testing.T) {
	got, err := commentBody("", "-", strings.NewReader("from a pipe"))
	if err != nil {
		t.Fatalf("commentBody: %v", err)
	}
	if got != "from a pipe" {
		t.Fatalf("got %q", got)
	}
}

func TestCommentBodyReportsAMissingFile(t *testing.T) {
	if _, err := commentBody("", filepath.Join(t.TempDir(), "nope.md"), nil); err == nil {
		t.Fatal("expected an unreadable body file to be an error")
	}
}

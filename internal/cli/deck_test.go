package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/jj"
)

func TestDeckSessionNameFormat(t *testing.T) {
	got := DeckSessionName("agent-deck", "qa")
	if got != "[awp]agent-deck__qa" {
		t.Fatalf("got %q", got)
	}
}

func TestParseAwpSession(t *testing.T) {
	cases := []struct {
		in       string
		repo, ws string
		ok       bool
	}{
		{"[awp]agent-deck__qa", "agent-deck", "qa", true},
		{"[awp]repo__my__workspace", "repo", "my__workspace", true},
		{"main", "", "", false},
		{"[awp]noSeparator", "", "", false},
	}
	for _, tc := range cases {
		r, w, ok := parseAwpSession(tc.in)
		if ok != tc.ok || r != tc.repo || w != tc.ws {
			t.Fatalf("parseAwpSession(%q) = (%q,%q,%v) want (%q,%q,%v)", tc.in, r, w, ok, tc.repo, tc.ws, tc.ok)
		}
	}
}

func TestMaybeUpdateStaleWorkingCopyNonInteractiveReturnsOriginalError(t *testing.T) {
	client := jj.New(NewExecRunner())
	cause := errors.New("stale")
	updated, err := maybeUpdateStaleWorkingCopy(client, strings.NewReader(""), &bytes.Buffer{}, cause)
	if updated {
		t.Fatal("expected no update")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected original error, got %v", err)
	}
}

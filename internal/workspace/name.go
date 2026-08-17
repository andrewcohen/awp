package workspace

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var invalidNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

func NormalizeName(name string) (string, error) {
	n := strings.TrimSpace(strings.ToLower(name))
	n = strings.ReplaceAll(n, "_", "-")
	n = strings.ReplaceAll(n, " ", "-")
	n = invalidNameChars.ReplaceAllString(n, "-")
	n = strings.Trim(n, "-")
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	if n == "" {
		return "", errors.New("workspace name is empty after normalization")
	}
	return n, nil
}

// slugWords is how many words of free text SlugFromText keeps. Free text is
// a sentence ("fix the sidebar cursor bug in the deck"); a directory name
// wants a handful of words, not the whole thing.
const slugWords = 5

// slugMaxLen bounds the result regardless of word count, so one very long
// word cannot produce an unwieldy directory name.
const slugMaxLen = 48

// SlugFromText derives a directory-safe workspace name from a sentence the
// user typed in their own words. It is the local answer to the naming
// question — used when the agent that would normally name the workspace is
// unavailable, and to sanitize the name it returns when it is.
//
// The result is always usable: text that normalizes to nothing yields
// "workspace" rather than an error, because the caller is a fallback path
// that has nowhere better to go.
func SlugFromText(text string) string {
	fields := strings.Fields(text)
	if len(fields) > slugWords {
		fields = fields[:slugWords]
	}
	n, err := NormalizeName(strings.Join(fields, " "))
	if err != nil {
		return "workspace"
	}
	if len(n) > slugMaxLen {
		n = strings.Trim(n[:slugMaxLen], "-")
	}
	if n == "" {
		return "workspace"
	}
	return n
}

// ReviewWorkspaceName is the workspace name the review flow assigns to a
// PR checkout: "pr-<number>-<head-branch>". Centralized so the deck can
// predict the name it will land under — for an optimistic "setting up"
// row — using the exact string the review flow passes to PrepareWorkspace,
// keeping the two in sync. The result is unnormalized; callers normalize
// it (PrepareWorkspace via resolveName, the deck via NormalizeName) before
// use.
func ReviewWorkspaceName(prNumber int, branch string) string {
	return fmt.Sprintf("pr-%d-%s", prNumber, strings.TrimSpace(branch))
}

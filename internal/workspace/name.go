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

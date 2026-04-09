package workspace

import (
	"errors"
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

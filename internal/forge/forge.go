// Package forge wraps the per-host CLIs (gh for GitHub, glab for GitLab)
// for fetching PR/MR metadata and building the bash one-liners that run
// inside tmux windows. Detection picks the right backend by inspecting
// the repo's git remote URL.
package forge

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

// PRSummary is the row shape used by list views (deck PR fetcher,
// review picker, new-flow PR list).
type PRSummary struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HeadRef string `json:"headRefName"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	IsDraft bool `json:"isDraft"`
}

// PRInfo is the detail shape used by `awp review`.
type PRInfo struct {
	Number  int    `json:"number"`
	HeadRef string `json:"headRefName"`
	BaseRef string `json:"baseRefName"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	URL     string `json:"url"`
}

// Forge abstracts a code-host CLI. Methods named *Command return a bash
// command intended to be sent into a tmux window via SendCommand.
type Forge interface {
	Name() string
	ListPRs() ([]PRSummary, error)
	FetchPR(num int) (PRInfo, error)
	PRDescriptionCommand(num int) string
	CIWatchCommand() string
}

// Detect picks a Forge for the repo whose `origin` remote is reachable
// from runner's cwd. A non-empty override ("github" or "gitlab") wins
// over auto-detection — useful for self-hosted GitLab installations
// whose hostname doesn't contain "gitlab". When override is empty,
// hosts containing "github" use gh, hosts containing "gitlab" use glab,
// and anything else returns an unsupported-host error.
func Detect(runner Runner, override string) (Forge, error) {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "":
		// fall through to auto-detect
	case "github":
		return NewGitHub(runner), nil
	case "gitlab":
		return NewGitLab(runner), nil
	default:
		return nil, fmt.Errorf("unknown forge override %q (want \"github\" or \"gitlab\")", override)
	}
	host, err := remoteHost(runner)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(host)
	switch {
	case strings.Contains(lower, "github"):
		return NewGitHub(runner), nil
	case strings.Contains(lower, "gitlab"):
		return NewGitLab(runner), nil
	default:
		return nil, fmt.Errorf("unsupported forge host %q: set deck.forge to \"github\" or \"gitlab\" in awp config to override", host)
	}
}

func remoteHost(runner Runner) (string, error) {
	// `git remote get-url` (not `git config --get remote.origin.url`) so
	// `[url] insteadOf` rewrites resolve to whatever the user actually
	// pushes/pulls against.
	out, err := runner.Run(context.Background(), "", "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w: %s", err, out)
	}
	return parseHost(strings.TrimSpace(out))
}

// parseHost extracts the hostname from common git remote URL forms:
//   - git@host:owner/repo.git
//   - ssh://git@host[:port]/owner/repo.git
//   - https://host/owner/repo.git
func parseHost(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty remote url")
	}
	if !strings.Contains(raw, "://") {
		// scp-like: [user@]host:path
		rest := raw
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if colon := strings.Index(rest, ":"); colon >= 0 {
			return rest[:colon], nil
		}
		return "", fmt.Errorf("could not parse remote url %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse remote url %q: %w", raw, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("remote url %q has no host", raw)
	}
	return host, nil
}

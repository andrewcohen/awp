package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// GitLab is the glab-backed Forge implementation. Exported for symmetry
// with GitHub.
type GitLab struct {
	runner Runner
}

// NewGitLab builds a glab-backed Forge tied to the given runner.
func NewGitLab(runner Runner) *GitLab { return &GitLab{runner: runner} }

func (g *GitLab) Name() string { return "gitlab" }

// glab mr list --output=json returns objects keyed by GitLab API names.
type glabMRSummary struct {
	IID            int    `json:"iid"`
	Title          string `json:"title"`
	SourceBranch   string `json:"source_branch"`
	Draft          bool   `json:"draft"`
	WorkInProgress bool   `json:"work_in_progress"`
	Author         struct {
		Username string `json:"username"`
	} `json:"author"`
}

type glabMRView struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	WebURL       string `json:"web_url"`
}

func (g *GitLab) ListPRs() ([]PRSummary, error) {
	// glab paginates differently from gh: `--per-page=100` returns the
	// first 100 open MRs (one page), while gh's `--limit=100` caps the
	// total. Both effectively truncate at 100, which is fine for the
	// interactive picker.
	out, err := g.runner.Run(
		context.Background(), "",
		"glab", "mr", "list",
		"--output=json",
		"--per-page=100",
	)
	if err != nil {
		return nil, fmt.Errorf("glab mr list: %w: %s", err, out)
	}
	var raw []glabMRSummary
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse glab mr list output: %w", err)
	}
	prs := make([]PRSummary, len(raw))
	for i, m := range raw {
		prs[i] = PRSummary{
			Number:  m.IID,
			Title:   m.Title,
			HeadRef: m.SourceBranch,
			IsDraft: m.Draft || m.WorkInProgress,
		}
		prs[i].Author.Login = m.Author.Username
	}
	return prs, nil
}

func (g *GitLab) FetchPR(num int) (PRInfo, error) {
	out, err := g.runner.Run(
		context.Background(), "",
		"glab", "mr", "view", strconv.Itoa(num),
		"--output=json",
	)
	if err != nil {
		return PRInfo{}, fmt.Errorf("glab mr view %d: %w: %s", num, err, out)
	}
	var v glabMRView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return PRInfo{}, fmt.Errorf("parse glab mr view output: %w", err)
	}
	return PRInfo{
		Number:  v.IID,
		HeadRef: v.SourceBranch,
		BaseRef: v.TargetBranch,
		Title:   v.Title,
		Body:    v.Description,
		URL:     v.WebURL,
	}, nil
}

func (g *GitLab) PRDescriptionCommand(num int) string {
	// GLAB_FORCE_TTY=1 keeps colors when stdout is the pipe to less,
	// mirroring the GH_FORCE_TTY treatment in the gh forge.
	return fmt.Sprintf("GLAB_FORCE_TTY=1 glab mr view %d | less -R", num)
}

func (g *GitLab) CIWatchCommand() string {
	return `bash -c 'b=$(jj log --no-graph -r "latest(::@ & bookmarks())" -T "local_bookmarks.map(|b| b.name()).join(\"\n\") ++ \"\n\"" | head -n1); glab ci view -b "$b"'`
}

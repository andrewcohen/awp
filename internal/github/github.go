package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type PRInfo struct {
	Number   int    `json:"number"`
	HeadRef  string `json:"headRefName"`
	BaseRef  string `json:"baseRefName"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
}

type Client struct {
	runner Runner
}

func New(runner Runner) *Client {
	return &Client{runner: runner}
}

type PRSummary struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HeadRef string `json:"headRefName"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	IsDraft bool `json:"isDraft"`
}

func (c *Client) ListPRs() ([]PRSummary, error) {
	out, err := c.runner.Run(
		context.Background(), "",
		"gh", "pr", "list",
		"--json", "number,title,headRefName,author,isDraft",
		"--limit", "100",
	)
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w: %s", err, out)
	}
	var prs []PRSummary
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}
	return prs, nil
}

func (c *Client) FetchPR(num int) (PRInfo, error) {
	out, err := c.runner.Run(
		context.Background(), "",
		"gh", "pr", "view", strconv.Itoa(num),
		"--json", "number,headRefName,baseRefName,title,body,url",
	)
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %d: %w: %s", num, err, out)
	}
	var info PRInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return PRInfo{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return info, nil
}

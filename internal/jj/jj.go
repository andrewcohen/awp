package jj

import (
	"context"
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/cli"
)

type Client struct {
	runner cli.Runner
}

func New(runner cli.Runner) *Client {
	return &Client{runner: runner}
}

func (c *Client) RepoRoot() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "root")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) WorkspaceExists(name string) (bool, error) {
	names, err := c.ListWorkspaceNames()
	if err != nil {
		return false, err
	}
	for _, ws := range names {
		if ws == name {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) ListWorkspaceNames() ([]string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "workspace", "list", "-T", "name ++ \"\\n\"")
	if err != nil {
		return nil, err
	}
	return parseWorkspaceNames(out), nil
}

func (c *Client) AddWorkspace(name string, path string, revision string) error {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		revision = "@"
	}
	out, err := c.runner.Run(context.Background(), "", "jj", "workspace", "add", "--name", name, "-r", revision, path)
	if err == nil {
		return nil
	}

	if revision != "@" {
		_, _ = c.runner.Run(context.Background(), "", "jj", "bookmark", "track", revision)
		out2, err2 := c.runner.Run(context.Background(), "", "jj", "workspace", "add", "--name", name, "-r", revision, path)
		if err2 == nil {
			return nil
		}
		if strings.TrimSpace(out2) != "" {
			out = out2
			err = err2
		}
	}
	return formatCommandError(fmt.Sprintf("create workspace %q", name), err, out)
}

func (c *Client) SetBookmark(bookmarkName string, workspaceName string) error {
	bookmarkName = strings.TrimSpace(bookmarkName)
	workspaceName = strings.TrimSpace(workspaceName)
	out, err := c.runner.Run(context.Background(), "", "jj", "bookmark", "set", "--allow-backwards", bookmarkName, "-r", workspaceName+"@")
	if err != nil {
		return formatCommandError(fmt.Sprintf("set bookmark %q for workspace %q", bookmarkName, workspaceName), err, out)
	}
	return nil
}

func (c *Client) RenameWorkspace(path string, newName string) error {
	out, err := c.runner.Run(context.Background(), path, "jj", "workspace", "rename", newName)
	if err != nil {
		return formatCommandError(fmt.Sprintf("rename workspace to %q", newName), err, out)
	}
	return nil
}

func (c *Client) ForgetWorkspace(name string) error {
	out, err := c.runner.Run(context.Background(), "", "jj", "workspace", "forget", name)
	if err != nil {
		return formatCommandError(fmt.Sprintf("forget workspace %q", name), err, out)
	}
	return nil
}

func formatCommandError(action string, err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w\n%s", action, err, output)
}

func parseWorkspaceNames(out string) []string {
	lines := strings.Split(out, "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			line = strings.TrimSpace(line[:idx])
		}
		names = append(names, line)
	}
	return names
}

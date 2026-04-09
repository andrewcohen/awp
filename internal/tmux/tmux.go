package tmux

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

func (c *Client) WindowExists(name string) (bool, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-windows", "-F", "#{window_name}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) NewWindow(name string, dir string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "new-window", "-d", "-n", name, "-c", dir)
	if err != nil {
		return fmt.Errorf("create tmux window %q: %w", name, err)
	}
	return nil
}

func (c *Client) SwitchToWindow(name string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "select-window", "-t", name)
	if err != nil {
		return fmt.Errorf("switch to tmux window %q: %w", name, err)
	}
	return nil
}

func (c *Client) RenameWindow(oldName string, newName string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "rename-window", "-t", oldName, newName)
	if err != nil {
		return fmt.Errorf("rename tmux window %q to %q: %w", oldName, newName, err)
	}
	return nil
}

func (c *Client) KillWindow(name string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "kill-window", "-t", name)
	if err != nil {
		return fmt.Errorf("kill tmux window %q: %w", name, err)
	}
	return nil
}

func (c *Client) CurrentWindow() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "display-message", "-p", "#{window_name}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

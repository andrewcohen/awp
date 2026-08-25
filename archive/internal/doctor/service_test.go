package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/agenthooks"
)

// installAgentHooksForTest stages the global Claude/pi files in a fake $HOME
// so doctor's hook-installed checks pass during tests.
func installAgentHooksForTest(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	if _, err := agenthooks.InstallClaude(); err != nil {
		t.Fatalf("install claude hooks: %v", err)
	}
	if _, err := agenthooks.InstallPi(); err != nil {
		t.Fatalf("install pi extension: %v", err)
	}
}

type fakeRunner struct {
	responses map[string]struct {
		out string
		err error
	}
}

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	if r, ok := f.responses[key]; ok {
		return r.out, r.err
	}
	return "", nil
}

type fakeHooks struct {
	commands []string
	err      error
}

func (f *fakeHooks) PostWorkspaceStart(string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.commands, nil
}

func TestDoctorRunReportsSuccess(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	installAgentHooksForTest(t, home)
	out := &bytes.Buffer{}
	r := &fakeRunner{responses: map[string]struct {
		out string
		err error
	}{
		"jj --version":                         {out: "jj 0.1"},
		"tmux -V":                              {out: "tmux 3.4"},
		"jj root":                              {out: repo + "\n"},
		"jj workspace list -T name ++ \"\\n\"": {out: "default\n"},
		"jj log -r default@ --no-graph -T commit_id.short() ++ \"\\n\"": {out: "abc123\n"},
	}}

	svc := New(Dependencies{Runner: r, Hooks: &fakeHooks{commands: []string{"echo hi"}}, HomeDir: home, Out: out})
	if err := svc.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "doctor checks passed") {
		t.Fatalf("expected success output, got %q", out.String())
	}
}

func TestDoctorRunFailsForUnsupportedHooksKey(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".awp"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".awp", "config.json"), []byte(`{"hooks":{"bootstrapx":["echo hi"]}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	r := &fakeRunner{responses: map[string]struct {
		out string
		err error
	}{
		"jj --version":                         {out: "jj 0.1"},
		"tmux -V":                              {out: "tmux 3.4"},
		"jj root":                              {out: repo + "\n"},
		"jj workspace list -T name ++ \"\\n\"": {out: "default\n"},
		"jj log -r default@ --no-graph -T commit_id.short() ++ \"\\n\"": {out: "abc123\n"},
	}}
	out := &bytes.Buffer{}
	svc := New(Dependencies{Runner: r, HomeDir: home, Out: out})
	err := svc.Run()
	if err == nil {
		t.Fatal("expected doctor error")
	}
	if !strings.Contains(out.String(), "expected hooks.bootstrap") {
		t.Fatalf("expected hooks key error, got %q", out.String())
	}
}

func TestDoctorRunFailsForInvalidWorkspace(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	r := &fakeRunner{responses: map[string]struct {
		out string
		err error
	}{
		"jj --version":                         {out: "jj 0.1"},
		"tmux -V":                              {out: "tmux 3.4"},
		"jj root":                              {out: repo + "\n"},
		"jj workspace list -T name ++ \"\\n\"": {out: "broken\n"},
		"jj log -r broken@ --no-graph -T commit_id.short() ++ \"\\n\"": {err: errors.New("no wc")},
	}}

	svc := New(Dependencies{Runner: r, Hooks: &fakeHooks{}, HomeDir: home, Out: &bytes.Buffer{}})
	err := svc.Run()
	if err == nil {
		t.Fatal("expected doctor error")
	}
	if !strings.Contains(err.Error(), "issue") {
		t.Fatalf("unexpected error: %v", err)
	}

}

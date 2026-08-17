package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoUnder makes a directory that looks like a jj repo, so discoverProjects and
// isRepoDir both count it.
func repoUnder(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(path, ".jj"), 0o755); err != nil {
		t.Fatalf("make repo %s: %v", name, err)
	}
	return path
}

func TestTakeProjectFlagBothSpellings(t *testing.T) {
	for _, args := range [][]string{
		{"--project", "proj", "ws"},
		{"--project=proj", "ws"},
		{"ws", "--project", "proj"},
	} {
		project, rest, err := takeProjectFlag(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if project != "proj" {
			t.Errorf("%v: project = %q, want proj", args, project)
		}
		if len(rest) != 1 || rest[0] != "ws" {
			t.Errorf("%v: rest = %v, want [ws]", args, rest)
		}
	}
}

// A flag with nothing after it is a mistake worth naming, not an empty project
// that resolution will later blame on something else.
func TestTakeProjectFlagWantsAValue(t *testing.T) {
	for _, args := range [][]string{
		{"--project"},
		{"--project="},
		{"ws", "--project"},
	} {
		if _, _, err := takeProjectFlag(args); err == nil {
			t.Errorf("%v: expected an error, got none", args)
		} else if !strings.Contains(err.Error(), projectFlag) {
			t.Errorf("%v: error should name the flag, got %v", args, err)
		}
	}
}

// The one that matters. Resolution takes no cwd and consults none, so the case
// this test runs in — inside a real repository, where a fallback would have
// quietly worked — still fails.
//
// Run from the package's own directory, which is inside awp's repo. If someone
// later teaches resolveProjectRoot to look around when it has nothing, this is
// where it stops being a captain-safe command and starts being one that addresses
// whatever repo the process was launched from.
func TestNoProjectIsAnErrorEvenInsideARepo(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if !insideRepo(wd) {
		t.Skipf("expected to be running inside a repository, but %s is not under one", wd)
	}

	_, err = resolveProjectRoot("", []string{t.TempDir()})
	if err == nil {
		t.Fatal("no project given resolved to something — a captain command would address the cwd's repo")
	}
	if !strings.Contains(err.Error(), projectFlag) {
		t.Errorf("the error should say how to name a project, got %v", err)
	}
}

// insideRepo says whether path or any ancestor looks like a repo.
func insideRepo(path string) bool {
	for {
		if isRepoDir(path) {
			return true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}

func TestResolveProjectRootByName(t *testing.T) {
	root := t.TempDir()
	want := repoUnder(t, root, "alpha")
	repoUnder(t, root, "beta")

	got, err := resolveProjectRoot("alpha", []string{root})
	if err != nil {
		t.Fatalf("resolve alpha: %v", err)
	}
	if got != want {
		t.Errorf("resolved to %q, want %q", got, want)
	}
}

// An unknown name says what is known. A captain that guessed wrong can correct
// itself from the error instead of asking.
func TestUnknownProjectListsTheKnownOnes(t *testing.T) {
	root := t.TempDir()
	repoUnder(t, root, "alpha")
	repoUnder(t, root, "beta")

	_, err := resolveProjectRoot("gamma", []string{root})
	if err == nil {
		t.Fatal("expected an error for an unknown project")
	}
	for _, want := range []string{"gamma", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got %v", want, err)
		}
	}
}

// A path is accepted directly, so a repo the configured roots do not cover is
// still addressable rather than unreachable.
func TestResolveProjectRootByPath(t *testing.T) {
	outside := repoUnder(t, t.TempDir(), "elsewhere")

	got, err := resolveProjectRoot(outside, nil)
	if err != nil {
		t.Fatalf("resolve by path: %v", err)
	}
	if got != outside {
		t.Errorf("resolved to %q, want %q", got, outside)
	}
}

func TestPathThatIsNotARepoIsRefused(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "notarepo")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := resolveProjectRoot(plain, nil)
	if err == nil {
		t.Fatal("expected a directory with no .jj or .git to be refused")
	}
	if !strings.Contains(err.Error(), plain) {
		t.Errorf("error should name the path, got %v", err)
	}
}

// No roots configured and a name rather than a path: say that the roots are
// empty, since "not found" would send the reader looking for a typo.
func TestNoRootsSaysToConfigureThem(t *testing.T) {
	_, err := resolveProjectRoot("alpha", nil)
	if err == nil {
		t.Fatal("expected an error with no project roots configured")
	}
	if !strings.Contains(err.Error(), "project_roots") {
		t.Errorf("error should point at deck.project_roots, got %v", err)
	}
}

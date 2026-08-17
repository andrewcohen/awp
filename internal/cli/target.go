package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/workspace"
)

// Naming a target explicitly, for commands that cannot resolve one from where
// they are standing.
//
// Everywhere else in awp, the subject is ambient: jj.RepoRoot() walks up from
// the process's cwd to find the repo, and the workspace is the directory you are
// in. That works because a person runs those commands from inside the thing they
// mean. `awp watch --repo` is the one existing exception, and it exists because
// the deck runs watch for a row that is not where the deck is.
//
// The captain generalises that exception. It has no repository and its cwd is
// inside no project, so for it the ambient answer is not merely unhelpful — it is
// absent, and a fallback to cwd would silently address whatever repo the deck
// happened to be launched from. That is the failure this file exists to make
// impossible: resolveProjectRoot never consults the process's location, so a
// caller that forgets to pass a project gets an error rather than someone else's
// repo.
//
// So the rule for any command the captain may run: say the project, or be told
// you have to.

// projectFlag is how a target names its project on the command line. One
// spelling, so a caller cannot say it a second way that drifts.
const projectFlag = "--project"

// takeProjectFlag pulls `--project <name>` (or `--project=<name>`) out of args
// and returns it with the remaining arguments.
//
// Returns "" with no error when the flag is absent: whether a missing project is
// fatal belongs to the verb, since some may legitimately accept a cwd fallback
// while a captain's may not. What is never allowed is a *silent* fallback, which
// is why resolveProjectRoot refuses "" rather than guessing.
func takeProjectFlag(args []string) (project string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == projectFlag:
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s needs a project name or path", projectFlag)
			}
			project = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, projectFlag+"="):
			project = strings.TrimSpace(strings.TrimPrefix(arg, projectFlag+"="))
		default:
			rest = append(rest, arg)
			continue
		}
		if project == "" {
			return "", nil, fmt.Errorf("%s needs a project name or path", projectFlag)
		}
	}
	return project, rest, nil
}

// resolveProjectRoot turns a project name into the repo root it means.
//
// name is either the basename of a project discovered under deck.project_roots —
// the same list the deck's `o` picker offers, so the captain and the picker agree
// on what a project is — or a path to a repo, which is accepted directly so a
// project outside the configured roots is still addressable.
//
// It deliberately takes no cwd and has no fallback. An empty name is an error
// naming the flag, not an invitation to look around.
func resolveProjectRoot(name string, roots []string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("no project given: pass %s <name|path> (this command cannot infer one)", projectFlag)
	}

	// A path first: an explicit location outranks a name that might collide with
	// one, and it is the escape hatch for a repo the roots do not cover.
	if strings.ContainsRune(name, filepath.Separator) || strings.HasPrefix(name, "~") {
		path, err := expandProjectPath(name)
		if err != nil {
			return "", err
		}
		if !isRepoDir(path) {
			return "", fmt.Errorf("%s: not a jj or git repository", path)
		}
		return path, nil
	}

	projects, err := discoverProjects(roots, 4)
	if err != nil {
		return "", fmt.Errorf("discover projects: %w", err)
	}
	if len(projects) == 0 {
		return "", fmt.Errorf("project %q: no projects found — set deck.project_roots in your awp config, or pass %s with a path", name, projectFlag)
	}
	var names []string
	for _, p := range projects {
		if p.Name == name {
			return p.Path, nil
		}
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("project %q not found under deck.project_roots; known: %s", name, strings.Join(names, ", "))
}

// expandProjectPath resolves ~ and makes the path absolute.
func expandProjectPath(name string) (string, error) {
	if strings.HasPrefix(name, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", name, err)
		}
		name = filepath.Join(home, strings.TrimPrefix(name, "~"))
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", name, err)
	}
	return filepath.Clean(abs), nil
}

// serviceForProject is the workspace service for a named project, and the repo
// root it resolved to.
//
// Pinned to that root via fixedDirRunner, the way the deck's per-row service is
// (newDeckActionServiceWithIO). The pinning is the whole point: a service built
// from the ambient cwd would answer questions about whichever repository the
// process started in, and answer them successfully, which is the failure this file
// exists to make impossible.
func (a *App) serviceForProject(name string) (workspace.Service, string, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	root, err := resolveProjectRoot(name, cfg.Deck.ProjectRoots)
	if err != nil {
		return nil, "", err
	}
	return newDeckActionServiceWithIO(a.runner, root, a.in, a.out), root, nil
}

// ambientRepoRoot is the repository the process is standing in.
//
// The thing a captain's command may never use, and the thing a person's command
// should: `awp w list` has always meant "here", and making one verb out of four
// demand a flag would be a worse CLI for the sake of a rule that is about a
// different caller. A verb that accepts it says which project it resolved to, so a
// wrong answer is visible immediately — see App.sendTarget.
func (a *App) ambientRepoRoot() (string, error) {
	root, err := jj.New(a.runner).RepoRoot()
	if err != nil {
		return "", fmt.Errorf("no project given and this is not a repository: pass %s <name|path>", projectFlag)
	}
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("no project given and this is not a repository: pass %s <name|path>", projectFlag)
	}
	return root, nil
}

// projectNameFor is what a repo root is called on the deck: its basename.
//
// One spelling, because the name is matched against session names and row
// identities elsewhere — deck.go and workspaceEnvPairs both derive it the same way,
// and a second derivation is the one that would disagree.
func projectNameFor(repoRoot string) string {
	return filepath.Base(strings.TrimRight(repoRoot, string(filepath.Separator)))
}

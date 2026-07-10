package cli

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

//go:embed review_prompt.md
var reviewPromptTemplate string

// sessionDiscoveryTimeout caps how long we wait for `tuicr pr <n>` to
// register its persisted session in active_sessions.json before we
// build the agent prompt without a resolved JSON path. Five seconds is
// well above the ~1s we've observed in practice and short enough that
// nothing's noticeably stuck for the user.
const sessionDiscoveryTimeout = 5 * time.Second

type writerReporter struct{ out io.Writer }

func (w writerReporter) Step(label string) {
	if w.out != nil {
		fmt.Fprintf(w.out, "▶️ %s\n", label)
	}
}
func (w writerReporter) Log(line string) {
	if w.out != nil {
		fmt.Fprintln(w.out, line)
	}
}

func runReviewWithCharm(runner Runner, svc workspace.Service, prNumber int, in io.Reader, out io.Writer) error {
	return runReviewWithReporter(runner, svc, prNumber, in, writerReporter{out: out})
}

// runReviewWithReporter prepares (or attaches to) the PR-review tmux
// session and switches the user's client to it.
func runReviewWithReporter(runner Runner, svc workspace.Service, prNumber int, in io.Reader, reporter deckui.Reporter) error {
	return runReviewOpts(runner, svc, prNumber, in, reporter, false)
}

// runReviewAsync runs the same setup as runReviewWithReporter but
// suppresses the final SwitchToWindow + SwitchClient — used by the
// async job path so the user's tmux focus is not yanked away.
func runReviewAsync(runner Runner, svc workspace.Service, prNumber int, reporter deckui.Reporter) error {
	return runReviewOpts(runner, svc, prNumber, nil, reporter, true)
}

func runReviewOpts(runner Runner, svc workspace.Service, prNumber int, in io.Reader, reporter deckui.Reporter, noSwitch bool) error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("awp review must run inside tmux")
	}
	if prNumber <= 0 {
		return fmt.Errorf("invalid PR number: %d", prNumber)
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	// Always run jj/git operations from the default workspace (the source repo
	// root) so a stale secondary workspace can't interfere with the new PR
	// workspace's bookmark resolution.
	defaultRoot, derr := jj.New(runner).SourceRepoRoot()
	if derr != nil || strings.TrimSpace(defaultRoot) == "" {
		return fmt.Errorf("resolve default workspace: %w", derr)
	}
	runner = fixedDirRunner{base: runner, dir: defaultRoot}
	gh := github.New(runner)
	tmuxClient := tmux.New(runner)

	reporter.Step(fmt.Sprintf("Fetch PR #%d from GitHub", prNumber))
	pr, err := gh.FetchPR(prNumber)
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(pr.HeadRef)
	base := strings.TrimSpace(pr.BaseRef)
	if branch == "" || base == "" {
		return fmt.Errorf("PR #%d missing head/base ref", prNumber)
	}
	reporter.Log(fmt.Sprintf("PR #%d: %s (%s ← %s)", pr.Number, pr.Title, base, branch))

	reporter.Step("jj git fetch")
	if fetchOut, err := runner.Run(context.Background(), defaultRoot, "jj", "git", "fetch"); err != nil {
		return fmt.Errorf("jj git fetch: %w: %s", err, fetchOut)
	}

	// Fork PRs: the head branch isn't on origin, so the jj fetch above
	// doesn't pick it up. Pull it from the head repo directly into a
	// local ref so jj's bookmark resolution finds it on the next op.
	// No-op when the PR comes from origin (the ref already exists from
	// the fetch above; git fetch is cheap regardless).
	if pr.HeadRepoOwner != "" && pr.HeadRepoName != "" && pr.HeadRef != "" {
		// Derive the fetch URL from `origin`'s URL so private forks
		// keep working: the user's auth (SSH keys, GH PAT in https
		// credential helper, etc.) is tied to a specific URL format,
		// and hard-coding HTTPS prompts for a username on private
		// repos. Falls back to https://github.com/... when origin
		// can't be resolved or parsed.
		fetchURL := forkFetchURLFromOrigin(runner, defaultRoot, pr.HeadRepoOwner, pr.HeadRepoName)
		refspec := pr.HeadRef + ":refs/heads/" + pr.HeadRef
		reporter.Step(fmt.Sprintf("git fetch %s/%s %s", pr.HeadRepoOwner, pr.HeadRepoName, pr.HeadRef))
		if out, err := runner.Run(context.Background(), defaultRoot, "git", "fetch", "--no-tags", fetchURL, refspec); err != nil {
			return fmt.Errorf("fetch PR head from %s: %w: %s", fetchURL, err, out)
		}
		// jj caches its view of git refs per-operation; force a fresh
		// import so the bookmark added above is visible to the
		// PrepareWorkspace step that follows.
		if out, err := runner.Run(context.Background(), defaultRoot, "jj", "git", "import"); err != nil {
			return fmt.Errorf("jj git import after fork fetch: %w: %s", err, out)
		}
	}

	wsName := fmt.Sprintf("pr-%d-%s", pr.Number, branch)
	reporter.Step(fmt.Sprintf("Prepare jj workspace %s (bookmark %s)", wsName, branch))
	name, wsPath, err := svc.PrepareWorkspace(wsName, branch, true)
	if err != nil {
		return fmt.Errorf("prepare workspace from bookmark %q: %w", branch, err)
	}
	if strings.TrimSpace(wsPath) == "" {
		return fmt.Errorf("workspace %q has empty path", name)
	}

	repoRoot, rerr := repoRootFromPath(wsPath)
	if rerr != nil {
		return rerr
	}
	project := filepath.Base(repoRoot)
	sessionName := DeckSessionName(project, name)

	// Write the just-fetched PR into the pr-status cache so the deck's
	// `p o` chord (and the row glyph) resolve immediately. Without this
	// write-through there's a race window between the new workspace
	// appearing in workspace-state.json and the next pr-status fetch
	// completing — during which prStatusLabelForItem returns "no PR".
	// The cache merge is the same writer the periodic job uses.
	if pr.URL != "" {
		// Viewer "" → review-requested signals false, which is accurate
		// here: the user is opening a review on this PR right now.
		status := prStatusFromGithub(github.PRStatusFromInfo(pr), false, "")
		persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
			repoRoot: {pr.HeadRef: status},
		}, time.Now())
		deckDebugLogf("review wrote-through pr=#%d head=%s repo=%s", pr.Number, pr.HeadRef, repoRoot)
		// Pin the workspace to this PR number so the lookup is direct
		// regardless of bookmark drift.
		if err := svc.RecordPROverride(name, pr.Number); err != nil {
			deckDebugLogf("review RecordPROverride err ws=%s pr=%d err=%v", name, pr.Number, err)
		}
	}

	reviewCmd := fmt.Sprintf("tuicr pr %d", pr.Number)
	prDescWindow := "pr description"
	prDescTarget := sessionName + ":" + prDescWindow
	prDescCmd := fmt.Sprintf("GH_FORCE_TTY=100%% gh pr view %d | less -R", pr.Number)

	exists, err := tmuxClient.SessionExists(sessionName)
	if err != nil {
		return err
	}
	env := workspaceEnvPairs(project, name, repoRoot)
	if !exists {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		if err := tmuxClient.NewSession(sessionName, wsPath, prDescWindow, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(prDescTarget, prDescCmd); err != nil {
			return err
		}
	} else {
		reporter.Log(fmt.Sprintf("tmux session %s already exists; ensuring review windows", sessionName))
	}

	// Add whichever of the three review windows is missing. Necessary
	// because the session may have been created out-of-band (e.g. an
	// earlier `enter` on the workspace row summons a session with only
	// an `agent` window) — without this idempotent setup, review.go
	// would attach to that bare session and leave the user without the
	// `pr description` and `review` windows.
	windows, werr := tmuxClient.ListWindowsInSession(sessionName)
	if werr != nil {
		return werr
	}
	have := make(map[string]bool, len(windows))
	for _, w := range windows {
		have[w.Name] = true
	}
	if !have[prDescWindow] {
		reporter.Step("Open PR description window")
		if err := tmuxClient.NewWindowInSession(sessionName, prDescWindow, wsPath, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(prDescTarget, prDescCmd); err != nil {
			return err
		}
	}
	// Open the review window *before* the agent so `tuicr pr <n>` has
	// a head start writing active_sessions.json. The agent prompt then
	// embeds the resolved session JSON path: tuicr's --repo-scoped
	// session lookup can't find PR-mode sessions from a local checkout
	// (repo_path is stored as forge:github.com/..., not a filesystem
	// path), so the agent has to pass --session <abs-path> instead.
	if !have["review"] {
		reporter.Step("Open review window")
		if err := tmuxClient.NewWindowInSession(sessionName, "review", wsPath, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(sessionName+":review", reviewCmd); err != nil {
			return err
		}
	}

	// TODO(tuicr#368): the slug + data-dir + JSON-file lookup chain is a
	// workaround for tuicr's --repo-scoped session resolution not
	// finding PR-mode sessions (their repo_path is "forge:github.com/...",
	// not a local path). Replace once tuicr exposes a stable agent
	// discovery protocol. https://github.com/agavra/tuicr/issues/368
	slug := tuicrSessionSlug(pr.URL, pr.Number)
	dataDir := tuicrDataDir()
	sessionPath := awaitTuicrSessionPath(context.Background(), dataDir, slug, sessionDiscoveryTimeout)
	if sessionPath != "" {
		reporter.Log(fmt.Sprintf("tuicr session: %s", sessionPath))
	} else if slug != "" {
		reporter.Log(fmt.Sprintf("tuicr session not yet registered for %s; agent will resolve at use time", slug))
	}
	diffRange := resolveDiffRange(runner, wsPath, base, pr.HeadSHA)
	// Pull existing PR comments so the agent doesn't re-raise points
	// reviewers (or bots) already made. Non-fatal: a review with no prior
	// comments is the common case, and a fetch error shouldn't block the
	// review — fall back to the empty list.
	reporter.Step(fmt.Sprintf("Fetch existing comments for PR #%d", prNumber))
	comments, cerr := gh.FetchPRComments(prNumber)
	if cerr != nil {
		reporter.Log(fmt.Sprintf("could not fetch existing comments: %v", cerr))
		comments = nil
	} else {
		reporter.Log(fmt.Sprintf("found %d existing comment(s)", len(comments)))
	}
	// Render the full review instructions, but don't paste them into the
	// agent terminal — that ~170-line block is what users complained was
	// "too big". Write it to disk and hand the agent a tiny pointer prompt
	// instead; the agent reads the file itself. Falls back to the inline
	// prompt if the write fails, so a read-only home dir still works.
	instructions := buildReviewPrompt(pr, base, diffRange, slug, sessionPath, dataDir, comments)
	prompt := instructions
	if promptPath, werr := writeReviewPromptFile(repoRoot, name, instructions); werr != nil {
		reporter.Log(fmt.Sprintf("could not write review prompt file (sending inline): %v", werr))
	} else {
		reporter.Log(fmt.Sprintf("review prompt: %s", promptPath))
		prompt = buildReviewPointerPrompt(pr, promptPath)
	}

	if !have["agent"] {
		reporter.Step("Open agent window")
		if err := tmuxClient.NewWindowInSession(sessionName, "agent", wsPath, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(sessionName+":agent", config.AgentInvocation(repoRoot)+" "+shellSingleQuote(prompt)); err != nil {
			return err
		}
	} else {
		// Agent window pre-existed (typically from a prior summon
		// that launched the default agent without a prompt). Feed the
		// review prompt to it as user input so the agent picks up the
		// review context. Use PasteText so the prompt's embedded
		// newlines don't each submit as separate messages — bracketed
		// paste lets the agent receive the whole block in one go.
		reporter.Step("Send review prompt to agent")
		if err := tmuxClient.PasteText(sessionName+":agent", prompt); err != nil {
			return err
		}
	}
	// Set the session's current-window pointer to pr-description so a
	// later switch into the session lands the user on the PR view
	// instead of whatever window was last focused (commonly `agent`,
	// when the session was summoned out-of-band before review ran).
	if err := tmuxClient.SwitchToWindow(prDescTarget); err != nil {
		return err
	}

	if noSwitch {
		return nil
	}
	reporter.Step(fmt.Sprintf("Switch to %s", sessionName))
	if err := tmuxClient.SwitchClient(sessionName); err != nil {
		return err
	}
	return nil
}

// repoRootFromPath walks up from a workspace path to find the jj repo root (contains .jj).
// For secondary jj workspaces, .jj/repo is a file whose contents point to the main repo's
// .jj/repo directory; follow that pointer so the result is the source repo root, not the
// workspace dir (otherwise filepath.Base would return the workspace/branch name).
func repoRootFromPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		jjDir := filepath.Join(dir, ".jj")
		if st, err := os.Stat(jjDir); err == nil && st.IsDir() {
			repoEntry := filepath.Join(jjDir, "repo")
			rst, rerr := os.Stat(repoEntry)
			if rerr == nil && rst.IsDir() {
				return dir, nil
			}
			if rerr == nil && !rst.IsDir() {
				data, ferr := os.ReadFile(repoEntry)
				if ferr != nil {
					return "", fmt.Errorf("read %s: %w", repoEntry, ferr)
				}
				target := strings.TrimSpace(string(data))
				if !filepath.IsAbs(target) {
					target = filepath.Join(jjDir, target)
				}
				// target is .../<mainRepo>/.jj/repo — main repo root is two levels up.
				mainRepo := filepath.Clean(filepath.Join(target, "..", ".."))
				return mainRepo, nil
			}
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate jj repo root above %s", abs)
		}
		dir = parent
	}
}

// pickPRNumber lists open PRs via gh and prompts the user to pick one.
func pickPRNumber(runner Runner, picker workspacePicker) (int, error) {
	if runner == nil {
		runner = NewExecRunner()
	}
	gh := github.New(runner)
	prs, err := gh.ListPRs()
	if err != nil {
		return 0, err
	}
	if len(prs) == 0 {
		return 0, fmt.Errorf("no open PRs found")
	}
	options := make([]string, 0, len(prs))
	byLabel := make(map[string]int, len(prs))
	for _, pr := range prs {
		draft := ""
		if pr.IsDraft {
			draft = " [draft]"
		}
		author := pr.Author.Login
		if author == "" {
			author = "?"
		}
		label := fmt.Sprintf("#%d%s %s (@%s, %s)", pr.Number, draft, pr.Title, author, pr.HeadRef)
		options = append(options, label)
		byLabel[label] = pr.Number
	}
	selected, err := picker("Select PR to review", options)
	if err != nil {
		return 0, err
	}
	n, ok := byLabel[strings.TrimSpace(selected)]
	if !ok {
		return 0, fmt.Errorf("picker returned unknown label %q", selected)
	}
	return n, nil
}

// writeReviewPromptFile renders the full review instructions to
// ~/.awp/review-prompts/<repo>/<workspace>.md (see config.ReviewPromptPath)
// and returns its absolute path. The file lives outside the review
// workspace on purpose: a review workspace's own .awp/ is replaced with a
// symlink to the shared source-repo .awp during prep, so writing the prompt
// there would make it shared across every review and clobbered by the next
// one. Keying by repo + workspace name keeps each review's prompt private
// (even when workspace names collide across repos) and lets workspace
// delete/prune remove it. The agent receives only the short pointer prompt
// from buildReviewPointerPrompt and reads this file itself, keeping the
// terminal-pasted prompt tiny.
func writeReviewPromptFile(repoRoot, wsName, content string) (string, error) {
	path := config.ReviewPromptPath(repoRoot, wsName)
	if path == "" {
		return "", fmt.Errorf("empty workspace name for review prompt")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create review prompt dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write review prompt: %w", err)
	}
	return path, nil
}

// buildReviewPointerPrompt is the short prompt actually sent to the agent.
// It names the PR and points at the on-disk instructions file rather than
// inlining the full review guide.
func buildReviewPointerPrompt(pr github.PRInfo, promptPath string) string {
	title := strings.TrimSpace(pr.Title)
	if title == "" {
		title = "(no title)"
	}
	return fmt.Sprintf(`Review PR #%d: %s

Your full review instructions and PR context are in this file:

    %s

Read that file first, then post your review comments via tuicr exactly as it describes.`, pr.Number, title, promptPath)
}

func buildReviewPrompt(pr github.PRInfo, base, diffRange, slug, sessionPath, dataDir string, comments []github.PRComment) string {
	body := strings.TrimSpace(pr.Body)
	if body == "" {
		body = "(no description)"
	}
	if strings.TrimSpace(diffRange) == "" {
		diffRange = base + "..@"
	}
	if strings.TrimSpace(slug) == "" {
		slug = "(unknown — gh: prefixed slug could not be derived from PR URL)"
	}
	pathField := sessionPath
	if strings.TrimSpace(pathField) == "" {
		pathField = "(not resolved — use `tuicr review list` to find it; see below)"
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "<unknown>"
	}
	ownerRepo := "<owner>/<repo>"
	if owner, repo := ownerRepoFromPRURL(pr.URL); owner != "" && repo != "" {
		ownerRepo = owner + "/" + repo
	}
	return strings.NewReplacer(
		"{{number}}", strconv.Itoa(pr.Number),
		"{{title}}", pr.Title,
		"{{body}}", body,
		"{{base}}", base,
		"{{diff_range}}", diffRange,
		"{{slug}}", slug,
		"{{session_path}}", pathField,
		"{{data_dir}}", dataDir,
		"{{owner_repo}}", ownerRepo,
		"{{comments}}", formatExistingComments(comments),
	).Replace(reviewPromptTemplate)
}

// formatExistingComments renders the PR's existing comments as a compact
// markdown list for the {{comments}} slot, so the reviewing agent can see
// what's already been raised and avoid restating it. Returns a sentinel
// line when there are none.
func formatExistingComments(comments []github.PRComment) string {
	if len(comments) == 0 {
		return "(none — no prior comments on this PR)"
	}
	var b strings.Builder
	for _, c := range comments {
		author := c.Author
		if author == "" {
			author = "unknown"
		}
		switch c.Kind {
		case "inline":
			loc := c.Path
			if c.Line > 0 {
				loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
			}
			fmt.Fprintf(&b, "- inline @%s [%s]: %s\n", author, loc, oneLine(c.Body))
		case "review":
			fmt.Fprintf(&b, "- review @%s: %s\n", author, oneLine(c.Body))
		default:
			fmt.Fprintf(&b, "- comment @%s: %s\n", author, oneLine(c.Body))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine collapses a comment body to a single line so the comment list
// stays scannable. Internal newlines become " / ".
func oneLine(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	for i, f := range fields {
		fields[i] = strings.TrimSpace(f)
	}
	return strings.Join(fields, " / ")
}

// resolveDiffRange returns the commit-SHA range that mirrors what tuicr
// shows in the review pane: <merge-base(origin/base, headSHA)>..<headSHA>.
// Pre-baking SHAs into the prompt avoids the failure mode where the
// agent computes `<baseRef>..@` against a branch whose origin/<base>
// has drifted far ahead, producing a diff full of unrelated upstream
// churn.
//
// We pass headSHA explicitly (from `gh pr view --json headRefOid`)
// instead of resolving the workspace's HEAD, because jj workspaces
// don't always align HEAD with the PR head — `jj workspace add` may
// land at a different revision (commonly the source repo's tip) and
// reviewing against that gives the wrong range.
//
// Falls back to "<baseRef>..@" when any input is missing or git errors
// out (e.g. the workspace doesn't have origin/<base> fetched, the head
// SHA isn't reachable locally yet). Functional but imprecise — the
// agent's prompt will still steer it toward the right files via tuicr.
func resolveDiffRange(runner Runner, wsPath, baseRef, headSHA string) string {
	base := strings.TrimSpace(baseRef)
	head := strings.TrimSpace(headSHA)
	if base == "" || head == "" || runner == nil || strings.TrimSpace(wsPath) == "" {
		return base + "..@"
	}
	ctx := context.Background()
	// Prefer the remote-tracking ref so we follow GitHub's view of the
	// base. Fall back to the bare ref name (may be local-only in some
	// jj setups) before giving up.
	for _, cand := range []string{"origin/" + base, base} {
		mbOut, err := runner.Run(ctx, wsPath, "git", "merge-base", cand, head)
		if err != nil {
			continue
		}
		mb := strings.TrimSpace(mbOut)
		if mb != "" {
			return mb + ".." + head
		}
	}
	return base + "..@"
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// forkFetchURLFromOrigin builds a git fetch URL for <headOwner>/<headName>
// that mirrors the user's existing `origin` URL format — same scheme,
// host, and user — so their git auth keeps working. Hard-coding
// `https://github.com/...` would prompt for a username on private
// forks (the `Device not configured: fatal: could not read Username`
// failure we just hit) when the user's auth is SSH-key based.
//
// Falls back to `https://github.com/<owner>/<repo>.git` when origin
// can't be resolved or parsed — same behavior as the previous
// implementation, just used as a last resort.
func forkFetchURLFromOrigin(runner Runner, repoRoot, headOwner, headName string) string {
	fallback := fmt.Sprintf("https://github.com/%s/%s.git", headOwner, headName)
	out, err := runner.Run(context.Background(), repoRoot, "git", "remote", "get-url", "origin")
	if err != nil {
		return fallback
	}
	return rewriteFetchURL(strings.TrimSpace(out), headOwner, headName, fallback)
}

// rewriteFetchURL replaces the path/repo portion of `origin` with
// `<owner>/<name>.git`, preserving the URL's scheme/host/user so the
// caller's git credentials still apply. Returns `fallback` for any
// origin that doesn't match a known form.
//
// Recognized inputs:
//   - URL form:  ssh://git@host/old/repo[.git]   → ssh://git@host/<owner>/<name>.git
//     https://host/old/repo[.git]     → https://host/<owner>/<name>.git
//   - SCP form:  git@host:old/repo.git           → git@host:<owner>/<name>.git
func rewriteFetchURL(origin, owner, name, fallback string) string {
	owner = strings.TrimSpace(owner)
	name = strings.TrimSuffix(strings.TrimSpace(name), ".git")
	if origin == "" || owner == "" || name == "" {
		return fallback
	}
	target := owner + "/" + name + ".git"

	// URL form: prefer net/url for ssh://, https://, http://, git://.
	if strings.Contains(origin, "://") {
		u, err := url.Parse(origin)
		if err == nil && u.Host != "" {
			u.Path = "/" + target
			u.RawQuery = ""
			u.Fragment = ""
			return u.String()
		}
		return fallback
	}

	// SCP form: <user>@<host>:<path>. Detect by requiring an '@'
	// before the first ':' and no '://' (handled above).
	if at := strings.IndexByte(origin, '@'); at > 0 {
		rest := origin[at+1:]
		if colon := strings.IndexByte(rest, ':'); colon > 0 {
			host := rest[:colon]
			user := origin[:at]
			return fmt.Sprintf("%s@%s:%s", user, host, target)
		}
	}

	return fallback
}

func parsePRNumber(arg string) (int, error) {
	arg = strings.TrimSpace(arg)
	arg = strings.TrimPrefix(arg, "#")
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("invalid PR number %q", arg)
	}
	return n, nil
}

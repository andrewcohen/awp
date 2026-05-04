package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jobs"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// runRunJob is the entrypoint for `awp run-job <id>`. It looks up the
// pending record in the store, dispatches by action to the existing
// handler code, and writes status updates back to the record.
//
// The subprocess is detached (Setsid: true at spawn). It catches
// SIGTERM/SIGINT/SIGHUP to flush a `cancelled` final state, and uses
// a deferred guard to record `error: exited without finalizing` if
// the action returns without a terminal mark (defensive — every
// action path below should explicitly call MarkDone first).
func runRunJob(svc workspace.Service, runner Runner, args []string) error {
	if len(args) < 1 {
		return errors.New("run-job requires a job id")
	}
	id := jobs.JobID(args[0])

	store, err := jobs.NewStore()
	if err != nil {
		return fmt.Errorf("open job store: %w", err)
	}
	job, err := store.Get(id)
	if err != nil {
		return fmt.Errorf("load job %s: %w", id, err)
	}

	// Track terminality so the SIGTERM handler and the deferred
	// guard don't double-write. Once any branch transitions to a
	// terminal state, set this flag and don't transition again.
	var terminal atomic.Bool
	finalize := func(status jobs.JobStatus, msg string) {
		if !terminal.CompareAndSwap(false, true) {
			return
		}
		_ = store.MarkDone(id, status, msg)
	}

	// Signal handlers: any of these should flush `cancelled` and
	// exit. We use a buffered channel + non-blocking goroutine so
	// repeated signals don't deadlock.
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		s := <-sigCh
		finalize(jobs.StatusCancelled, fmt.Sprintf("received %s", s))
		os.Exit(130)
	}()
	defer func() {
		if r := recover(); r != nil {
			finalize(jobs.StatusError, fmt.Sprintf("panic: %v", r))
			panic(r)
		}
		// Ensure we never leave a `running` record behind even if a
		// dispatch path forgot to mark a terminal state.
		finalize(jobs.StatusError, "process exited without finalizing")
	}()

	// Heartbeat keeps LastHeartbeat fresh so the deck-side orphan
	// detection only fires when we genuinely die.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go jobs.NewHeartbeater(store, id, 0).Run(ctx)

	if err := store.MarkRunning(id); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	reporter := &storeReporter{store: store, id: id}

	// Use the workspace.Service the parent process passed in only
	// when the spec carries no repo_root override. For deck-spawned
	// jobs the spec always pins a repo_root so each subprocess
	// builds its own service rooted at that repo (mirrors how the
	// deck handler does it today).
	actionSvc := svc
	if r := job.Spec.RepoRoot; r != "" {
		actionSvc = newDeckActionService(runner, r, os.Stdin)
	}

	switch job.Spec.Action {
	case jobs.ActionCreateWorkspace:
		err = runCreateWorkspaceJob(runner, actionSvc, job, reporter)
	case jobs.ActionDelete:
		err = runDeleteJob(runner, actionSvc, job, reporter)
	case jobs.ActionReview:
		err = runReviewJob(runner, actionSvc, job, reporter)
	case jobs.ActionCustom:
		err = runCustomJob(job, reporter)
	default:
		err = fmt.Errorf("unsupported job action %q", job.Spec.Action)
	}

	if err != nil {
		finalize(jobs.StatusError, err.Error())
		return err
	}
	finalize(jobs.StatusDone, "")
	return nil
}

// runReviewJob runs PR review setup detached. Uses runReviewAsync so
// the final SwitchClient is suppressed — the user navigates to the
// new tmux session manually when ready.
func runReviewJob(runner Runner, svc workspace.Service, job jobs.Job, reporter *storeReporter) error {
	prNum, err := strconv.Atoi(job.Spec.Arg)
	if err != nil {
		return fmt.Errorf("review: invalid PR number %q", job.Spec.Arg)
	}
	dir := job.Spec.RepoRoot
	fr := fixedDirRunner{base: runner, dir: dir}
	return runReviewAsync(fr, svc, prNum, reporter)
}

// runDeleteJob delegates to handleDeckAction's delete branch with
// inputs reconstituted from the job spec. The tmux client inherits
// TMUX from the deck-spawned subprocess so it talks to the same
// tmux server.
func runDeleteJob(runner Runner, svc workspace.Service, job jobs.Job, reporter *storeReporter) error {
	tmuxClient := tmux.New(runner)
	item := deckui.Item{
		ProjectName:   filepath.Base(job.Spec.RepoRoot),
		WorkspaceName: job.Spec.WorkspaceName,
		Path:          job.Spec.WorkspacePath,
		RepoRoot:      job.Spec.RepoRoot,
	}
	return handleDeckAction(tmuxClient, svc, runner, deckui.ActionRequest{
		Item:     item,
		Action:   deckui.ActionDelete,
		Reporter: reporter,
	}, reporter)
}

// runCreateWorkspaceJob runs a workspace-creation job. It performs the
// full setup — jj workspace, tmux session, agent window, prompt
// delivery — but skips the final switch-client so the user's tmux
// focus stays with the deck. They summon the new workspace by
// pressing enter on it in the deck list once it appears.
func runCreateWorkspaceJob(runner Runner, svc workspace.Service, job jobs.Job, reporter *storeReporter) error {
	dir := job.Spec.RepoRoot
	fr := fixedDirRunner{base: runner, dir: dir}
	return openWorkspaceWithReporter(fr, svc, openRequest{
		Name:     job.Spec.Name,
		Bookmark: job.Spec.Bookmark,
		Prompt:   job.Spec.Prompt,
		Yes:      true,
		NoSwitch: true,
	}, reporter)
}

// storeReporter implements deckui.Reporter by writing Step/Log events
// to the job store.
type storeReporter struct {
	store *jobs.Store
	id    jobs.JobID
}

func (r *storeReporter) Step(label string) {
	_ = r.store.AppendStep(r.id, label)
}

func (r *storeReporter) Log(line string) {
	_ = r.store.AppendLog(r.id, line)
}

// fileLogger is a small io.Writer that mirrors the subprocess's
// stdout/stderr (already pointed at the sidecar log file by the
// parent's spawn) into the inline log buffer for tray display.
// Currently unused — keeping a sketch here for when we want to
// stream subprocess child-process output (e.g. jj git fetch) into
// the tray. Wire by passing into workspace.Service via a new option.
type fileLogger struct {
	mirror io.Writer
}

func (f fileLogger) Write(p []byte) (int, error) {
	if f.mirror == nil {
		return len(p), nil
	}
	return f.mirror.Write(p)
}

// runCustomJob runs a background user action (config.UserAction with
// Background=true). Spec.Arg holds the action name; we resolve it
// against the config rooted at Spec.RepoRoot and exec the command via
// `sh -c` in Spec.WorkspacePath. Stdout/stderr are streamed line-by-line
// into the job log via reporter.Log so the deck's overlay/log file
// stays useful.
func runCustomJob(job jobs.Job, reporter *storeReporter) error {
	name := job.Spec.Arg
	cfg, err := config.Load(job.Spec.RepoRoot)
	if err != nil {
		return fmt.Errorf("custom: load config: %w", err)
	}
	ua, ok := cfg.Actions[name]
	if !ok {
		return fmt.Errorf("custom: unknown user action %q", name)
	}
	cmd := ua.Command
	if cmd == "" {
		return fmt.Errorf("custom: action %q has no command", name)
	}
	dir := job.Spec.WorkspacePath
	if dir == "" {
		dir = job.Spec.RepoRoot
	}
	reporter.Step(fmt.Sprintf("Run %s", name))
	c := exec.Command("sh", "-c", cmd)
	c.Dir = dir
	stdout, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}
	streamLines := func(r io.Reader) {
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for s.Scan() {
			reporter.Log(s.Text())
		}
	}
	done := make(chan struct{}, 2)
	go func() { streamLines(stdout); done <- struct{}{} }()
	go func() { streamLines(stderr); done <- struct{}{} }()
	<-done
	<-done
	if err := c.Wait(); err != nil {
		if ee, isExit := err.(*exec.ExitError); isExit {
			return fmt.Errorf("exit %d", ee.ExitCode())
		}
		return err
	}
	return nil
}

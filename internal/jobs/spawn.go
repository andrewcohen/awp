package jobs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// SpawnOptions configures a Spawn call. Most callers pass zero values
// and let Spawn use sensible defaults.
type SpawnOptions struct {
	// Binary is the executable to launch. Defaults to the current
	// process (os.Executable()), so a `awp deck` spawning a job
	// re-execs the same `awp` binary.
	Binary string

	// Args are the arguments passed to the binary. The first element
	// (subcommand) defaults to ["run-job", "<id>"] when empty.
	Args []string

	// Now lets tests inject a fixed clock.
	Now func() time.Time
}

// Spawn writes a pending job record to the store, then forks the
// run-job subprocess detached from the caller (Setsid: true, stdin
// to /dev/null, stdout/stderr to the sidecar log file). The returned
// Job has its PID and PIDStartedAt populated. The caller's process
// can exit immediately afterwards without affecting the subprocess.
func (s *Store) Spawn(spec Spec, title string, opts SpawnOptions) (Job, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	id, err := NewID(now())
	if err != nil {
		return Job{}, err
	}
	host, _ := os.Hostname()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return Job{}, fmt.Errorf("create jobs dir: %w", err)
	}

	pending := Job{
		ID:            id,
		Title:         title,
		Spec:          spec,
		Host:          host,
		Status:        StatusPending,
		StartedAt:     now(),
		LastHeartbeat: now(),
		LogFile:       s.LogPath(id),
	}
	if err := s.Save(pending); err != nil {
		return Job{}, fmt.Errorf("save pending: %w", err)
	}

	binary := opts.Binary
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return Job{}, fmt.Errorf("resolve self executable: %w", err)
		}
		binary = exe
	}
	args := opts.Args
	if len(args) == 0 {
		args = []string{"run-job", string(id)}
	}

	logFile, err := os.OpenFile(s.LogPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = s.Delete(id)
		return Job{}, fmt.Errorf("open job log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		_ = s.Delete(id)
		return Job{}, fmt.Errorf("open /dev/null: %w", err)
	}
	defer func() { _ = devNull.Close() }()

	cmd := exec.Command(binary, args...)
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachAttrs()
	// Inherit the deck's environment (so user's PATH, $HOME, etc. are
	// available to jj/tmux/git invocations inside the subprocess).
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		_ = s.Delete(id)
		return Job{}, fmt.Errorf("start run-job: %w", err)
	}

	// Captured before the reaper goroutine below can run, since Wait
	// invalidates cmd.Process. These go into the record so orphan
	// detection has something to compare against later.
	pid := cmd.Process.Pid
	startTime, _ := PIDStartTime(pid)

	if err := s.Update(id, func(j *Job) error {
		j.PID = pid
		j.PIDStartedAt = startTime
		return nil
	}); err != nil {
		// The subprocess is already running; failing to record its
		// pid is unfortunate but not fatal — the subprocess will
		// still write its own status updates.
		return Job{}, fmt.Errorf("record pid: %w", err)
	}

	// Reap it when it ends. Detaching is about who the child *obeys* —
	// Setsid gives it its own session, so it survives the deck and takes
	// no signal sent to the deck's group — and none of that changes who
	// its parent is. Until the parent waits, an exited child stays a
	// zombie in the process table.
	//
	// This used to call cmd.Process.Release() instead, on the theory that
	// letting go of the handle handed the child to init. It does not: it
	// only lets Go's finalizer free the struct. Measured, a deck spawning
	// a pr-status job every few seconds accumulated one <defunct> child
	// per spawn — several hundred inside an hour — because nothing here
	// ever waited on one.
	//
	// A goroutine per live job, blocked on Wait, is what that costs. It
	// holds nothing else: the log file is the child's own descriptor, and
	// Wait closes only pipes it opened itself, of which there are none.
	go func() { _ = cmd.Wait() }()

	out, err := s.Get(id)
	if err != nil {
		return Job{}, err
	}
	return out, nil
}

// SignalCancel sends SIGTERM to the job's subprocess so it has a
// chance to flush a `cancelled` final record before exiting. Returns
// an error if the record isn't found, the host doesn't match (we
// won't kill arbitrary pids on the wrong machine), or the signal
// fails. ESRCH (process already gone) is treated as success — the
// goal state is reached.
func (s *Store) SignalCancel(id JobID) error {
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	if j.PID <= 0 {
		return errors.New("job has no recorded pid")
	}
	host, _ := os.Hostname()
	if j.Host != "" && host != "" && j.Host != host {
		return fmt.Errorf("job belongs to host %q, not %q", j.Host, host)
	}
	if err := syscall.Kill(j.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("kill: %w", err)
	}
	return nil
}

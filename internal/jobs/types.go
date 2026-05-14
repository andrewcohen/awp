// Package jobs implements the file-backed job system that powers async
// (detached) deck actions. A job is a long-running command (workspace
// creation, review, CI watch, custom action) that runs as a subprocess
// of `awp run-job <id>` and writes status updates to a JSON file under
// ~/.awp/jobs/. The deck reads those files and renders progress; the
// subprocess survives the deck and any number of deck instances can
// observe the same job set.
package jobs

import "time"

// JobID identifies a single job. Format: YYYYMMDD-<rand4>, matching the
// spec ID convention in CLAUDE.md.
type JobID string

// JobAction names the kind of work a job performs. The run-job
// subcommand dispatches on this value.
type JobAction string

const (
	ActionCreateWorkspace JobAction = "create-workspace"
	ActionReview          JobAction = "review"
	ActionCI              JobAction = "ci"
	ActionCustom          JobAction = "custom"
	ActionDelete          JobAction = "delete"
	ActionDeleteProject   JobAction = "delete-project"
	ActionPRStatus        JobAction = "pr-status"
)

// JobStatus is the lifecycle state of a job. Terminal states are
// {done, error, cancelled, orphaned}.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusDone      JobStatus = "done"
	StatusError     JobStatus = "error"
	StatusCancelled JobStatus = "cancelled"
	StatusOrphaned  JobStatus = "orphaned"
)

// IsTerminal reports whether the status is a final state that the
// subprocess will not transition out of.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case StatusDone, StatusError, StatusCancelled, StatusOrphaned:
		return true
	}
	return false
}

// StepState mirrors deckui.ProgressStepState for serialization.
type StepState string

const (
	StepRunning StepState = "running"
	StepDone    StepState = "done"
	StepError   StepState = "error"
)

// Step is one named phase reported by the subprocess.
type Step struct {
	Label string    `json:"label"`
	State StepState `json:"state"`
}

// Spec is the input payload to a job: enough information for the
// run-job subprocess to do the work without consulting the deck.
type Spec struct {
	Action   JobAction `json:"action"`
	RepoRoot string    `json:"repo_root,omitempty"`

	// Create-workspace fields.
	Name     string `json:"name,omitempty"`
	Bookmark string `json:"bookmark,omitempty"`
	Prompt   string `json:"prompt,omitempty"`

	// Generic action arg (PR number for review, action name for custom).
	Arg string `json:"arg,omitempty"`

	// Workspace context for actions that operate on a specific workspace.
	WorkspaceName string `json:"workspace_name,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`

	// Repos lists repo roots the action operates over. Populated by
	// ActionPRStatus (one gh-pr-list call per entry).
	Repos []string `json:"repos,omitempty"`
}

// Job is the persisted record on disk at ~/.awp/jobs/<id>.json.
type Job struct {
	ID    JobID  `json:"id"`
	Title string `json:"title"`
	Spec  Spec   `json:"spec"`

	// Identification fields used for orphan detection. Host scopes
	// the pid to a machine; PIDStartedAt guards against pid reuse.
	Host         string  `json:"host,omitempty"`
	PID          int     `json:"pid,omitempty"`
	PIDStartedAt float64 `json:"pid_started_at,omitempty"`

	Status        JobStatus  `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	LastHeartbeat time.Time  `json:"last_heartbeat,omitempty"`

	Steps      []Step   `json:"steps,omitempty"`
	LogsInline []string `json:"logs_inline,omitempty"`
	LogFile    string   `json:"log_file,omitempty"`
	ErrMsg     string   `json:"error,omitempty"`
}

// IsActive reports whether the job is still in flight (pending or
// running and not orphaned).
func (j Job) IsActive() bool {
	return j.Status == StatusPending || j.Status == StatusRunning
}

// MaxInlineLogLines caps the number of stdout/stderr lines we
// retain inside the JSON record. Beyond this we keep only the tail.
// Full output goes to the sidecar log file.
const MaxInlineLogLines = 200

// HeartbeatInterval is how often a running subprocess refreshes its
// LastHeartbeat timestamp.
const HeartbeatInterval = 5 * time.Second

// HeartbeatStale is the threshold past which a record's heartbeat is
// considered stale enough to trigger a pid liveness check on the
// deck side. Set generously to absorb transient subprocess pauses
// (GC, syscalls under load).
const HeartbeatStale = 30 * time.Second

// RetentionDone is how long terminal records persist before GC.
const RetentionDone = 24 * time.Hour

// RetentionOrphaned is how long orphan records persist (longer so
// the user has a chance to notice and debug).
const RetentionOrphaned = 7 * 24 * time.Hour

package watch

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GateState is the observed status of one gate in the current unit of work.
type GateState struct {
	Name  string
	Phase string
	// Result is "", "pass", or "fail" — the last observed outcome.
	Result string
	// RedCount is how many times this gate has been observed failing.
	// A non-zero count on the current unit is the churn / loop-back signal.
	RedCount int
}

// Todo is one item from the agent's TodoWrite list.
type Todo struct {
	Content string
	Status  string // pending | in_progress | completed
}

// State is the combined task view for a single workspace at a point in time.
type State struct {
	AgentStatus  string // from workspace-state.json (working/waiting/idle/…)
	CurrentPhase string
	Todos        []Todo
	Gates        []GateState
	UnitStart    time.Time // when the current in_progress todo began
	LastActivity time.Time // timestamp of the last tool event
	Now          time.Time
}

// CurrentUnit returns the index of the in_progress todo, or -1 if none.
func (s State) CurrentUnit() int {
	for i, t := range s.Todos {
		if t.Status == "in_progress" {
			return i
		}
	}
	return -1
}

// DoneCount returns how many todos are completed.
func (s State) DoneCount() int {
	n := 0
	for _, t := range s.Todos {
		if t.Status == "completed" {
			n++
		}
	}
	return n
}

// --- transcript line/block shapes -------------------------------------------

type rawLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	// text (assistant prose — used to detect "Unit N: …" announcements)
	Text string `json:"text"`
}

// unitAnnounce matches the agent announcing a unit of work, as the dev-loop
// preamble instructs ("Unit N: <what>"). It is the organic breadth signal
// when the agent emits no TodoWrite list.
var unitAnnounce = regexp.MustCompile(`(?im)^[\s*_>#-]*unit\s+(\d+)\s*[:.\-–—]\s*(.+)`)

// BuildState scans a transcript from the top and derives the combined
// todos+loop state.
//
// A full replay each call, which is what a one-off caller (`awp watch`, a test)
// wants. The deck refreshes every few seconds against transcripts that reach
// hundreds of megabytes, so it uses a Reader instead and folds only what the
// agent has written since the last pass — see Reader, and #186 for the profile
// that made the difference 25% of the deck's CPU.
func BuildState(loop Loop, transcriptPath, agentStatus string, now time.Time) (State, error) {
	return NewReader(transcriptPath).State(loop, agentStatus, now)
}

// Reader folds one transcript incrementally, keeping the accumulated state and
// the offset it stopped at so a later call folds only what the agent has written
// since.
//
// The deck asks every workspace with a live agent for its dev-loop summary every
// few seconds. Re-reading from byte zero made that cost proportional to the
// length of the whole session rather than to what just happened: a profile of a
// real zdeck session spent 25% of the process in this fold, ~50 MB/s of JSON,
// against a transcript that had reached 251 MB — plus most of the scheduler
// churn around it. A transcript is append-only, so the fold can simply carry on.
//
// One Reader per transcript path, held by the caller across refreshes. Not safe
// for concurrent use: the deck folds each row's transcript in its own goroutine,
// and each row has its own Reader.
type Reader struct {
	path string
	// offset is the end of the last complete line folded. A line without its
	// newline yet is a write in progress, so it is left for the next pass rather
	// than folded half-parsed and skipped forever.
	offset int64
	fold   *folder
}

// NewReader starts a fold at the top of the named transcript.
func NewReader(path string) *Reader {
	return &Reader{path: path, fold: newFolder()}
}

// Path is the transcript this Reader is following.
func (r *Reader) Path() string { return r.path }

// State folds whatever is new and derives the current answer.
//
// Cheap when nothing has been written — the file's size is compared to the
// offset and no read happens at all, which is the common case for a deck row
// whose agent is thinking.
func (r *Reader) State(loop Loop, agentStatus string, now time.Time) (State, error) {
	if err := r.advance(loop); err != nil {
		return State{}, err
	}
	return r.fold.state(loop, agentStatus, now), nil
}

// advance folds the bytes past the offset.
func (r *Reader) advance(loop Loop) error {
	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < r.offset {
		// Shorter than what has already been folded, so it is not the same file
		// any more — truncated, or a new session written over the same path.
		// Starting again is the only honest answer; continuing would fold the new
		// content on top of a state describing content that no longer exists.
		r.offset, r.fold = 0, newFolder()
	}
	if info.Size() == r.offset {
		return nil
	}
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			// Either EOF or a read error, and in both cases what came back is a
			// line with no newline — not yet a line. The offset stays behind it.
			return nil
		}
		r.offset += int64(len(line))
		r.fold.line(loop, line)
	}
}

// folder is the mutable half of the fold: everything the scan accumulates as it
// walks the transcript, and nothing that is derived from it afterwards.
//
// It is a struct rather than a closure's locals because the fold has to be
// resumable. A transcript is append-only, so folding a line is the same
// operation whether it is line 1 or line 900,000 — but only if the state the
// fold carries can outlive the loop that produced it. That is the whole trick
// behind Reader.
type folder struct {
	// st is the raw accumulated state. Fields derived at the end (the resolved
	// phase, the emitted gate list, the fallback todo lists) do not live here —
	// State copies it and derives them, so folding one more line later still
	// starts from what the transcript actually said.
	st      State
	gates   map[string]*GateState
	pending map[string]string // tool_use ID -> gate name

	currentTodo string
	started     bool           // has implementation begun in the current unit?
	units       map[int]string // announced "Unit N: desc"
	maxUnit     int
	// TaskCreate/TaskUpdate reconstruction — the todo tool in this
	// environment. IDs are assigned in creation order (matching "Task #N").
	taskByID    map[string]*Todo
	taskOrder   []string
	taskCreates int
	currentTask string
	checklist   []Todo // latest markdown "- [x]" checklist snapshot
}

func newFolder() *folder {
	return &folder{
		gates:    map[string]*GateState{},
		pending:  map[string]string{},
		units:    map[int]string{},
		taskByID: map[string]*Todo{},
	}
}

// resetUnit clears per-unit state when a new unit begins, so gate lights
// and the loop phase reflect only the current unit's work — not results
// carried over from earlier units in the same session.
func (f *folder) resetUnit(ts time.Time) {
	for k := range f.gates {
		delete(f.gates, k)
	}
	for k := range f.pending {
		delete(f.pending, k)
	}
	f.st.CurrentPhase = ""
	f.started = false
	if !ts.IsZero() {
		f.st.UnitStart = ts
	}
}

// line folds one transcript line. A line that is not JSON is skipped, which is
// what a partially-flushed write looks like from here.
func (f *folder) line(loop Loop, raw []byte) {
	st := &f.st
	gates, pending, units := f.gates, f.pending, f.units
	taskByID := f.taskByID
	resetUnit := f.resetUnit
	var ln rawLine
	if err := json.Unmarshal(raw, &ln); err != nil {
		return
	}
	blocks := decodeBlocks(ln.Message.Content)
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			switch b.Name {
			case "TaskCreate":
				var in struct {
					Subject string `json:"subject"`
					Content string `json:"content"`
				}
				_ = json.Unmarshal(b.Input, &in)
				subject := strings.TrimSpace(in.Subject)
				if subject == "" {
					subject = strings.TrimSpace(in.Content)
				}
				if subject == "" {
					// A subjectless TaskCreate fails validation (the tool
					// requires `subject`) and creates nothing — e.g. the
					// batch {"tasks":[…]} form an agent might try first.
					// Skipping it avoids minting a phantom empty task and,
					// crucially, keeps the synthetic ids aligned with the
					// tool's own "Task #N" numbering so a later
					// TaskUpdate(taskId) targets the right task.
					break
				}
				f.taskCreates++
				id := strconv.Itoa(f.taskCreates)
				taskByID[id] = &Todo{Content: subject, Status: "pending"}
				f.taskOrder = append(f.taskOrder, id)
			case "TaskUpdate":
				var in struct {
					TaskID  string `json:"taskId"`
					Status  string `json:"status"`
					Subject string `json:"subject"`
				}
				_ = json.Unmarshal(b.Input, &in)
				if t := taskByID[in.TaskID]; t != nil {
					if in.Subject != "" {
						t.Content = in.Subject
					}
					if in.Status != "" {
						t.Status = in.Status
						if in.Status == "in_progress" && in.TaskID != f.currentTask {
							f.currentTask = in.TaskID
							resetUnit(ln.Timestamp)
						}
						// Finishing the current unit resets the loop for the
						// next one: clear the gate lights and drop back toward
						// implement, so later work — even ad-hoc, un-tracked
						// edits — doesn't inherit the completed unit's stale
						// green gates.
						if in.Status == "completed" && in.TaskID == f.currentTask {
							f.currentTask = ""
							resetUnit(ln.Timestamp)
						}
					}
				}
			default:
				// A task list "exists" once any TaskCreate has landed or a
				// live TodoWrite list is present — that's the boundary past
				// explore.
				hasTasks := f.taskCreates > 0 || len(st.Todos) > 0
				handleToolUse(loop, b, ln.Timestamp, st, gates, pending, &f.currentTodo, &f.started, hasTasks, resetUnit)
			}
			if !ln.Timestamp.IsZero() {
				st.LastActivity = ln.Timestamp
			}
		case "tool_result":
			if name, ok := pending[b.ToolUseID]; ok {
				g := gates[name]
				if b.IsError {
					g.Result = "fail"
					g.RedCount++
				} else {
					g.Result = "pass"
				}
				delete(pending, b.ToolUseID)
			}
		case "text":
			// A markdown checklist the agent renders in prose is a breadth
			// fallback (below the task tool). Latest snapshot wins.
			if items := parseChecklist(b.Text); len(items) >= 2 {
				f.checklist = items
			}
			// Prose "Unit N:" announcements are only a breadth source when
			// the agent isn't using the task tool. If tasks are in play,
			// ignore prose mentions — otherwise the agent's own commentary
			// ("Unit 8: …") triggers false unit boundaries and wipes the
			// current unit's gate state.
			if f.taskCreates == 0 {
				if m := unitAnnounce.FindStringSubmatch(b.Text); m != nil {
					num := atoi(m[1])
					units[num] = firstLine(m[2])
					if num > f.maxUnit {
						f.maxUnit = num
						resetUnit(ln.Timestamp)
					}
				}
			}
		}
	}
}

// state derives the answer from what has been folded so far, without disturbing
// the fold.
//
// Every derived field lands on a copy, because the same folder is asked again a
// few seconds later with more of the transcript behind it — a derivation that
// appended to the accumulator would double its own output. The Todos copy is
// defensive rather than demonstrably necessary (a TodoWrite replaces the list
// wholesale, so today nothing survives to be corrupted); it is here because the
// promotion below writes into the slice, and "derives without mutating" is the
// property that makes resuming safe at all.
func (f *folder) state(loop Loop, agentStatus string, now time.Time) State {
	st := f.st
	st.AgentStatus, st.Now = agentStatus, now
	st.Todos = append([]Todo(nil), f.st.Todos...)
	st.Gates = nil
	taskOrder, taskByID, checklist, maxUnit, units, started := f.taskOrder, f.taskByID, f.checklist, f.maxUnit, f.units, f.started

	// Breadth axis priority: a real TodoWrite list (set in handleToolUse) wins;
	// otherwise reconstruct from TaskCreate/TaskUpdate in creation order.
	if len(st.Todos) == 0 && len(taskOrder) > 0 {
		for _, id := range taskOrder {
			t := taskByID[id]
			if t.Status == "deleted" {
				continue
			}
			st.Todos = append(st.Todos, *t)
		}
	}

	// Next fallback: a markdown checkbox list the agent rendered in prose.
	if len(st.Todos) == 0 && len(checklist) > 0 {
		st.Todos = checklist
	}

	// If the agent announced "Unit N: …" units (per the preamble) and emitted
	// no TodoWrite list, synthesize the breadth axis from the announcements.
	// The preamble tells the agent to finish one unit's loop before the next,
	// so a lower-numbered unit is treated as done once a higher one begins.
	if len(st.Todos) == 0 && maxUnit > 0 {
		nums := make([]int, 0, len(units))
		for n := range units {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		for _, n := range nums {
			status := "completed"
			if n == maxUnit {
				status = "in_progress"
			}
			st.Todos = append(st.Todos, Todo{Content: units[n], Status: status})
		}
	}

	// Imply the current unit when the agent started implementing (edits or
	// gate runs) but never marked a todo in_progress — a common lapse that
	// otherwise leaves the view a bare pending list with no current-unit body
	// (loop ring, gate lights). Promote the first incomplete todo so that work
	// surfaces under it. Guarded on `started` so a pure exploration/planning
	// phase (only reads) still shows every todo as pending.
	if started {
		if st.CurrentUnit() < 0 {
			for i := range st.Todos {
				if st.Todos[i].Status != "completed" {
					st.Todos[i].Status = "in_progress"
					break
				}
			}
		}
	}

	// Resolve the final phase against the task-list boundary: no task list yet
	// → explore (pre-loop planning / spec); a task list but no phase-relevant
	// tool in the current unit → default to implement (the loop starts there).
	st.CurrentPhase = loop.ResolvePhase(len(st.Todos) > 0, st.CurrentPhase)

	// Emit gates in loop order for stable rendering. Markers are phase
	// transitions, not pass/fail checks — they don't appear in the row.
	for _, g := range loop.Gates {
		if g.Marker {
			continue
		}
		if gs, ok := f.gates[g.Name]; ok {
			st.Gates = append(st.Gates, *gs)
		} else {
			st.Gates = append(st.Gates, GateState{Name: g.Name, Phase: g.Phase})
		}
	}
	return st
}

func handleToolUse(loop Loop, b block, ts time.Time, st *State, gates map[string]*GateState, pending map[string]string, currentTodo *string, started *bool, hasTasks bool, resetUnit func(time.Time)) {
	if b.Name == "TodoWrite" {
		var in struct {
			Todos []Todo `json:"todos"`
		}
		if json.Unmarshal(b.Input, &in) == nil {
			st.Todos = in.Todos
			// A new in_progress item begins a fresh unit: reset the clock.
			for _, t := range in.Todos {
				if t.Status == "in_progress" && t.Content != *currentTodo {
					*currentTodo = t.Content
					resetUnit(ts)
				}
			}
		}
		return
	}

	var command string
	if b.Name == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(b.Input, &in)
		command = in.Command
	}

	// Breadth fallback: mark implementation started so a unit can be implied
	// when the agent edits/gates without marking a todo in_progress.
	switch b.Name {
	case "Edit", "Write", "MultiEdit", "ExitPlanMode":
		*started = true
	case "Bash":
		if loop.gateFor(command) != nil {
			*started = true
		}
	}

	// Depth: the current phase, gated on whether a task list exists yet
	// (explore is the pre-task-list phase — see PhaseForTool).
	if p := loop.PhaseForTool(b.Name, command, hasTasks); p != "" {
		st.CurrentPhase = p
	}

	// A non-marker Bash gate sets up a pending result to resolve against the
	// following tool_result (the authoritative pass/fail signal).
	if b.Name == "Bash" {
		if g := loop.gateFor(command); g != nil && !g.Marker {
			if gates[g.Name] == nil {
				gates[g.Name] = &GateState{Name: g.Name, Phase: g.Phase}
			}
			pending[b.ID] = g.Name
		}
	}
}

// decodeBlocks handles message.content being either a JSON array of blocks
// or a bare string (which carries no tool blocks).
func decodeBlocks(raw json.RawMessage) []block {
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// checkboxLine matches a markdown task-list item: "- [ ] foo", "* [x] bar",
// "- [~] wip". Capture 1 is the marker, capture 2 the label.
var checkboxLine = regexp.MustCompile(`(?m)^\s*[-*]\s*\[([ xX~-])\]\s+(.+?)\s*$`)

// parseChecklist extracts todo items from a markdown checkbox list.
func parseChecklist(text string) []Todo {
	ms := checkboxLine.FindAllStringSubmatch(text, -1)
	if len(ms) == 0 {
		return nil
	}
	out := make([]Todo, 0, len(ms))
	for _, m := range ms {
		status := "pending"
		switch m[1] {
		case "x", "X":
			status = "completed"
		case "~", "-":
			status = "in_progress"
		}
		out = append(out, Todo{Content: firstLine(m[2]), Status: status})
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	// Strip markdown bold/italic markers — noise in a one-line title.
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.TrimRight(s, "*_ ")
	const max = 72
	if len(s) > max {
		// Truncate on a word boundary rather than mid-word.
		cut := s[:max]
		if i := strings.LastIndexByte(cut, ' '); i > 0 {
			cut = cut[:i]
		}
		s = strings.TrimRight(cut, " ,.;:—-") + "…"
	}
	return s
}

package watch

import (
	"bufio"
	"encoding/json"
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
// todos+loop state. It is a full replay each call — transcripts are
// append-only, so a fresh scan is simplest and correct for a POC repaint.
func BuildState(loop Loop, transcriptPath, agentStatus string, now time.Time) (State, error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return State{}, err
	}
	defer func() { _ = f.Close() }()

	st := State{AgentStatus: agentStatus, Now: now}
	gates := map[string]*GateState{}
	pending := map[string]string{} // tool_use ID -> gate name
	var currentTodo string
	var started bool          // has implementation begun in the current unit?
	units := map[int]string{} // announced "Unit N: desc"
	maxUnit := 0
	// TaskCreate/TaskUpdate reconstruction — the todo tool in this
	// environment. IDs are assigned in creation order (matching "Task #N").
	taskByID := map[string]*Todo{}
	var taskOrder []string
	taskCreates := 0
	var currentTask string
	var checklist []Todo // latest markdown "- [x]" checklist snapshot

	// resetUnit clears per-unit state when a new unit begins, so gate lights
	// and the loop phase reflect only the current unit's work — not results
	// carried over from earlier units in the same session.
	resetUnit := func(ts time.Time) {
		for k := range gates {
			delete(gates, k)
		}
		for k := range pending {
			delete(pending, k)
		}
		st.CurrentPhase = ""
		started = false
		if !ts.IsZero() {
			st.UnitStart = ts
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var ln rawLine
		if err := json.Unmarshal(sc.Bytes(), &ln); err != nil {
			continue
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
					taskCreates++
					id := strconv.Itoa(taskCreates)
					taskByID[id] = &Todo{Content: subject, Status: "pending"}
					taskOrder = append(taskOrder, id)
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
							if in.Status == "in_progress" && in.TaskID != currentTask {
								currentTask = in.TaskID
								resetUnit(ln.Timestamp)
							}
						}
					}
				default:
					handleToolUse(loop, b, ln.Timestamp, &st, gates, pending, &currentTodo, &started, resetUnit)
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
					checklist = items
				}
				// Prose "Unit N:" announcements are only a breadth source when
				// the agent isn't using the task tool. If tasks are in play,
				// ignore prose mentions — otherwise the agent's own commentary
				// ("Unit 8: …") triggers false unit boundaries and wipes the
				// current unit's gate state.
				if taskCreates == 0 {
					if m := unitAnnounce.FindStringSubmatch(b.Text); m != nil {
						num := atoi(m[1])
						units[num] = firstLine(m[2])
						if num > maxUnit {
							maxUnit = num
							resetUnit(ln.Timestamp)
						}
					}
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return State{}, err
	}

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

	// Emit gates in loop order for stable rendering. Markers are phase
	// transitions, not pass/fail checks — they don't appear in the row.
	for _, g := range loop.Gates {
		if g.Marker {
			continue
		}
		if gs, ok := gates[g.Name]; ok {
			st.Gates = append(st.Gates, *gs)
		} else {
			st.Gates = append(st.Gates, GateState{Name: g.Name, Phase: g.Phase})
		}
	}
	return st, nil
}

func handleToolUse(loop Loop, b block, ts time.Time, st *State, gates map[string]*GateState, pending map[string]string, currentTodo *string, started *bool, resetUnit func(time.Time)) {
	setPhase := func(p string) {
		if loop.hasPhase(p) {
			st.CurrentPhase = p
		}
	}
	switch b.Name {
	case "TodoWrite":
		var in struct {
			Todos []Todo `json:"todos"`
		}
		if json.Unmarshal(b.Input, &in) == nil {
			st.Todos = in.Todos
			// A new in_progress item begins a fresh unit: reset the clock
			// and let the explore phase register again.
			for _, t := range in.Todos {
				if t.Status == "in_progress" && t.Content != *currentTodo {
					*currentTodo = t.Content
					resetUnit(ts)
				}
			}
		}
	case "ExitPlanMode":
		// The plan is done; implementation begins.
		*started = true
		setPhase("implement")
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(b.Input, &in)
		if g := loop.gateFor(in.Command); g != nil {
			*started = true
			setPhase(g.Phase)
			if g.Marker {
				// Phase marker (e.g. commit): advances the loop, no pass/fail.
				break
			}
			if gates[g.Name] == nil {
				gates[g.Name] = &GateState{Name: g.Name, Phase: g.Phase}
			}
			pending[b.ID] = g.Name
		} else if !*started && isExploreCommand(in.Command) {
			setPhase("explore")
		}
	case "Edit", "Write", "MultiEdit":
		var in struct {
			FilePath string `json:"file_path"`
		}
		_ = json.Unmarshal(b.Input, &in)
		*started = true
		if isTestFile(in.FilePath) && loop.hasPhase("test") {
			setPhase("test")
		} else {
			setPhase("implement")
		}
	case "Read", "Grep", "Glob", "LS", "NotebookRead":
		// Read-only investigation counts as exploration only until the
		// agent starts implementing; reads mid-implementation don't regress
		// the phase back to explore.
		if !*started {
			setPhase("explore")
		}
	}
}

// exploreCmd matches read-only investigative shell commands (used to detect
// the explore phase before implementation begins).
var exploreCmd = regexp.MustCompile(`(?m)^\s*(cd\s+[^&;|]*&&\s*)?(grep|rg|ls|cat|find|head|tail|sed|awk|tree|wc|jj\s+(log|st|status|diff|show)|git\s+(log|diff|status|show))\b`)

func isExploreCommand(command string) bool { return exploreCmd.MatchString(command) }

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

func isTestFile(path string) bool {
	base := strings.ToLower(path)
	return strings.Contains(base, "_test.") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, "test_") ||
		strings.Contains(base, "spec.") ||
		strings.Contains(base, "fixture")
}

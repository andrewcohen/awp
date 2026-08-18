package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/andrewcohen/awp/internal/watch"
)

// chatChangedEvent tells the frontend the transcript grew; see Chat.Follow for
// why the event carries no turns.
const chatChangedEvent = "chat:changed"

// One watcher at a time, matching the one pane at a time the rest of this
// surface assumes.
var (
	chatMu   sync.Mutex
	chatStop chan struct{}
)

// Chat is the agent's conversation, read from the transcript rather than
// scraped off the screen.
//
// A pane shows what the agent drew. This shows what it said — and those are not
// the same thing, because a TUI is a rendering: it wraps, truncates, redraws in
// place and throws away everything that scrolled past. The transcript is the
// record underneath, so a chat built from it can show a tool call the pane has
// long since scrolled away, and can render a diff as a diff instead of as the
// characters an agent chose to draw a diff with.
//
// Claude Code writes it as JSON Lines under
// ~/.claude/projects/<slug>/<session>.jsonl, and internal/watch already knows
// how to find the newest one for a workspace — that package reads the same file
// to follow the dev loop. What it does not have is this projection: it decodes
// for gate and phase detection, with shapes unexported and tuned to that
// question. Conversation turns are a different question about the same bytes.
type Chat struct{}

// ChatTool is one tool call, and the result that came back for it.
//
// Summary is the one line a collapsed row shows; Detail is what expanding it
// reveals. They are separated here rather than in the frontend because deciding
// what a Bash call is "about" means reading its arguments, and that is a
// question about the tool's schema, not about layout.
type ChatTool struct {
	Name    string
	Summary string
	Detail  string
	IsError bool
	// File and Patch are set for edits, so the frontend can render a real diff
	// instead of the JSON arguments that produced one. The patch is built here
	// rather than shipped as two strings because turning an edit into a diff is
	// a question about the edit, not about how it is displayed.
	File  string
	Patch string
}

// ChatTurn is one entry in the conversation.
type ChatTurn struct {
	// Kind is "user", "assistant" or "system".
	Kind     string
	At       time.Time
	Text     string
	Thinking string
	Tools    []ChatTool
}

// transcriptLine is the subset of a transcript line this projection needs.
type transcriptLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock is one item of a message's content array. Claude Code also
// writes a plain string for simple messages, which parseContent handles.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// maxTurns bounds what crosses the bridge. A long session is tens of thousands
// of lines, and nobody scrolls back through all of it in a POC — the recent
// conversation is the part being asked about.
const maxTurns = 300

// Turns reads the session's transcript and returns the conversation.
func (c *Chat) Turns(session string) ([]ChatTurn, error) {
	path, err := transcriptFor(session)
	if err != nil {
		return nil, err
	}
	return readTurns(path)
}

// transcriptFor maps a zmx session to the transcript of the workspace it runs
// in. The session knows its own directory, which is the only link between the
// two: a transcript is filed under the path the agent was started in.
func transcriptFor(session string) (string, error) {
	if session == "" {
		return "", fmt.Errorf("chat: no session named")
	}
	found, ok, err := zmxClient().Lookup(context.Background(), session)
	if err != nil {
		return "", fmt.Errorf("chat: looking up %s: %w", session, err)
	}
	if !ok {
		return "", fmt.Errorf("chat: no session called %s", session)
	}
	if found.StartDir == "" {
		return "", fmt.Errorf("chat: %s reports no directory, so its transcript cannot be found", session)
	}
	path, err := watch.Locate(found.StartDir)
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}
	return path, nil
}

func readTurns(path string) ([]ChatTurn, error) {
	file, err := os.Open(path) //nolint:gosec // the path comes from watch.Locate, not from the webview.
	if err != nil {
		return nil, fmt.Errorf("chat: reading %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	turns := []ChatTurn{}
	// A result arrives on a later line than the call it answers, so calls are
	// indexed by id and filled in when their result shows up. Matched by id
	// rather than by name and arguments: an agent runs the same command twice
	// often enough that anything else attaches the wrong output to it.
	type site struct{ turn, tool int }
	pending := map[string]site{}

	scan := bufio.NewScanner(file)
	// A single tool result can be megabytes, and the default 64KB limit would
	// silently truncate the conversation mid-session.
	scan.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for scan.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(scan.Bytes(), &line); err != nil {
			// Transcripts carry lines this projection does not model — summaries,
			// meta entries, whatever Claude Code adds next. Skipping them is the
			// difference between a chat that renders and one that refuses to.
			continue
		}
		if line.Type != "user" && line.Type != "assistant" {
			continue
		}

		turn := ChatTurn{Kind: line.Type, At: line.Timestamp}
		var ids []string
		for _, b := range parseContent(line.Message.Content) {
			switch b.Type {
			case "text":
				turn.Text = joinText(turn.Text, b.Text)
			case "thinking":
				// Usually nothing to show. Measured against a real 7MB
				// transcript, all 205 thinking blocks carried an empty string
				// and a signature — the text is stripped before it is written,
				// so the chat can only render what survived. Kept because the
				// block is parsed correctly and costs nothing when it is empty.
				turn.Thinking = joinText(turn.Thinking, b.Thinking)
			case "tool_use":
				turn.Tools = append(turn.Tools, toolFromUse(b))
				ids = append(ids, b.ID)
			case "tool_result":
				if at, ok := pending[b.ToolUseID]; ok {
					turns[at.turn].Tools[at.tool].Detail = resultText(b.Content)
					turns[at.turn].Tools[at.tool].IsError = b.IsError
					delete(pending, b.ToolUseID)
				}
			}
		}

		// A user line carrying nothing but tool results is the harness answering
		// the agent, not a person speaking. Kept as a turn it would put an empty
		// bubble between every call and its output.
		if turn.Text == "" && turn.Thinking == "" && len(turn.Tools) == 0 {
			continue
		}
		// Empty rather than nil, because a nil slice marshals as JSON null and
		// the frontend then maps over nothing. The bridge is the seam where Go's
		// "nil is an empty list" stops being true, so it is fixed on this side
		// once instead of guarded at every call site over there.
		if turn.Tools == nil {
			turn.Tools = []ChatTool{}
		}
		turns = append(turns, turn)
		for i, id := range ids {
			pending[id] = site{turn: len(turns) - 1, tool: i}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("chat: reading %s: %w", path, err)
	}

	// Trimming to the tail would orphan results whose calls fall off the front,
	// but those calls are gone with them, so nothing dangles.
	if len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	return turns, nil
}

// toolFromUse turns a tool call into something a collapsed row can show.
//
// The summary is per-tool because "what is this call about" has a different
// answer for each: a command for Bash, a path for a read, a path and a diff for
// an edit. A generic renderer would show `{"file_path":"...","old_string":...}`
// and make the reader do the parsing.
func toolFromUse(b contentBlock) ChatTool {
	tool := ChatTool{Name: b.Name}
	var in struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		Pattern     string `json:"pattern"`
		Prompt      string `json:"prompt"`
		URL         string `json:"url"`
		OldString   string `json:"old_string"`
		NewString   string `json:"new_string"`
		Content     string `json:"content"`
	}
	_ = json.Unmarshal(b.Input, &in)

	switch b.Name {
	case "Bash":
		tool.Summary = firstLine(in.Command)
	case "Read", "NotebookEdit":
		tool.Summary = in.FilePath
	case "Edit":
		tool.Summary = in.FilePath
		tool.File, tool.Patch = in.FilePath, unifiedPatch(in.FilePath, in.OldString, in.NewString)
	case "Write":
		tool.Summary = in.FilePath
		tool.File, tool.Patch = in.FilePath, unifiedPatch(in.FilePath, "", in.Content)
	case "Grep", "Glob":
		tool.Summary = in.Pattern
	case "WebFetch", "WebSearch":
		tool.Summary = firstLine(in.URL + in.Prompt)
	default:
		tool.Summary = firstLine(in.Description)
	}
	if tool.Summary == "" {
		tool.Summary = strings.TrimSpace(string(b.Input))
	}
	return tool
}

// parseContent accepts both shapes a message's content takes: an array of
// blocks, or a bare string for a simple message.
func parseContent(raw json.RawMessage) []contentBlock {
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return []contentBlock{{Type: "text", Text: text}}
	}
	return nil
}

// resultText flattens a tool result, which is a string in the simple case and
// an array of blocks when the tool returned structured output.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return strings.TrimSpace(string(raw))
}

func joinText(existing, add string) string {
	if strings.TrimSpace(add) == "" {
		return existing
	}
	if existing == "" {
		return add
	}
	return existing + "\n\n" + add
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Follow watches the session's transcript and tells the frontend when it grows.
//
// An event rather than the turns themselves: the file is appended to constantly
// while an agent works, and pushing a parsed conversation on every write would
// send the whole thing dozens of times a minute. The frontend re-reads when it
// hears, which costs one parse per change and keeps the decision about how often
// to look on the side that knows whether anyone is looking.
//
// Polled rather than watched with fsevents. The transcript is a local file
// appended by a process on the same machine; a second of latency is invisible
// next to how long an agent takes to say anything, and a poll cannot leak a
// watcher when the pane closes.
func (c *Chat) Follow(session string) error {
	path, err := transcriptFor(session)
	if err != nil {
		return err
	}

	chatMu.Lock()
	if chatStop != nil {
		close(chatStop)
	}
	stop := make(chan struct{})
	chatStop = stop
	chatMu.Unlock()

	go func() {
		app := application.Get()
		var last int64 = -1
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				info, err := os.Stat(path)
				if err != nil {
					continue
				}
				if size := info.Size(); size != last {
					last = size
					if app != nil {
						app.Event.Emit(chatChangedEvent, session)
					}
				}
			}
		}
	}()
	return nil
}

// Unfollow stops the watcher. Called when the chat is closed or the pane
// switches away, so a session nobody is looking at is not being stat'd.
func (c *Chat) Unfollow() {
	chatMu.Lock()
	defer chatMu.Unlock()
	if chatStop != nil {
		close(chatStop)
		chatStop = nil
	}
}

// Say sends a message to the agent as if it had been typed into its terminal.
//
// Through `zmx paste` rather than by writing to the pty, and that is not a
// detail: an agent's input is a line editor, so text arriving as a stream of
// keystrokes gets interpreted — a newline submits, a bracket may trigger
// completion, and a multi-line message would send itself one line at a time.
// Paste wraps the text in bracketed-paste markers, which is how a terminal says
// "this is text, not typing". awp already sends prompts this way; see
// internal/cli/prompt_sender.go.
//
// It also works with no pane attached, because it goes to the session rather
// than through this client's pty — so the chat can be used on a workspace the
// terminal tab has never been opened on.
func (c *Chat) Say(session, text string) error {
	if session == "" {
		return errors.New("chat: no session named")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("chat: nothing to send")
	}
	if err := zmxClient().Paste(context.Background(), session, text); err != nil {
		return fmt.Errorf("chat: sending to %s: %w", session, err)
	}
	return nil
}

// unifiedPatch renders an edit as a patch, because @pierre/diffs takes one and
// because a patch is what an edit is.
//
// The transcript stores an Edit as the two strings the agent supplied, which is
// enough to show but not enough to read: a wall of removed lines beside a wall
// of added ones hides that eighty of them are identical. Trimming the common
// head and tail leaves the part that actually changed, which is the same thing
// a real diff would show without needing a diff algorithm here.
//
// Not a minimal diff. Lines that differ in the middle are all marked changed
// even where they match — an interior coincidence is reported as a rewrite. The
// alternative is an LCS implementation, which is worth having when the chat is
// worth keeping, and is not worth it to render a POC's edit rows.
func unifiedPatch(file, before, after string) string {
	if file == "" || before == after {
		return ""
	}
	oldLines := splitLines(before)
	newLines := splitLines(after)

	head := 0
	for head < len(oldLines) && head < len(newLines) && oldLines[head] == newLines[head] {
		head++
	}
	tail := 0
	for tail < len(oldLines)-head && tail < len(newLines)-head &&
		oldLines[len(oldLines)-1-tail] == newLines[len(newLines)-1-tail] {
		tail++
	}

	// A few lines either side, so a change has somewhere to sit.
	const context = 3
	start := head - context
	if start < 0 {
		start = 0
	}
	oldEnd, newEnd := len(oldLines)-tail+context, len(newLines)-tail+context
	if oldEnd > len(oldLines) {
		oldEnd = len(oldLines)
	}
	if newEnd > len(newLines) {
		newEnd = len(newLines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", file, file)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
		start+1, oldEnd-start, start+1, newEnd-start)
	for _, line := range oldLines[start:head] {
		b.WriteString(" " + line + "\n")
	}
	for _, line := range oldLines[head : len(oldLines)-tail] {
		b.WriteString("-" + line + "\n")
	}
	for _, line := range newLines[head : len(newLines)-tail] {
		b.WriteString("+" + line + "\n")
	}
	for _, line := range oldLines[len(oldLines)-tail : oldEnd] {
		b.WriteString(" " + line + "\n")
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

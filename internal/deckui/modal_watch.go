package deckui

import (
	"os"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
)

// watchInterval is how often the watch modal re-reads the transcript while
// open. Background deck refresh pauses during a modal (canBackgroundRefresh),
// so the modal drives its own tick.
const watchInterval = 1 * time.Second

type watchTickMsg time.Time

// watchFrameMsg carries a rebuilt frame back to the main loop. The rebuild
// runs in a tea.Cmd because it parses the agent's whole transcript, which is
// hundreds of milliseconds on a session that has been running a while —
// measured at 675ms for a 97MB transcript. Done inline, that is 675ms of
// frozen deck on every open and every tick.
type watchFrameMsg struct {
	transcript string
	header     string
	body       string
	// stamp is the transcript's size and mtime at the time it was read, so
	// the next tick can skip the parse when the agent has written nothing.
	stamp watchStamp
	// unchanged means the stamp matched and header/body are not set.
	unchanged bool
}

// watchStamp identifies a transcript's contents cheaply. An agent only ever
// appends to its transcript, so size plus mtime is enough to know a reparse
// would produce the same frame.
type watchStamp struct {
	size int64
	mod  time.Time
}

func statWatchStamp(path string) watchStamp {
	info, err := os.Stat(path)
	if err != nil {
		return watchStamp{}
	}
	return watchStamp{size: info.Size(), mod: info.ModTime()}
}

func scheduleWatchTick() tea.Cmd {
	return tea.Tick(watchInterval, func(t time.Time) tea.Msg { return watchTickMsg(t) })
}

// watchModal is the `w` overlay: a bordered popover framing a scrollable
// render of the selected workspace's dev-loop progress (units + loop phase +
// gates), rebuilt from the agent's Claude Code transcript every watchInterval.
// Read-only — it never runs gates or touches the agent. Mirrors helpModal's
// viewport-in-a-box shape.
type watchModal struct {
	loop          watch.Loop
	configured    bool
	workspacePath string
	label         string
	agentStatus   string
	transcript    string
	vp            viewport.Model
	header        string
	// stamp is what the last rebuilt frame was read from, so a tick over an
	// idle transcript costs a stat instead of a full parse.
	stamp watchStamp
}

// newWatchModal resolves the workspace's dev loop and seeds the first frame.
func newWatchModal(item Item) *watchModal {
	cfg, _ := config.Load(item.RepoRoot)
	vp := viewport.New()
	vp.KeyMap = viewport.KeyMap{
		Up:           key.NewBinding(key.WithKeys("up", "k")),
		Down:         key.NewBinding(key.WithKeys("down", "j")),
		PageUp:       key.NewBinding(key.WithKeys("pgup")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d")),
	}
	wm := &watchModal{
		loop:          watch.Resolve(cfg),
		configured:    watch.IsConfigured(cfg),
		workspacePath: item.Path,
		label:         item.ProjectName + "/" + item.WorkspaceName,
		agentStatus:   item.Status,
		vp:            vp,
	}
	wm.header = watch.Header(item.ProjectName+"/"+item.WorkspaceName, watch.State{AgentStatus: item.Status})
	wm.vp.SetContent("reading the agent's transcript…")
	return wm
}

// refresh builds the next frame off the main loop.
//
// Everything it needs is copied into the closure, so the returned Cmd touches
// no modal state — the result comes back as a watchFrameMsg and apply is the
// only writer. That is what keeps a multi-hundred-millisecond parse off the
// thread that is drawing the deck.
func (wm *watchModal) refresh() tea.Cmd {
	loop, label, status := wm.loop, wm.label, wm.agentStatus
	configured, path, known, stamp := wm.configured, wm.workspacePath, wm.transcript, wm.stamp
	return func() tea.Msg {
		if !configured {
			// No dev_loop → don't watch with a guessed default loop.
			return watchFrameMsg{
				header: watch.Header(label, watch.State{AgentStatus: status}),
				body:   "no dev_loop configured for this repo — run `awp watch --suggest` for a setup prompt.",
			}
		}
		transcript := known
		if located, err := watch.LocateSticky(path, known, time.Now()); err == nil {
			transcript = located
		}
		if transcript == "" {
			return watchFrameMsg{
				header: watch.Header(label, watch.State{AgentStatus: status}),
				body:   "waiting for the agent to start its session…",
			}
		}
		// Stamped before reading, not after: anything the agent appends while
		// the parse is running belongs to the next frame, and a stamp taken
		// afterwards would claim to cover it.
		next := statWatchStamp(transcript)
		// An agent only appends, so an unchanged size and mtime means the
		// parse would rebuild the frame already on screen.
		if transcript == known && next == stamp && stamp != (watchStamp{}) {
			return watchFrameMsg{transcript: transcript, stamp: stamp, unchanged: true}
		}
		st, err := watch.BuildState(loop, transcript, status, time.Now())
		if err != nil {
			return watchFrameMsg{
				transcript: transcript,
				stamp:      next,
				header:     watch.Header(label, watch.State{AgentStatus: status}),
				body:       "watch error: " + err.Error(),
			}
		}
		// The header is pinned as the popover's sticky title (see
		// renderPopover); the body carries the rest so it isn't repeated.
		return watchFrameMsg{
			transcript: transcript,
			stamp:      next,
			header:     watch.Header(label, st),
			body:       watch.RenderBody(loop, st),
		}
	}
}

// apply installs a frame the refresh Cmd produced.
func (wm *watchModal) apply(msg watchFrameMsg) {
	if msg.transcript != "" {
		wm.transcript = msg.transcript
		wm.stamp = msg.stamp
	}
	if msg.unchanged {
		return
	}
	wm.header = msg.header
	wm.vp.SetContent(msg.body)
}

func (wm *watchModal) footerHelp() string { return "" }

func (wm *watchModal) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case watchTickMsg:
		return wm.refresh()
	case watchFrameMsg:
		wm.apply(msg)
		// Re-arm only now. Ticking independently of the rebuild would let a
		// parse slower than watchInterval queue another before the first
		// finished, and the modal would fall further behind the more it had
		// to read.
		return scheduleWatchTick()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "w", "esc", "q", "ctrl+c":
			m.active = nil
			return nil
		}
		var cmd tea.Cmd
		wm.vp, cmd = wm.vp.Update(msg)
		return cmd
	}
	return nil
}

func (wm *watchModal) renderPopover(m *Model, b box) string {
	boxWidth, innerWidth := helpBoxDims(b.w)
	wm.vp.SetWidth(innerWidth)
	vpHeight := b.h - 8
	if vpHeight < 3 {
		vpHeight = 3
	}
	wm.vp.SetHeight(vpHeight)

	hintText := "↑/↓ scroll · pgup/pgdn page · esc close · repaints 1s"
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(hintText)

	body := lipgloss.JoinVertical(lipgloss.Left, wm.header, "", wm.vp.View(), "", hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(boxWidth + borderCells).
		Render(body)
}

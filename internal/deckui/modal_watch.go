package deckui

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/watch"
)

// watchInterval is how often the watch modal re-reads the transcript while
// open. Background deck refresh pauses during a modal (canBackgroundRefresh),
// so the modal drives its own tick.
const watchInterval = 1 * time.Second

type watchTickMsg time.Time

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
	workspacePath string
	label         string
	agentStatus   string
	transcript    string
	vp            viewport.Model
}

// newWatchModal resolves the workspace's dev loop and seeds the first frame.
func newWatchModal(item Item) *watchModal {
	cfg, _ := config.Load(item.RepoRoot)
	vp := viewport.New(0, 0)
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
		workspacePath: item.Path,
		label:         item.ProjectName + "/" + item.WorkspaceName,
		agentStatus:   item.Status,
		vp:            vp,
	}
	wm.refresh()
	return wm
}

// refresh re-locates the newest transcript (sticky) and rebuilds the view.
func (wm *watchModal) refresh() {
	if located, err := watch.LocateSticky(wm.workspacePath, wm.transcript, time.Now()); err == nil {
		wm.transcript = located
	}
	if wm.transcript == "" {
		wm.vp.SetContent("waiting for the agent to start its session…")
		return
	}
	st, err := watch.BuildState(wm.loop, wm.transcript, wm.agentStatus, time.Now())
	if err != nil {
		wm.vp.SetContent("watch error: " + err.Error())
		return
	}
	wm.vp.SetContent(watch.Render(wm.loop, wm.label, st))
}

func (wm *watchModal) footerHelp() string { return "" }

func (wm *watchModal) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case watchTickMsg:
		wm.refresh()
		return scheduleWatchTick()
	case tea.KeyMsg:
		switch msg.String() {
		case "w", "esc", "q", "ctrl+c":
			m.active = nil
			return tea.ClearScreen
		}
		var cmd tea.Cmd
		wm.vp, cmd = wm.vp.Update(msg)
		return cmd
	}
	return nil
}

func (wm *watchModal) renderPopover(m *Model) string {
	boxWidth, innerWidth := helpBoxDims(m.width)
	wm.vp.Width = innerWidth
	vpHeight := m.height - 8
	if vpHeight < 3 {
		vpHeight = 3
	}
	wm.vp.Height = vpHeight

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)).Render("watch · " + wm.label)
	hintText := "↑/↓ scroll · pgup/pgdn page · esc close · repaints 1s"
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(hintText)

	body := lipgloss.JoinVertical(lipgloss.Left, title, "", wm.vp.View(), "", hint)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(boxWidth).
		Render(body)
}

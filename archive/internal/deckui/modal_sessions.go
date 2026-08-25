package deckui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
)

// sessionPicker is `z`: everything the pane backend is holding, in one list.
//
// It exists because a hosted session outlives the deck that opened it, so the
// set of live agents is real state with nowhere to see it. `zmx ls` shows it,
// but as dotted names, a unix stamp and a full argv on one line — which is
// what the deck is for.
//
// Enter attaches the selected session in a pane, which is the thing the raw
// command cannot do.
type sessionPicker struct {
	list    list.Model
	loading bool
	err     error
	// pendingEnd is the session `x` has asked about and is waiting on a y for.
	//
	// The question goes in the status bar and the picker stays up, rather than a
	// popover replacing it: ending sessions is something you do to several in a
	// row, and a modal would drop the list and the cursor each time.
	pendingEnd *PaneSession
}

// sessionItem is one row. It keeps the whole PaneSession because the delegate
// renders several fields and the enter handler needs the identity.
type sessionItem struct{ s PaneSession }

func (i sessionItem) FilterValue() string {
	return i.s.Label + " " + i.s.Kind + " " + i.s.Cmd
}

// loadingSession stands in during the fetch so the list's chrome keeps its
// shape and the panel does not resize under the cursor.
type loadingSession struct{}

func (loadingSession) FilterValue() string { return "" }

type sessionsLoadedMsg struct {
	sessions []PaneSession
	err      error
}

// sessionEndedMsg reports the outcome of an `x`. It carries the label and kind
// rather than just the name so the status line reads the way the row did.
type sessionEndedMsg struct {
	label string
	kind  string
	err   error
}

// age renders how long a session has been alive, in the largest unit that
// still says something: "3s", "4m", "2h", "3d". A unix stamp is not a thing to
// show anyone, and a full timestamp answers a question nobody asked — what you
// want to know about a session is whether it is old.
func age(started, now time.Time) string {
	if started.IsZero() {
		return "?"
	}
	d := now.Sub(started)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

// sessionItemDelegate renders a session row: a state glyph, the workspace it
// belongs to, its kind, and how long it has been up.
//
// Its own delegate rather than the shared one because the columns carry
// different colours — the glyph is the session's state and the kind is not —
// and a single styled string could not say that.
type sessionItemDelegate struct {
	styles deckStyles
	now    time.Time
	// wsWidth is the widest workspace label in the list, so the kind column
	// lines up instead of ragging off the longest name.
	wsWidth int
}

func (d sessionItemDelegate) Height() int                         { return 1 }
func (d sessionItemDelegate) Spacing() int                        { return 0 }
func (d sessionItemDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d sessionItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	if _, ok := item.(loadingSession); ok {
		_, _ = io.WriteString(w, d.styles.Muted.Render("  loading…"))
		return
	}
	it, ok := item.(sessionItem)
	if !ok {
		return
	}
	selected := index == m.Index()

	// The house selection treatment: the ┃ bar plus warning-bold on the label.
	prefix := "  "
	if selected {
		prefix = d.styles.Bar.Render("┃ ")
	}

	var glyph string
	var glyphStyle lipgloss.Style
	switch {
	case !it.s.Live:
		glyph, glyphStyle = "✗", d.styles.Danger
	case it.s.Attached:
		glyph, glyphStyle = "●", d.styles.Success
	default:
		glyph, glyphStyle = "○", d.styles.Accent
	}

	label := it.s.Label
	labelStyle := d.styles.Label
	if selected {
		labelStyle = d.styles.Selected
	}
	pad := d.wsWidth - lipgloss.Width(label)
	if pad < 0 {
		pad = 0
	}

	_, _ = io.WriteString(w, prefix)
	_, _ = io.WriteString(w, glyphStyle.Render(glyph))
	_, _ = io.WriteString(w, " ")
	_, _ = io.WriteString(w, labelStyle.Render(label))
	_, _ = io.WriteString(w, strings.Repeat(" ", pad))
	_, _ = io.WriteString(w, "  ")
	_, _ = io.WriteString(w, d.styles.Info.Render(it.s.Kind))
	_, _ = io.WriteString(w, d.styles.Muted.Render("  · "+age(it.s.Started, d.now)))
	if !it.s.Live {
		_, _ = io.WriteString(w, d.styles.Danger.Render("  exited"))
	}
	if !it.s.HasItem {
		_, _ = io.WriteString(w, d.styles.Warning.Render("  no workspace"))
	}
}

func newSessionPicker(m *Model) (*sessionPicker, tea.Cmd) {
	delegate := sessionItemDelegate{styles: m.styles, now: time.Now()}
	l := list.New([]list.Item{loadingSession{}}, delegate, 0, 0)
	l.Title = "sessions"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	charm.ApplyListTheme(&l, nil)
	p := &sessionPicker{list: l, loading: true}
	return p, loadPaneSessions(m.panes, m.itemsAll)
}

// loadPaneSessions asks the backend on a command, not inline: `zmx ls` is a
// subprocess, and the deck must not block a frame on one.
func loadPaneSessions(panes PaneBackend, items []Item) tea.Cmd {
	sessioner, ok := panes.(PaneSessioner)
	if !ok {
		return nil
	}
	rows := append([]Item(nil), items...)
	return func() tea.Msg {
		s, err := sessioner.Sessions(rows)
		return sessionsLoadedMsg{sessions: s, err: err}
	}
}

func (p *sessionPicker) footerHelp() string {
	if p.loading {
		return ""
	}
	// While a question is pending the footer is the wrong place to look — the
	// status bar is asking, and offering the ordinary keys would suggest they
	// still work.
	if p.pendingEnd != nil {
		return ""
	}
	bindings := append(pickerShortHelp(p.list),
		key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "end")))
	return p.list.Help.ShortHelpView(bindings)
}

func (p *sessionPicker) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sessionsLoadedMsg:
		p.loading = false
		p.err = msg.err
		p.setSessions(m, msg.sessions)
		return nil

	case sessionEndedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("end %s %s: %v", msg.label, msg.kind, msg.err)
		} else {
			m.status = fmt.Sprintf("ended %s %s", msg.label, msg.kind)
		}
		// Reload either way: a failed kill may still have changed something, and
		// the list is the only place the answer shows.
		p.loading = true
		return loadPaneSessions(m.panes, m.itemsAll)

	case tea.KeyPressMsg:
		if p.list.SettingFilter() {
			var cmd tea.Cmd
			p.list, cmd = p.list.Update(msg)
			return cmd
		}
		if p.pendingEnd != nil {
			// Anything but a yes is a no, including the keys that would otherwise
			// move the cursor — the question names one row and answering it must
			// not be able to apply to another.
			target := *p.pendingEnd
			p.pendingEnd = nil
			switch strings.ToLower(msg.String()) {
			case "y", "enter":
				return p.endSession(m, target)
			default:
				m.status = ""
				return nil
			}
		}
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			m.active = nil
			return nil
		case "enter":
			return p.attachSelected(m)
		case "x":
			return p.askToEndSelected(m)
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

// setSessions fills the list, newest first so the thing you just started is at
// the top, and sizes the workspace column to the widest label.
func (p *sessionPicker) setSessions(m *Model, sessions []PaneSession) {
	sorted := append([]PaneSession(nil), sessions...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Started.After(sorted[j].Started)
	})
	widest := 0
	items := make([]list.Item, 0, len(sorted))
	for _, s := range sorted {
		if w := lipgloss.Width(s.Label); w > widest {
			widest = w
		}
		items = append(items, sessionItem{s: s})
	}
	p.list.SetDelegate(sessionItemDelegate{styles: m.styles, now: time.Now(), wsWidth: widest})
	p.list.SetItems(items)
}

// attachSelected opens the highlighted session in a pane.
//
// It needs the deck row the session belongs to, because a pane is opened for
// an Item — that is where the path and repo root come from. A session whose
// workspace has since been deleted has no row, and says so rather than opening
// a pane against nothing.
func (p *sessionPicker) attachSelected(m *Model) tea.Cmd {
	it, ok := p.list.SelectedItem().(sessionItem)
	if !ok {
		return nil
	}
	if !it.s.Live {
		m.status = fmt.Sprintf("%s %s has exited — press a to start a new one", it.s.Label, it.s.Kind)
		return nil
	}
	if !it.s.HasItem {
		m.status = fmt.Sprintf("no workspace row for %s — the session outlived its workspace", it.s.Label)
		return nil
	}
	m.active = nil
	cmd, handled := m.openPane(it.s.Item, it.s.Kind)
	if !handled {
		m.status = fmt.Sprintf("this deck has no pane for %q", it.s.Kind)
	}
	return cmd
}

// askToEndSelected puts the question in the status bar. It asks rather than just
// doing it because a live agent's session is its whole context — the
// conversation, what it has read, what it was part-way through — and none of
// that comes back.
func (p *sessionPicker) askToEndSelected(m *Model) tea.Cmd {
	it, ok := p.list.SelectedItem().(sessionItem)
	if !ok {
		return nil
	}
	if strings.TrimSpace(it.s.Name) == "" {
		// A backend that lists sessions without naming them cannot be asked to
		// end one, and silently doing nothing would read as a broken key.
		m.status = "end: this deck's sessions have no name to end them by"
		return nil
	}
	if _, ok := m.panes.(PaneSessioner); !ok {
		m.status = "end: this deck does not host its own sessions"
		return nil
	}
	s := it.s
	p.pendingEnd = &s
	what := s.Label + " " + s.Kind
	if !s.Live {
		// An exited session holds no agent, so there is nothing to lose — say so,
		// or the same y/N reads as the same risk.
		m.status = fmt.Sprintf("end %s? it has already exited [y/N]", what)
		return nil
	}
	m.status = fmt.Sprintf("end %s? the agent's context is lost [y/N]", what)
	return nil
}

// endSession runs the kill on a command: it is a subprocess, and a frame must
// not wait on one.
func (p *sessionPicker) endSession(m *Model, s PaneSession) tea.Cmd {
	sessioner, ok := m.panes.(PaneSessioner)
	if !ok {
		m.status = "end: this deck does not host its own sessions"
		return nil
	}
	m.status = "ending " + s.Label + " " + s.Kind + "…"
	name, label, kind := s.Name, s.Label, s.Kind
	return func() tea.Msg {
		return sessionEndedMsg{label: label, kind: kind, err: sessioner.EndSession(name)}
	}
}

func (p *sessionPicker) view(m *Model, b box) (left, right string) {
	if p.err != nil {
		return m.styles.Panel.Width(b.w).Render(
			m.styles.Danger.Render("sessions: " + p.err.Error())), ""
	}
	if !p.loading && len(p.list.Items()) == 0 {
		return m.styles.Panel.Width(b.w).Render(
			m.styles.Muted.Render("No sessions. Opening an agent, editor, shell or vcs pane starts one.")), ""
	}
	return renderPickerPanel(m, &p.list, b), ""
}

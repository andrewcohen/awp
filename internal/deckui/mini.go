package deckui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// MiniRow is one workspace row in the mini-deck quick-jump list.
// Kept flat (no nested structs) so the caller can build it directly
// from state.JSONStore entries without dragging in workspace.Service.
type MiniRow struct {
	Project   string
	Workspace string
	RepoRoot  string
	Path      string
	Status    string
	Unread    bool
}

// miniItem wraps MiniRow for the bubbles/list integration. FilterValue
// concatenates project + workspace so the list's default fuzzy filter
// matches either.
type miniItem struct{ row MiniRow }

func (m miniItem) FilterValue() string { return m.row.Project + " " + m.row.Workspace }
func (m miniItem) Title() string       { return m.row.Workspace }
func (m miniItem) Description() string { return m.row.Project }

// miniItemDelegate renders "[project] glyph workspace" with the shared
// selection treatment (┃  + warning fg) and the find-mode hint chip
// when an easymotion lookup is active.
type miniItemDelegate struct {
	findHints map[int]string
	findMode  bool
}

func (miniItemDelegate) Height() int                             { return 1 }
func (miniItemDelegate) Spacing() int                            { return 0 }
func (miniItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d miniItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(miniItem)
	if !ok {
		return
	}
	r := item.row
	selected := index == m.Index()
	width := m.Width()

	const prefixWidth = 4
	prefixSlot := lipgloss.NewStyle().Width(prefixWidth)

	prefix := "  "
	if d.findMode {
		if hint, ok := d.findHints[index]; ok {
			prefix = renderFindHint(hint)
		}
	}
	labelStyle := lipgloss.NewStyle()
	if selected && !d.findMode {
		prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true).Render("┃") + " "
		labelStyle = labelStyle.Foreground(lipgloss.Color(colWarning)).Bold(true)
	}

	projectChip := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render("[" + r.Project + "] ")
	glyph := statusGlyph(r.Status, false, r.Unread)
	label := truncate(r.Workspace, max(8, width-12-lipgloss.Width(projectChip)))
	line := fmt.Sprintf("%s %s %s%s",
		prefixSlot.Render(prefix), glyph, projectChip, labelStyle.Render(label))
	fmt.Fprint(w, lipgloss.NewStyle().Width(max(width, 1)).Render(line))
}

// MiniModel is a Bubble Tea model for the mini-deck: a stripped-down
// deck that only renders workspaces with an active agent or an
// unread notification. Enter returns the selected row to the caller
// via Chosen(); q/esc/ctrl+c quits with Chosen()==nil.
type MiniModel struct {
	rows   []MiniRow
	list   list.Model
	width  int
	height int
	chosen *MiniRow

	// Easymotion (f-find) state, mirroring the deck's findMode but
	// flattened: no project stage, just one set of per-row hints.
	findMode    bool
	findHints   map[int]string
	findLookup  map[string]int
	findPrefix  map[rune]bool
	findPending rune
}

func NewMiniModel(rows []MiniRow) MiniModel {
	items := make([]list.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, miniItem{row: r})
	}
	l := list.New(items, miniItemDelegate{}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
	charm.ApplyListTheme(&l, nil)
	return MiniModel{rows: rows, list: l, width: 60, height: 20}
}

func (m MiniModel) Init() tea.Cmd { return nil }

func (m MiniModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.findMode {
			return m.updateFind(msg)
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "f":
			if len(m.rows) > 0 {
				m.findMode = true
				m.findHints, m.findLookup, m.findPrefix = buildMiniRowHints(m.rows)
				m.findPending = 0
				m.list.SetDelegate(miniItemDelegate{findMode: true, findHints: m.findHints})
			}
			return m, nil
		case "enter":
			if len(m.rows) == 0 {
				return m, tea.Quit
			}
			row := m.rows[m.list.Index()]
			m.chosen = &row
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m MiniModel) updateFind(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.cancelFind()
		return m, nil
	case tea.KeyEnter:
		m.cancelFind()
		if len(m.rows) == 0 {
			return m, tea.Quit
		}
		row := m.rows[m.list.Index()]
		m.chosen = &row
		return m, tea.Quit
	}
	if len(msg.Runes) != 1 {
		return m, nil
	}
	if idx, ok := findHintStep(msg.Runes[0], m.findLookup, m.findPrefix, &m.findPending); ok {
		m.list.Select(idx)
		m.cancelFind()
	}
	return m, nil
}

func (m *MiniModel) cancelFind() {
	m.findMode = false
	m.findPending = 0
	m.findHints = nil
	m.findLookup = nil
	m.findPrefix = nil
	m.list.SetDelegate(miniItemDelegate{})
}

func (m MiniModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("awp mini-deck")
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
		Render("active or notified workspaces")
	rows := []string{title, subtitle, ""}

	if len(m.rows) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
			Render("Nothing waiting on you."))
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
			MarginTop(1).Render("q quit"))
		return lipgloss.NewStyle().Width(max(m.width, 1)).Padding(1, 1, 1, 1).
			Render(strings.Join(rows, "\n"))
	}

	listWidth := max(m.width-2, 1)
	// Reserve title + subtitle + blank + footer + container padding.
	listHeight := max(m.height-6, 3)
	m.list.SetSize(listWidth, listHeight)
	rows = append(rows, m.list.View())

	hint := "j/k move · f find · enter jump · q quit"
	if m.findMode {
		if m.findPending != 0 {
			hint = fmt.Sprintf("find: %c… (esc cancel)", m.findPending)
		} else {
			hint = "find: type a hint (esc cancel)"
		}
	}
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).MarginTop(1).
		Render(hint)
	rows = append(rows, footer)
	return lipgloss.NewStyle().Width(max(m.width, 1)).Padding(1, 1, 1, 1).
		Render(strings.Join(rows, "\n"))
}

// Chosen returns the row the user selected with enter, or nil if they
// quit without choosing.
func (m MiniModel) Chosen() *MiniRow { return m.chosen }

// Cursor returns the current cursor index (test helper).
func (m MiniModel) Cursor() int { return m.list.Index() }

// Rows returns the loaded rows (test helper).
func (m MiniModel) Rows() []MiniRow { return m.rows }

// FindMode reports whether the model is currently in find/easymotion
// mode (test helper).
func (m MiniModel) FindMode() bool { return m.findMode }

// MiniIncluded reports whether an entry's status/unread combination
// qualifies it for the mini-deck / attention scope.
//
// "Attention" is "things I should pay attention to right now":
//   - working → an agent is actively generating output or running a
//     tool. Always surface.
//   - waiting → Claude is blocked on a permission/notification prompt.
//     Surface ONLY when Unread, because Unread=false in practice
//     means "I was attached to the session when the hook fired and
//     already saw it" — at which point the row is just stale noise.
//   - idle → only surface when Unread, meaning "the agent finished a
//     turn and I haven't visited since". An idle row that's been
//     read is just a quiet workspace.
//   - exited → never surface. The agent process is gone; there is
//     no one waiting on the other end of an enter press.
//
// This is the (status, unread) half of the filter. The full attention
// filter (AttentionIncluded) layers on a freshness check that drops
// stale "working" rows whose tmux session is gone or whose agent pane
// has fallen back to a bare shell.
func MiniIncluded(status string, unread bool) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return true
	case "waiting":
		return unread
	case "exited", "error":
		return false
	case "idle", "":
		return unread
	default:
		return unread
	}
}

// AttentionIncluded is the shared "this row needs your attention" filter
// used by both the deck's ScopeAttention and the mini-deck. It composes
// MiniIncluded with a freshness check: a stored "working" status only
// counts when the row is actually fresh (live tmux session whose :agent
// pane is the real agent, not a bare shell). Without the freshness
// check, a crashed agent — Claude has no exit hook — would leave
// "working" pinned in the attention scope forever.
//
// active should be true when the row's tmux session exists and its
// :agent pane is running an agent, OR when tmux state is not yet known
// (fast first paint — trust the stored status and let a later refresh
// correct it).
//
// active is only consulted for "working" statuses. For waiting/idle the
// Unread flag is the durable signal (Claude wrote it after the turn
// finished), so the row surfaces regardless of whether the agent
// process is still alive — the mini-deck will recreate the session on
// jump if necessary.
func AttentionIncluded(status string, unread, active bool) bool {
	if !MiniIncluded(status, unread) {
		return false
	}
	if isWorkingStatus(status) && !active {
		return false
	}
	return true
}

func isWorkingStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return true
	}
	return false
}

// buildMiniRowHints assigns easymotion hints across every row. Uses
// "<project>/<workspace>" as the assignHints input so that (a) duplicate
// workspace names across projects don't collide (assignHints would
// otherwise merge them in its map) and (b) the first-letter bucket is
// the project name's first letter, which matches the visual grouping.
func buildMiniRowHints(rows []MiniRow) (map[int]string, map[string]int, map[rune]bool) {
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Project + "/" + r.Workspace
	}
	hintByKey := assignHints(keys)
	forward := map[int]string{}
	lookup := map[string]int{}
	prefix := map[rune]bool{}
	for i, key := range keys {
		hint, ok := hintByKey[key]
		if !ok {
			continue
		}
		forward[i] = hint
		lookup[hint] = i
		if len([]rune(hint)) == 2 {
			prefix[[]rune(hint)[0]] = true
		}
	}
	return forward, lookup, prefix
}

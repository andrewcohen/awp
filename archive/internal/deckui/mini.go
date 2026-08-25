package deckui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/deckdata"
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
	// PRTitle / PRNumber describe the workspace's associated PR when one
	// is resolvable from the persisted PR-status cache. PRNumber == 0
	// means no PR is linked. When both are present the row renders
	// "#N PRTitle" in place of the workspace name.
	PRTitle  string
	PRNumber int
}

// miniItem wraps MiniRow for the bubbles/list integration. FilterValue
// concatenates project + workspace + PR title so the list's default fuzzy
// filter matches any of them — the visible row text is whichever of
// workspace or PR title resolves, so filtering by what's on screen has
// to include both candidates.
type miniItem struct{ row MiniRow }

func (m miniItem) FilterValue() string {
	return strings.Join([]string{m.row.Project, m.row.Workspace, m.row.PRTitle}, " ")
}
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
	labelText := r.Workspace
	if t := strings.TrimSpace(r.PRTitle); t != "" && r.PRNumber > 0 {
		labelText = fmt.Sprintf("#%d %s", r.PRNumber, t)
	}
	label := truncate(labelText, max(8, width-12-lipgloss.Width(projectChip)))
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
	case tea.KeyPressMsg:
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

func (m MiniModel) updateFind(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.cancelFind()
		return m, nil
	case "enter":
		m.cancelFind()
		if len(m.rows) == 0 {
			return m, tea.Quit
		}
		row := m.rows[m.list.Index()]
		m.chosen = &row
		return m, tea.Quit
	}
	typed := []rune(msg.Text)
	if len(typed) != 1 {
		return m, nil
	}
	if idx, ok := findHintStep(typed[0], m.findLookup, m.findPrefix, &m.findPending); ok {
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

// View satisfies tea.Model. This program runs inline rather than in the
// alt-screen, so the view declares no terminal features — the content
// comes from render, which stays a plain string for tests.
func (m MiniModel) View() tea.View {
	return tea.NewView(m.render())
}

func (m MiniModel) render() string {
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

// AttentionIncluded is the mini-deck's "surface this row" filter, and the
// bool form of deckdata.AgentWants — which is where the rule and the
// reasoning behind it now live, since the deck's own scope needs the
// reason and not just the answer.
//
// Kept as a name because the mini-deck genuinely wants a bool: it is a
// jump list, so a row either is or is not somewhere to jump.
func AttentionIncluded(status string, unread, active bool) bool {
	return deckdata.AgentWants(status, unread, active) != deckdata.ReasonNone
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

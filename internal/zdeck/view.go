package zdeck

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
)

// Layout constants. There is deliberately no border and no box: the split is
// a single dim rule, and everything that would have gone in a hint row lives
// in the top and bottom lines instead, where it is doing a job.
const (
	dividerCols = 3 // " │ "
	chromeRows  = 2 // the top line and the bottom line
	minListCols = 24
	maxListCols = 44
)

type styles struct {
	Top       lipgloss.Style
	TopDim    lipgloss.Style
	Bottom    lipgloss.Style
	Key       lipgloss.Style
	KeyOff    lipgloss.Style
	Project   lipgloss.Style
	Label     lipgloss.Style
	Selected  lipgloss.Style
	Bar       lipgloss.Style
	BarIdle   lipgloss.Style
	Divider   lipgloss.Style
	Status    lipgloss.Style
	LiveChip  lipgloss.Style
	Ephemeral lipgloss.Style
}

func newStyles() styles {
	return styles{
		Top:       lipgloss.NewStyle().Bold(true),
		TopDim:    lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
		Bottom:    lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
		Key:       lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true),
		KeyOff:    lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
		Project:   lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true),
		Label:     lipgloss.NewStyle(),
		Selected:  lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true),
		Bar:       lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true),
		BarIdle:   lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
		Divider:   lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
		Status:    lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
		LiveChip:  lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success)),
		Ephemeral: lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)),
	}
}

// listCols is how wide the workspace column is.
func listCols(width int) int {
	w := width / 3
	if w < minListCols {
		w = minListCols
	}
	if w > maxListCols {
		w = maxListCols
	}
	if w > width-dividerCols-minListCols {
		w = width - dividerCols - minListCols
	}
	return w
}

// paneDims is the terminal size available to a hosted process.
func (m Model) paneDims() (w, h int) {
	if m.width <= 0 || m.height <= 0 {
		return 0, 0
	}
	return m.width - listCols(m.width) - dividerCols, m.height - chromeRows
}

func (m Model) render() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	bodyRows := m.height - chromeRows
	lw := listCols(m.width)
	pw, _ := m.paneDims()

	left := padBlock(m.renderList(lw), lw, bodyRows)
	var body string
	if pw < minListCols/2 {
		body = left
	} else {
		rule := m.styles.Divider.Render("│")
		divider := padBlock(strings.Repeat(" "+rule+" \n", bodyRows), dividerCols, bodyRows)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, divider, padBlock(m.renderPane(pw), pw, bodyRows))
	}
	return strings.Join([]string{m.renderTop(), body, m.renderBottom()}, "\n")
}

// renderTop is the context line: what you are looking at, and what is behind
// it. It replaces the title bar a pane would otherwise need.
func (m Model) renderTop() string {
	leftText := "zdeck"
	if it, ok := m.selected(); ok && it.WorkspaceName != "" {
		leftText = it.ProjectName + "/" + it.WorkspaceName
	}
	left := m.styles.Top.Render(leftText)

	var right string
	if m.pane != nil {
		kind := m.styles.Top.Render(m.pane.kind.Label)
		switch m.pane.kind.Lifetime {
		case LongLived:
			right = kind + m.styles.LiveChip.Render(" ● session")
		case Ephemeral:
			right = kind + m.styles.Ephemeral.Render(" ○ ephemeral")
		case Native:
			right = kind
		}
	} else {
		right = m.styles.TopDim.Render("no pane")
	}
	return spread(left, right, m.width)
}

// renderBottom is the key line for whatever currently has the keyboard, plus
// any status. Both are functional; neither is decoration.
func (m Model) renderBottom() string {
	var b strings.Builder
	if m.focus == focusPane {
		b.WriteString(m.styles.Key.Render(leaveKey))
		b.WriteString(m.styles.Bottom.Render(" list · every other key goes to the pane"))
	} else {
		for i, k := range Kinds {
			if i > 0 {
				b.WriteString(m.styles.Bottom.Render("  "))
			}
			style := m.styles.Key
			if k.Lifetime == Native {
				style = m.styles.KeyOff // not wired up yet
			}
			b.WriteString(style.Render(k.Key))
			b.WriteString(m.styles.Bottom.Render(" " + k.Label))
		}
		b.WriteString(m.styles.Bottom.Render("  ·  "))
		b.WriteString(m.styles.Key.Render("tab"))
		b.WriteString(m.styles.Bottom.Render(" focus"))
		if m.pane != nil {
			b.WriteString(m.styles.Bottom.Render("  "))
			b.WriteString(m.styles.Key.Render("x"))
			b.WriteString(m.styles.Bottom.Render(" close"))
		}
	}
	right := ""
	if m.status != "" {
		right = m.styles.Status.Render(m.status)
	}
	return spread(b.String(), right, m.width)
}

func (m Model) renderList(width int) string {
	if len(m.items) == 0 {
		return m.styles.Status.Render("no workspaces")
	}
	var rows []string
	project := ""
	for i, it := range m.items {
		if it.ProjectName != project {
			project = it.ProjectName
			rows = append(rows, m.styles.Project.Render(truncate(project, width)))
		}
		rows = append(rows, m.renderRow(i, it, width))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderRow(i int, it Item, width int) string {
	selected := i == m.cursor
	// The selection treatment drops a tier when the keyboard has moved into
	// the pane, matching the diff viewer: the bar stays, because that is where
	// the keys come back to, but it stops being the brightest thing on a
	// screen whose keys are elsewhere.
	prefix := "  "
	label := m.styles.Label
	if selected {
		switch m.focus {
		case focusList:
			prefix = m.styles.Bar.Render("┃ ")
			label = m.styles.Selected
		case focusPane:
			prefix = m.styles.BarIdle.Render("┃ ")
		}
	}
	name := it.WorkspaceName
	if name == "" {
		name = it.ProjectName
	}
	return prefix + label.Render(truncate(name, width-2))
}

func (m Model) renderPane(width int) string {
	if m.pane == nil {
		return m.styles.Status.Render("press " + kindKeyList() + " to open a pane here")
	}
	_ = width
	return m.pane.term.View()
}

// spread puts left and right on one line, right-aligned to width.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateANSI(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// padBlock forces a block to exactly rows lines of exactly width columns, so
// JoinHorizontal lines the columns up and the frame never overflows.
func padBlock(s string, width, rows int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	for i, l := range lines {
		w := lipgloss.Width(l)
		switch {
		case w > width:
			lines[i] = truncateANSI(l, width)
		case w < width:
			lines[i] = l + strings.Repeat(" ", width-w)
		}
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// truncateANSI cuts a styled line to width without counting escape sequences
// as visible columns.
func truncateANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if visible >= width {
			break
		}
		b.WriteByte(s[i])
		visible++
		i++
	}
	for visible < width {
		b.WriteByte(' ')
		visible++
	}
	return b.String()
}

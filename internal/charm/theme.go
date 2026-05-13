package charm

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

var (
	colorBorder  = lipgloss.Color(Muted)
	colorAccent  = lipgloss.Color(Accent)
	colorMuted   = lipgloss.Color(Muted)
	colorDanger  = lipgloss.Color(Danger)
	colorWarning = lipgloss.Color(Warning)
)

type Theme struct {
	Card       lipgloss.Style
	Title      lipgloss.Style
	Subtitle   lipgloss.Style
	Label      lipgloss.Style
	Focused    lipgloss.Style
	Dim        lipgloss.Style
	Hint       lipgloss.Style
	Error      lipgloss.Style
	Chip       lipgloss.Style
	ChipActive lipgloss.Style
}

func DefaultTheme() Theme {
	return Theme{
		Card:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(1, 2),
		Title:      lipgloss.NewStyle().Bold(true).Foreground(colorAccent),
		Subtitle:   lipgloss.NewStyle().Foreground(colorMuted),
		Label:      lipgloss.NewStyle().Bold(true),
		Focused:    lipgloss.NewStyle().Foreground(colorAccent).Bold(true),
		Dim:        lipgloss.NewStyle().Foreground(colorMuted),
		Hint:       lipgloss.NewStyle().Foreground(colorMuted),
		Error:      lipgloss.NewStyle().Foreground(colorDanger).Bold(true),
		Chip:       lipgloss.NewStyle().Padding(0, 1).Foreground(colorMuted),
		ChipActive: lipgloss.NewStyle().Padding(0, 1).Foreground(colorWarning).Bold(true),
	}
}

func NewHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorAccent)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(colorAccent)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(colorMuted)
	return h
}

func ApplyListTheme(m *list.Model, d *list.DefaultDelegate) {
	styles := m.Styles
	styles.Title = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1)
	styles.TitleBar = lipgloss.NewStyle().Padding(0, 0, 1, 0)
	styles.FilterPrompt = lipgloss.NewStyle().Foreground(colorAccent)
	styles.FilterCursor = lipgloss.NewStyle().Foreground(colorAccent)
	styles.StatusBar = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 0, 1, 0)
	styles.StatusEmpty = lipgloss.NewStyle().Foreground(colorMuted)
	styles.StatusBarActiveFilter = lipgloss.NewStyle()
	styles.StatusBarFilterCount = lipgloss.NewStyle().Foreground(colorMuted)
	styles.NoItems = lipgloss.NewStyle().Foreground(colorMuted)
	styles.PaginationStyle = lipgloss.NewStyle()
	styles.HelpStyle = lipgloss.NewStyle().Padding(1, 0, 0, 0)
	styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(colorAccent).SetString("•")
	styles.InactivePaginationDot = lipgloss.NewStyle().Foreground(colorMuted).SetString("•")
	styles.ArabicPagination = lipgloss.NewStyle().Foreground(colorMuted)
	styles.DividerDot = lipgloss.NewStyle().Foreground(colorMuted).SetString(" • ")
	m.Styles = styles
	m.Help = NewHelp()

	if d == nil {
		return
	}
	itemStyles := d.Styles
	itemStyles.NormalTitle = lipgloss.NewStyle().Padding(0, 0, 0, 2)
	itemStyles.NormalDesc = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 0, 0, 2)
	itemStyles.SelectedTitle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colorWarning).Foreground(colorWarning).Padding(0, 0, 0, 1)
	itemStyles.SelectedDesc = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colorWarning).Foreground(colorMuted).Padding(0, 0, 0, 1)
	itemStyles.DimmedTitle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 0, 0, 2)
	itemStyles.DimmedDesc = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 0, 0, 2)
	itemStyles.FilterMatch = lipgloss.NewStyle().Underline(true).Foreground(colorWarning)
	d.Styles = itemStyles
}

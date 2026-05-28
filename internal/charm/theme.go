package charm

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/huh"
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

// HuhTheme returns a huh.Theme that routes every color through this
// package's palette tokens (ANSI 16 slots). Because the slots are
// remapped by the user's terminal color scheme, huh forms inherit the
// same Catppuccin (or whatever the user is running) palette as the
// rest of the deck instead of being stuck on huh's hardcoded
// pink/indigo "Charm" colors.
func HuhTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = t.Focused.Base.BorderForeground(colorMuted)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(colorAccent).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(colorAccent).Bold(true).MarginBottom(1)
	t.Focused.Directory = t.Focused.Directory.Foreground(colorAccent)
	t.Focused.Description = t.Focused.Description.Foreground(colorMuted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(colorDanger)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(colorDanger)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(colorWarning)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(colorWarning)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(colorWarning)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(colorWarning)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colorWarning).Bold(true)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(colorWarning).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("• ")
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(lipgloss.Color(BgPanel)).
		Background(colorWarning).
		Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(colorMuted).
		Background(lipgloss.NoColor{})

	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colorWarning)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(colorMuted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colorAccent)

	// Blurred styles mirror Focused but drop the left bar so unselected
	// groups don't compete visually with the active one. Confirm-style
	// buttons also lose their warning highlight while blurred — the
	// Affirmative button is "selected" by binding but we don't want it
	// reading as the focused action until the user actually tabs there.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Blurred.FocusedButton = t.Focused.BlurredButton
	t.Blurred.Next = t.Focused.BlurredButton

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
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

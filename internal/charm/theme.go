package charm

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

var (
	colorBorder       = lipgloss.Color("240")
	colorAccent       = lipgloss.Color("205")
	colorAccentStrong = lipgloss.Color("212")
	colorText         = lipgloss.Color("252")
	colorMuted        = lipgloss.Color("241")
	colorHint         = lipgloss.Color("244")
	colorDanger       = lipgloss.Color("196")
	colorSurface      = lipgloss.Color("236")
	colorSelectedBg   = lipgloss.Color("212")
	colorSelectedFg   = lipgloss.Color("0")
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
		Subtitle:   lipgloss.NewStyle().Foreground(lipgloss.Color("246")),
		Label:      lipgloss.NewStyle().Bold(true).Foreground(colorText),
		Focused:    lipgloss.NewStyle().Foreground(colorAccentStrong).Bold(true),
		Dim:        lipgloss.NewStyle().Foreground(colorMuted),
		Hint:       lipgloss.NewStyle().Foreground(colorHint),
		Error:      lipgloss.NewStyle().Foreground(colorDanger).Bold(true),
		Chip:       lipgloss.NewStyle().Padding(0, 1).Foreground(colorText).Background(colorSurface),
		ChipActive: lipgloss.NewStyle().Padding(0, 1).Foreground(colorSelectedFg).Background(colorSelectedBg).Bold(true),
	}
}

func NewHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorAccentStrong)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorHint)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(colorAccentStrong)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorHint)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(colorMuted)
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(colorMuted)
	return h
}

func ApplyListTheme(m *list.Model, d *list.DefaultDelegate) {
	styles := m.Styles
	styles.Title = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1)
	styles.TitleBar = lipgloss.NewStyle().Padding(0, 0, 1, 0)
	styles.FilterPrompt = lipgloss.NewStyle().Foreground(colorAccentStrong)
	styles.FilterCursor = lipgloss.NewStyle().Foreground(colorAccentStrong)
	styles.StatusBar = lipgloss.NewStyle().Foreground(colorHint).Padding(0, 0, 1, 0)
	styles.StatusEmpty = lipgloss.NewStyle().Foreground(colorMuted)
	styles.StatusBarActiveFilter = lipgloss.NewStyle().Foreground(colorText)
	styles.StatusBarFilterCount = lipgloss.NewStyle().Foreground(colorMuted)
	styles.NoItems = lipgloss.NewStyle().Foreground(colorMuted)
	styles.PaginationStyle = lipgloss.NewStyle()
	styles.HelpStyle = lipgloss.NewStyle().Padding(1, 0, 0, 0)
	styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(colorAccentStrong).SetString("•")
	styles.InactivePaginationDot = lipgloss.NewStyle().Foreground(colorMuted).SetString("•")
	styles.ArabicPagination = lipgloss.NewStyle().Foreground(colorMuted)
	styles.DividerDot = lipgloss.NewStyle().Foreground(colorMuted).SetString(" • ")
	m.Styles = styles
	m.Help = NewHelp()

	if d == nil {
		return
	}
	itemStyles := d.Styles
	itemStyles.NormalTitle = lipgloss.NewStyle().Foreground(colorText).Padding(0, 0, 0, 2)
	itemStyles.NormalDesc = lipgloss.NewStyle().Foreground(colorHint).Padding(0, 0, 0, 2)
	itemStyles.SelectedTitle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colorAccentStrong).Foreground(colorAccentStrong).Bold(true).Padding(0, 0, 0, 1)
	itemStyles.SelectedDesc = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colorAccentStrong).Foreground(colorHint).Padding(0, 0, 0, 1)
	itemStyles.DimmedTitle = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 0, 0, 2)
	itemStyles.DimmedDesc = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 0, 0, 2)
	itemStyles.FilterMatch = lipgloss.NewStyle().Underline(true).Foreground(colorAccentStrong)
	d.Styles = itemStyles
}

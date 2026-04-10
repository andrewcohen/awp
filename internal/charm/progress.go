package charm

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	progressStep = lipgloss.NewStyle().Foreground(colorAccentStrong)
	progressOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	progressInfo = lipgloss.NewStyle().Foreground(colorHint)
	progressWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	progressSkip = lipgloss.NewStyle().Foreground(colorMuted)
	progressOut  = lipgloss.NewStyle().Foreground(colorMuted)
)

func RenderProgressLine(line string) string {
	switch {
	case strings.HasPrefix(line, "▶️ "):
		return progressStep.Render("▶") + " " + strings.TrimPrefix(line, "▶️ ")
	case strings.HasPrefix(line, "✅ "):
		return progressOK.Render("✓") + " " + strings.TrimPrefix(line, "✅ ")
	case strings.HasPrefix(line, "ℹ️ "):
		return progressInfo.Render("i") + " " + strings.TrimPrefix(line, "ℹ️ ")
	case strings.HasPrefix(line, "⚠️ "):
		return progressWarn.Render("!") + " " + strings.TrimPrefix(line, "⚠️ ")
	case strings.HasPrefix(line, "⏭️ "):
		return progressSkip.Render("→") + " " + strings.TrimPrefix(line, "⏭️ ")
	default:
		return line
	}
}

func RenderProgressOutputLine(line string) string {
	return progressOut.Render("  ↳ " + line)
}

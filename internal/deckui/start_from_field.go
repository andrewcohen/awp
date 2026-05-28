package deckui

import (
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// startFromField is a custom huh.Field for the new-workspace form's
// "Start from" picker. We hand-roll it instead of using huh.Select
// because Select's viewport-backed render adds scroll behavior we can't
// fully disable for a tiny static list (cursor pans the viewport,
// rebuilt options re-clamp YOffset against stale widths, etc.). With
// only two options and no filtering, plain text rendering is simpler
// and bulletproof.
//
// The field stays in lockstep with the form's bound startFromVal: it
// reads/writes through the pointer so SetPickedBookmark on the wrapper
// flips the cursor here too. Enter on the "pick" row sets pickPending,
// which the form wrapper polls each update to emit the OpenPicker
// action — on the "main" row Enter just advances to the next field.
type startFromField struct {
	options    []startFromOption
	value      *string
	focused    bool
	pickPending bool

	title       string
	description string
	width       int
	theme       *huh.Theme
}

type startFromOption struct {
	Label string
	Value string
}

func newStartFromField(title, description string, value *string, opts []startFromOption) *startFromField {
	return &startFromField{
		options:     opts,
		value:       value,
		title:       title,
		description: description,
	}
}

// SetOptions replaces the option list. The currently-selected value
// (via the bound pointer) is preserved as long as it still exists in
// the new options; otherwise the cursor falls back to the first option.
func (s *startFromField) SetOptions(opts []startFromOption) {
	s.options = opts
	if s.value == nil {
		return
	}
	for _, o := range opts {
		if o.Value == *s.value {
			return
		}
	}
	if len(opts) > 0 {
		*s.value = opts[0].Value
	}
}

// selectedIndex returns the index of the option whose Value matches the
// bound pointer, or 0 when no match (defensive — usually the bound
// value is one of the listed options).
func (s *startFromField) selectedIndex() int {
	if s.value == nil {
		return 0
	}
	for i, o := range s.options {
		if o.Value == *s.value {
			return i
		}
	}
	return 0
}

// ConsumePickPending atomically reads and clears the pick-pending flag.
// The form wrapper polls this after each update to trigger OpenPicker.
func (s *startFromField) ConsumePickPending() bool {
	if s.pickPending {
		s.pickPending = false
		return true
	}
	return false
}

// Bubble Tea Model

func (s *startFromField) Init() tea.Cmd { return nil }

func (s *startFromField) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !s.focused {
		return s, nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch k.String() {
	case "up", "k":
		idx := s.selectedIndex()
		if idx > 0 && s.value != nil {
			*s.value = s.options[idx-1].Value
		}
		return s, nil
	case "down", "j":
		idx := s.selectedIndex()
		if idx < len(s.options)-1 && s.value != nil {
			*s.value = s.options[idx+1].Value
		}
		return s, nil
	case "enter":
		if s.value != nil && *s.value == startFromPick {
			s.pickPending = true
			return s, nil
		}
		return s, huh.NextField
	case "tab":
		return s, huh.NextField
	case "shift+tab":
		return s, huh.PrevField
	}
	return s, nil
}

func (s *startFromField) View() string {
	theme := s.activeTheme()
	styles := s.activeStyles(theme)

	var sb strings.Builder
	if s.title != "" {
		sb.WriteString(styles.Title.Render(s.title))
		sb.WriteString("\n")
	}
	if s.description != "" {
		sb.WriteString(styles.Description.Render(s.description))
		sb.WriteString("\n")
	}

	cursor := styles.SelectSelector.String()
	if cursor == "" {
		cursor = "> "
	}
	cursorPad := strings.Repeat(" ", lipgloss.Width(cursor))
	selectedIdx := s.selectedIndex()

	for i, o := range s.options {
		if i == selectedIdx {
			row := lipgloss.JoinHorizontal(lipgloss.Left,
				cursor,
				styles.SelectedOption.Render(o.Label),
			)
			sb.WriteString(row)
		} else {
			row := lipgloss.JoinHorizontal(lipgloss.Left,
				cursorPad,
				styles.UnselectedOption.Render(o.Label),
			)
			sb.WriteString(row)
		}
		if i < len(s.options)-1 {
			sb.WriteString("\n")
		}
	}
	return styles.Base.Render(sb.String())
}

func (s *startFromField) activeTheme() *huh.Theme {
	if s.theme != nil {
		return s.theme
	}
	return huh.ThemeBase()
}

func (s *startFromField) activeStyles(t *huh.Theme) *huh.FieldStyles {
	if s.focused {
		return &t.Focused
	}
	return &t.Blurred
}

// Bubble Tea Events

func (s *startFromField) Blur() tea.Cmd  { s.focused = false; return nil }
func (s *startFromField) Focus() tea.Cmd { s.focused = true; return nil }

// Errors and Validation

func (s *startFromField) Error() error { return nil }

// Run / accessible — not used; this field always runs inside a form.

func (s *startFromField) Run() error                              { return nil }
func (s *startFromField) RunAccessible(_ io.Writer, _ io.Reader) error { return nil }

// Flags

func (s *startFromField) Skip() bool { return false }
func (s *startFromField) Zoom() bool { return false }

// KeyBinds — surfaces the bindings the help footer should list while
// this field is focused.
func (s *startFromField) KeyBinds() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select / pick bookmark")),
	}
}

// Configuration setters from huh.Field

func (s *startFromField) WithTheme(t *huh.Theme) huh.Field            { s.theme = t; return s }
func (s *startFromField) WithAccessible(bool) huh.Field               { return s }
func (s *startFromField) WithKeyMap(*huh.KeyMap) huh.Field            { return s }
func (s *startFromField) WithWidth(w int) huh.Field                   { s.width = w; return s }
func (s *startFromField) WithHeight(int) huh.Field                    { return s }
func (s *startFromField) WithPosition(huh.FieldPosition) huh.Field    { return s }

// Identity / value

func (s *startFromField) GetKey() string {
	return "start_from"
}

func (s *startFromField) GetValue() any {
	if s.value == nil {
		return ""
	}
	return *s.value
}

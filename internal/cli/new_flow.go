package cli

import (
	"errors"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/workspace"
)

type newFlowKind int

const (
	newFlowDefault newFlowKind = iota
	newFlowBookmark
	newFlowReview
)

type newFlowResult struct {
	kind     newFlowKind
	bookmark string
	prNumber int
}

type newFlowDeps struct {
	listBookmarks func() ([]string, error)
	listPRs       func() ([]struct {
		Number  int
		Title   string
		HeadRef string
		Author  string
		IsDraft bool
	}, error)
}

type newFlowStage int

const (
	stageMenu newFlowStage = iota
	stageBookmark
	stagePR
)

type newFlowModel struct {
	stage     newFlowStage
	cursor    int
	deps      newFlowDeps
	bookmarks []string
	prs       []struct {
		Number  int
		Title   string
		HeadRef string
		Author  string
		IsDraft bool
	}
	loading   bool
	loadErr   error
	filter    textinput.Model
	filtering bool
	result    newFlowResult
	cancel    bool
	chosen    bool
}

type bookmarksLoadedMsg struct {
	names []string
	err   error
}

type prsLoadedMsg struct {
	prs []struct {
		Number  int
		Title   string
		HeadRef string
		Author  string
		IsDraft bool
	}
	err error
}

func newNewFlowModel(deps newFlowDeps) newFlowModel {
	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 64
	return newFlowModel{stage: stageMenu, deps: deps, filter: fi}
}

func (m newFlowModel) Init() tea.Cmd { return nil }

func (m newFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bookmarksLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err
			return m, nil
		}
		m.bookmarks = msg.names
		return m, nil
	case prsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err
			return m, nil
		}
		m.prs = msg.prs
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			switch msg.String() {
			case "esc":
				m.filtering = false
				m.filter.Blur()
				m.filter.SetValue("")
				m.cursor = 0
				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()
				m.cursor = 0
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			return m, cmd
		}
		switch m.stage {
		case stageMenu:
			return m.handleMenuKey(msg)
		case stageBookmark:
			return m.handleBookmarkKey(msg)
		case stagePR:
			return m.handlePRKey(msg)
		}
	}
	return m, nil
}

func (m newFlowModel) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		m.cancel = true
		return m, tea.Quit
	case "j", "down":
		if m.cursor < 2 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "b":
		return m.enterBookmark()
	case "r":
		return m.enterPR()
	case "enter":
		switch m.cursor {
		case 0:
			m.result = newFlowResult{kind: newFlowDefault, bookmark: workspace.DefaultBookmark}
			m.chosen = true
			return m, tea.Quit
		case 1:
			return m.enterBookmark()
		case 2:
			return m.enterPR()
		}
	}
	return m, nil
}

func (m newFlowModel) enterBookmark() (tea.Model, tea.Cmd) {
	m.stage = stageBookmark
	m.cursor = 0
	m.loading = true
	m.loadErr = nil
	m.filter.SetValue("")
	return m, func() tea.Msg {
		names, err := m.deps.listBookmarks()
		return bookmarksLoadedMsg{names: names, err: err}
	}
}

func (m newFlowModel) enterPR() (tea.Model, tea.Cmd) {
	m.stage = stagePR
	m.cursor = 0
	m.loading = true
	m.loadErr = nil
	m.filter.SetValue("")
	return m, func() tea.Msg {
		prs, err := m.deps.listPRs()
		return prsLoadedMsg{prs: prs, err: err}
	}
}

func (m newFlowModel) filteredBookmarks() []string {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		return append([]string(nil), m.bookmarks...)
	}
	out := make([]string, 0, len(m.bookmarks))
	for _, n := range m.bookmarks {
		if strings.Contains(strings.ToLower(n), q) {
			out = append(out, n)
		}
	}
	return out
}

func (m newFlowModel) filteredPRs() []struct {
	Number  int
	Title   string
	HeadRef string
	Author  string
	IsDraft bool
} {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		return append(m.prs[:0:0], m.prs...)
	}
	out := m.prs[:0:0]
	for _, pr := range m.prs {
		if strings.Contains(strings.ToLower(pr.Title), q) || strings.Contains(strings.ToLower(pr.HeadRef), q) {
			out = append(out, pr)
		}
	}
	return out
}

func (m newFlowModel) handleBookmarkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		m.cancel = true
		return m, tea.Quit
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, nil
	case "j", "down":
		if m.cursor < len(m.filteredBookmarks())-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "enter":
		picks := m.filteredBookmarks()
		if len(picks) == 0 {
			return m, nil
		}
		m.result = newFlowResult{kind: newFlowBookmark, bookmark: picks[m.cursor]}
		m.chosen = true
		return m, tea.Quit
	}
	return m, nil
}

func (m newFlowModel) handlePRKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		m.cancel = true
		return m, tea.Quit
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, nil
	case "j", "down":
		if m.cursor < len(m.filteredPRs())-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "enter":
		picks := m.filteredPRs()
		if len(picks) == 0 {
			return m, nil
		}
		m.result = newFlowResult{kind: newFlowReview, prNumber: picks[m.cursor].Number}
		m.chosen = true
		return m, tea.Quit
	}
	return m, nil
}

func (m newFlowModel) View() string {
	switch m.stage {
	case stageMenu:
		return m.viewMenu()
	case stageBookmark:
		return m.viewBookmark()
	case stagePR:
		return m.viewPR()
	}
	return ""
}

func (m newFlowModel) viewMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Accent)).Render("new workspace: choose start")
	options := []struct {
		label string
		hint  string
	}{
		{"main", "start from main (edit in form if needed)"},
		{"bookmark [b]", "pick a jj bookmark to base it on"},
		{"review [r]", "review an open PR"},
	}
	rows := []string{title, ""}
	for i, opt := range options {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			prefix = "┃ "
			style = style.Foreground(lipgloss.Color(charm.Warning)).Bold(true)
		}
		rows = append(rows, style.Render(prefix+opt.label))
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Render("   "+opt.hint))
	}
	rows = append(rows, "", lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Render("↑/↓ j/k · enter select · b/r quick · esc cancel"))
	return strings.Join(rows, "\n") + "\n"
}

func (m newFlowModel) viewBookmark() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Accent)).Render("bookmark: pick one")
	rows := []string{title, ""}
	if m.loading {
		rows = append(rows, "Loading bookmarks...")
		return strings.Join(rows, "\n") + "\n"
	}
	if m.loadErr != nil {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Render("error: "+m.loadErr.Error()))
		return strings.Join(rows, "\n") + "\n"
	}
	if m.filtering || strings.TrimSpace(m.filter.Value()) != "" {
		rows = append(rows, "/"+m.filter.View(), "")
	}
	picks := m.filteredBookmarks()
	if len(picks) == 0 {
		rows = append(rows, "No bookmarks match.")
	}
	for i, name := range picks {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			prefix = "┃ "
			style = style.Foreground(lipgloss.Color(charm.Warning)).Bold(true)
		}
		rows = append(rows, style.Render(prefix+name))
	}
	rows = append(rows, "", lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Render("↑/↓ j/k · / filter · enter select · esc cancel"))
	return strings.Join(rows, "\n") + "\n"
}

func (m newFlowModel) viewPR() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Accent)).Render("review: pick a PR")
	rows := []string{title, ""}
	if m.loading {
		rows = append(rows, "Loading PRs...")
		return strings.Join(rows, "\n") + "\n"
	}
	if m.loadErr != nil {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Render("error: "+m.loadErr.Error()))
		return strings.Join(rows, "\n") + "\n"
	}
	if m.filtering || strings.TrimSpace(m.filter.Value()) != "" {
		rows = append(rows, "/"+m.filter.View(), "")
	}
	picks := m.filteredPRs()
	if len(picks) == 0 {
		rows = append(rows, "No PRs match.")
	}
	for i, pr := range picks {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			prefix = "┃ "
			style = style.Foreground(lipgloss.Color(charm.Warning)).Bold(true)
		}
		draft := ""
		if pr.IsDraft {
			draft = " [draft]"
		}
		rows = append(rows, style.Render(prefix+pr.Title+draft+" — "+pr.HeadRef))
	}
	rows = append(rows, "", lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Render("↑/↓ j/k · / filter · enter select · esc cancel"))
	return strings.Join(rows, "\n") + "\n"
}

// runNewFlowPreScreen drives the standalone 3-option menu used by `awp open`.
// It returns the user's selection or an error/cancel.
func runNewFlowPreScreen(runner Runner, in io.Reader, out io.Writer) (newFlowResult, error) {
	if charm.IsDumbTerminal() {
		return newFlowResult{}, errors.New("interactive new flow not available in dumb terminal")
	}
	deps := newFlowDeps{
		listBookmarks: func() ([]string, error) {
			return jj.New(runner).AllBookmarks()
		},
		listPRs: func() ([]struct {
			Number  int
			Title   string
			HeadRef string
			Author  string
			IsDraft bool
		}, error) {
			gh := github.New(runner)
			prs, err := gh.ListPRs()
			if err != nil {
				return nil, err
			}
			out := make([]struct {
				Number  int
				Title   string
				HeadRef string
				Author  string
				IsDraft bool
			}, len(prs))
			for i, pr := range prs {
				out[i].Number = pr.Number
				out[i].Title = pr.Title
				out[i].HeadRef = pr.HeadRef
				out[i].Author = pr.Author.Login
				out[i].IsDraft = pr.IsDraft
			}
			return out, nil
		},
	}
	model := newNewFlowModel(deps)
	prog := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	final, err := prog.Run()
	if err != nil {
		return newFlowResult{}, err
	}
	result, ok := final.(newFlowModel)
	if !ok {
		return newFlowResult{}, errors.New("unexpected new flow state")
	}
	if result.cancel || !result.chosen {
		return newFlowResult{}, ErrOpenCancelled
	}
	return result.result, nil
}


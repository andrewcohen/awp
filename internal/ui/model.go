package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/diff"
)

type Focus int

const (
	FocusFiles Focus = iota
	FocusHunks
	FocusFilter
)

const DefaultRefreshInterval = 0

type OpenFunc func(filePath string, line int) tea.Cmd

type Model struct {
	RepoRoot        string
	RefreshInterval time.Duration
	LoadDiff        func() (string, error)
	OpenFile        OpenFunc

	files          []diff.FileDiff
	filtered       []diff.FileDiff
	filesCursor    int
	hunksCursor    int
	hunkScroll     int
	focus          Focus
	filterInput    textinput.Model
	width          int
	height         int
	status         string
	statusErr      bool
	refreshing     bool
}

type diffLoadedMsg struct {
	files []diff.FileDiff
	err   error
}

type autoRefreshTickMsg struct{}

func New(repoRoot string, loadFn func() (string, error), openFn OpenFunc) Model {
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.CharLimit = 128
	return Model{
		RepoRoot:        repoRoot,
		RefreshInterval: DefaultRefreshInterval,
		LoadDiff:        loadFn,
		OpenFile:        openFn,
		filterInput:     ti,
		status:          "loading...",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadDiffCmd(m.LoadDiff), scheduleRefresh(m.RefreshInterval))
}

func scheduleRefresh(d time.Duration) tea.Cmd {
	if d <= 0 {
		return nil
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return autoRefreshTickMsg{} })
}

func loadDiffCmd(fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		raw, err := fn()
		if err != nil {
			return diffLoadedMsg{err: err}
		}
		return diffLoadedMsg{files: diff.ParseGitDiff(raw)}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case diffLoadedMsg:
		m.refreshing = false
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			m.statusErr = true
			return m, scheduleRefresh(m.RefreshInterval)
		}
		m.files = msg.files
		m.applyFilter()
		if m.filesCursor >= len(m.filtered) {
			m.filesCursor = max(0, len(m.filtered)-1)
		}
		m.hunksCursor = 0
		m.hunkScroll = 0
		if len(m.filtered) == 0 {
			m.status = "no changes"
		} else {
			m.status = fmt.Sprintf("%d file(s) changed — manual refresh (r)", len(m.filtered))
		}
		m.statusErr = false
		return m, scheduleRefresh(m.RefreshInterval)
	case autoRefreshTickMsg:
		if !m.refreshing {
			m.refreshing = true
			return m, loadDiffCmd(m.LoadDiff)
		}
		return m, scheduleRefresh(m.RefreshInterval)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	if m.focus == FocusFilter {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.applyFilter()
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.focus == FocusFilter {
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		case "esc", "enter":
			m.focus = FocusFiles
			m.filterInput.Blur()
			if key == "esc" {
				m.filterInput.SetValue("")
				m.applyFilter()
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		m.refreshing = true
		m.status = "refreshing..."
		return m, loadDiffCmd(m.LoadDiff)
	case "/":
		m.focus = FocusFilter
		m.filterInput.Focus()
		return m, nil
	case "tab", "l", "right":
		if m.focus == FocusFiles {
			m.focus = FocusHunks
		}
		return m, nil
	case "h", "left":
		if m.focus == FocusHunks {
			m.focus = FocusFiles
		}
		return m, nil
	case "ctrl+d":
		m.pageDown()
		return m, nil
	case "ctrl+u":
		m.pageUp()
		return m, nil
	}

	if m.focus == FocusFiles {
		switch key {
		case "j", "down":
			if m.filesCursor < len(m.filtered)-1 {
				m.filesCursor++
				m.hunksCursor = 0
				m.hunkScroll = 0
			}
		case "k", "up":
			if m.filesCursor > 0 {
				m.filesCursor--
				m.hunksCursor = 0
				m.hunkScroll = 0
			}
		case "enter", "e":
			return m, m.openCurrentFile()
		}
	}

	if m.focus == FocusHunks {
		if len(m.filtered) == 0 {
			return m, nil
		}
		current := m.filtered[m.filesCursor]
		switch key {
		case "j", "down":
			if m.hunksCursor < len(current.Hunks)-1 {
				m.hunksCursor++
				m.ensureSelectedHunkVisible()
			}
		case "k", "up":
			if m.hunksCursor > 0 {
				m.hunksCursor--
				m.ensureSelectedHunkVisible()
			}
		case "enter", "e":
			return m, m.openAtHunk()
		}
	}
	return m, nil
}

func (m *Model) applyFilter() {
	needle := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	m.filtered = m.filtered[:0]
	if needle == "" {
		m.filtered = append(m.filtered, m.files...)
		return
	}
	for _, f := range m.files {
		if strings.Contains(strings.ToLower(diff.DisplayPath(f)), needle) {
			m.filtered = append(m.filtered, f)
		}
	}
	if m.filesCursor >= len(m.filtered) {
		m.filesCursor = max(0, len(m.filtered)-1)
		m.hunksCursor = 0
		m.hunkScroll = 0
	}
}

func (m *Model) pageDown() {
	step := m.pageStep()
	if m.focus == FocusHunks {
		m.hunkScroll += step
		m.clampHunkScroll()
		return
	}
	if len(m.filtered) == 0 {
		return
	}
	m.filesCursor = min(len(m.filtered)-1, m.filesCursor+step)
	m.hunksCursor = 0
	m.hunkScroll = 0
}

func (m *Model) pageUp() {
	step := m.pageStep()
	if m.focus == FocusHunks {
		m.hunkScroll = max(0, m.hunkScroll-step)
		return
	}
	m.filesCursor = max(0, m.filesCursor-step)
	m.hunksCursor = 0
	m.hunkScroll = 0
}

func (m *Model) ensureSelectedHunkVisible() {
	if len(m.filtered) == 0 || m.filesCursor >= len(m.filtered) {
		m.hunkScroll = 0
		return
	}
	visibleHeight := m.hunkContentHeight()
	if visibleHeight <= 0 {
		return
	}
	start, end := m.selectedHunkRowRange(m.filtered[m.filesCursor])
	if start < m.hunkScroll {
		m.hunkScroll = start
		return
	}
	if end > m.hunkScroll+visibleHeight {
		m.hunkScroll = end - visibleHeight
	}
	m.clampHunkScroll()
}

func (m *Model) clampHunkScroll() {
	if len(m.filtered) == 0 || m.filesCursor >= len(m.filtered) {
		m.hunkScroll = 0
		return
	}
	maxScroll := max(0, m.totalHunkRows(m.filtered[m.filesCursor])-m.hunkContentHeight())
	m.hunkScroll = min(maxScroll, max(0, m.hunkScroll))
}

func (m Model) hunkContentHeight() int {
	return max(1, max(0, m.height-4)-1)
}

func (m Model) totalHunkRows(f diff.FileDiff) int {
	rows := 0
	for _, h := range f.Hunks {
		rows += 1 + len(h.Lines)
	}
	return rows
}

func (m Model) selectedHunkRowRange(f diff.FileDiff) (int, int) {
	start := 0
	for i, h := range f.Hunks {
		end := start + 1 + len(h.Lines)
		if i == m.hunksCursor {
			return start, end
		}
		start = end
	}
	return 0, 0
}

func (m Model) pageStep() int {
	step := max(1, (m.height-4)/2)
	if step < 1 {
		return 1
	}
	return step
}

func (m Model) openCurrentFile() tea.Cmd {
	if len(m.filtered) == 0 || m.OpenFile == nil {
		return nil
	}
	f := m.filtered[m.filesCursor]
	return m.OpenFile(m.resolveFilePath(f), diff.FirstChangedLine(f))
}

func (m Model) openAtHunk() tea.Cmd {
	if len(m.filtered) == 0 || m.OpenFile == nil {
		return nil
	}
	f := m.filtered[m.filesCursor]
	if len(f.Hunks) == 0 {
		return m.openCurrentFile()
	}
	if m.hunksCursor >= len(f.Hunks) {
		m.hunksCursor = len(f.Hunks) - 1
	}
	return m.OpenFile(m.resolveFilePath(f), diff.HunkChangedLine(f.Hunks[m.hunksCursor]))
}

func (m Model) resolveFilePath(f diff.FileDiff) string {
	p := f.NewPath
	if p == "" {
		p = f.OldPath
	}
	if m.RepoRoot == "" || p == "" {
		return p
	}
	return filepath.Join(m.RepoRoot, filepath.FromSlash(p))
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	leftWidth := max(24, m.width/3)
	rightWidth := max(30, m.width-leftWidth)
	contentHeight := max(6, m.height-4)

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderFileList(leftWidth, contentHeight),
		m.renderHunkPanel(rightWidth, contentHeight),
	)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
}

var (
	styleHeader             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110")).Padding(0, 1)
	styleSelected           = lipgloss.NewStyle().Background(lipgloss.Color("60")).Foreground(lipgloss.Color("189")).Bold(true)
	styleDim                = lipgloss.NewStyle().Foreground(lipgloss.Color("103"))
	styleMuted              = lipgloss.NewStyle().Foreground(lipgloss.Color("146"))
	stylePathDir            = lipgloss.NewStyle().Foreground(lipgloss.Color("147"))
	stylePathBase           = lipgloss.NewStyle().Foreground(lipgloss.Color("189")).Bold(true)
	styleAdded              = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	styleDeleted            = lipgloss.NewStyle().Foreground(lipgloss.Color("210"))
	styleContext            = lipgloss.NewStyle().Foreground(lipgloss.Color("152"))
	styleLineNo             = lipgloss.NewStyle().Foreground(lipgloss.Color("103"))
	styleStatus             = lipgloss.NewStyle().Foreground(lipgloss.Color("147")).Padding(0, 1)
	styleStatusErr          = lipgloss.NewStyle().Foreground(lipgloss.Color("210")).Padding(0, 1)
	styleHunkHeader         = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	styleFocusBorder        = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("110"))
	styleNormalBorder       = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("60"))
	styleAddedBadge         = lipgloss.NewStyle().Foreground(lipgloss.Color("24")).Background(lipgloss.Color("114")).Bold(true).Padding(0, 1)
	styleDeletedBadge       = lipgloss.NewStyle().Foreground(lipgloss.Color("234")).Background(lipgloss.Color("210")).Bold(true).Padding(0, 1)
	styleModifiedBadge      = lipgloss.NewStyle().Foreground(lipgloss.Color("234")).Background(lipgloss.Color("223")).Bold(true).Padding(0, 1)
	styleRenameBadge        = lipgloss.NewStyle().Foreground(lipgloss.Color("24")).Background(lipgloss.Color("117")).Bold(true).Padding(0, 1)
	styleSelectedBadge      = lipgloss.NewStyle().Foreground(lipgloss.Color("24")).Background(lipgloss.Color("189")).Bold(true).Padding(0, 1)
	styleSelectedPathDir    = lipgloss.NewStyle().Foreground(lipgloss.Color("225"))
	styleSelectedPathBase   = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	styleSelectedLineNo     = lipgloss.NewStyle().Foreground(lipgloss.Color("225"))
	styleSelectedHunkHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("24")).Background(lipgloss.Color("117")).Bold(true)
)

func (m Model) renderHeader() string {
	name := filepath.Base(m.RepoRoot)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = m.RepoRoot
	}
	if name == "" {
		name = "current repo"
	}
	return styleHeader.Render(" awp diff  repo: " + name + " ")
}

func (m Model) renderFooter() string {
	hint := "j/k:move  ctrl+u/d:page  h/l:switch  e/enter:open  r:refresh  /:filter  q:quit"
	filterLine := strings.Repeat(" ", max(1, m.width))
	if m.focus == FocusFilter {
		hint = "type to filter — enter:confirm  esc:clear"
		filterLine = "  Filter files: " + m.filterInput.View()
	}
	statusStyle := styleStatus
	if m.statusErr {
		statusStyle = styleStatusErr
	}
	st := statusStyle.Render(m.status)
	footerLine := lipgloss.JoinHorizontal(lipgloss.Left, st, "  ", styleDim.Render(hint))
	return lipgloss.JoinVertical(lipgloss.Left, filterLine, footerLine)
}

func (m Model) renderFileList(width, height int) string {
	border := styleNormalBorder
	if m.focus == FocusFiles {
		border = styleFocusBorder
	}
	rows := []string{styleDim.Render(fmt.Sprintf(" Files (%d)", len(m.filtered)))}
	start, end := visibleRange(m.filesCursor, max(1, height-2), len(m.filtered))
	contentWidth := width - 4
	for i := start; i < end; i++ {
		row := m.renderFileRow(m.filtered[i], contentWidth, i == m.filesCursor)
		if i == m.filesCursor {
			row = styleSelected.Width(contentWidth).Render(row)
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m Model) renderHunkPanel(width, height int) string {
	border := styleNormalBorder
	if m.focus == FocusHunks {
		border = styleFocusBorder
	}
	if len(m.filtered) == 0 {
		return border.Width(width - 2).Height(height).Render(styleDim.Render(" No changes"))
	}
	f := m.filtered[m.filesCursor]
	rows := []string{m.renderHunkTitle(f, width-4)}
	if len(f.Hunks) == 0 {
		rows = append(rows, styleDim.Render(" rename-only, binary, or empty diff body"))
		return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
	}

	contentRows := make([]string, 0, m.totalHunkRows(f))
	for i, h := range f.Hunks {
		hdrStyle := styleHunkHeader
		if i == m.hunksCursor && m.focus == FocusHunks {
			hdrStyle = styleSelectedHunkHeader
		}
		contentRows = append(contentRows, hdrStyle.Width(width-4).Render(fmt.Sprintf(" @@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)))
		contentRows = append(contentRows, renderHunkLines(h, width-4)...)
	}

	visibleHeight := max(1, height-1)
	scroll := min(max(0, m.hunkScroll), max(0, len(contentRows)-visibleHeight))
	end := min(len(contentRows), scroll+visibleHeight)
	rows = append(rows, contentRows[scroll:end]...)
	for len(rows) < height {
		rows = append(rows, "")
	}
	return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m Model) renderFileRow(f diff.FileDiff, width int, selected bool) string {
	badge := statusBadge(f.Status, selected)
	path := renderPath(diff.DisplayPath(f), width-lipgloss.Width(badge)-1, selected)
	return badge + " " + path
}

func (m Model) renderHunkTitle(f diff.FileDiff, width int) string {
	badge := statusBadge(f.Status, false)
	label := renderPath(diff.DisplayPath(f), max(10, width-lipgloss.Width(badge)-1), false)
	meta := styleMuted.Render(fmt.Sprintf(" (%d hunk%s)", len(f.Hunks), plural(len(f.Hunks))))
	return truncateStyled(badge+" "+label+meta, width)
}

func statusBadge(status string, selected bool) string {
	var style lipgloss.Style
	switch status {
	case "A":
		style = styleAddedBadge
	case "D":
		style = styleDeletedBadge
	case "R":
		style = styleRenameBadge
	default:
		style = styleModifiedBadge
	}
	if selected {
		style = styleSelectedBadge
	}
	return style.Render(status)
}

func renderPath(path string, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	if strings.Contains(path, " → ") {
		parts := strings.SplitN(path, " → ", 2)
		left := renderSinglePath(parts[0], max(1, (width-3)/2), selected)
		right := renderSinglePath(parts[1], max(1, width-lipgloss.Width(left)-3), selected)
		return truncateStyled(left+styleMuted.Render(" → ")+right, width)
	}
	return renderSinglePath(path, width, selected)
}

func renderSinglePath(path string, width int, selected bool) string {
	path = truncate(path, width)
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if dir == "." || dir == string(filepath.Separator) {
		dir = ""
	}
	dirStyle := stylePathDir
	baseStyle := stylePathBase
	if selected {
		dirStyle = styleSelectedPathDir
		baseStyle = styleSelectedPathBase
	}
	if dir == "" {
		return baseStyle.Render(base)
	}
	return dirStyle.Render(dir+"/") + baseStyle.Render(base)
}

func renderHunkLines(h diff.Hunk, width int) []string {
	if width <= 0 {
		return nil
	}
	oldWidth, newWidth := hunkLineNumberWidths(h)
	lines := make([]string, 0, len(h.Lines))
	oldLine, newLine := h.OldStart, h.NewStart
	for _, l := range h.Lines {
		switch l.Type {
		case '+':
			lines = append(lines, renderDecoratedLine('+', 0, newLine, oldWidth, newWidth, styleAdded.Render(l.Content), width, false))
			newLine++
		case '-':
			lines = append(lines, renderDecoratedLine('-', oldLine, 0, oldWidth, newWidth, styleDeleted.Render(l.Content), width, false))
			oldLine++
		default:
			lines = append(lines, renderDecoratedLine(' ', oldLine, newLine, oldWidth, newWidth, styleContext.Render(l.Content), width, false))
			oldLine++
			newLine++
		}
	}
	return lines
}

func hunkLineNumberWidths(h diff.Hunk) (int, int) {
	oldWidth, newWidth := 1, 1
	oldLine, newLine := h.OldStart, h.NewStart
	for _, l := range h.Lines {
		switch l.Type {
		case '+':
			newWidth = max(newWidth, len(strconv.Itoa(newLine)))
			newLine++
		case '-':
			oldWidth = max(oldWidth, len(strconv.Itoa(oldLine)))
			oldLine++
		default:
			oldWidth = max(oldWidth, len(strconv.Itoa(oldLine)))
			newWidth = max(newWidth, len(strconv.Itoa(newLine)))
			oldLine++
			newLine++
		}
	}
	return oldWidth, newWidth
}

func renderDecoratedLine(kind byte, oldLine, newLine int, oldWidth, newWidth int, content string, width int, selected bool) string {
	oldText := lineNoText(oldLine)
	newText := lineNoText(newLine)
	lineStyle := styleLineNo
	if selected {
		lineStyle = styleSelectedLineNo
	}
	gutter := string(kind)
	if kind == ' ' {
		gutter = "│"
	}
	gutterStyle := styleContext
	if kind == '+' {
		gutterStyle = styleAdded
	} else if kind == '-' {
		gutterStyle = styleDeleted
	}
	prefix := lineStyle.Render(fmt.Sprintf("%*s %*s ", oldWidth, oldText, newWidth, newText)) + gutterStyle.Render(gutter+" ")
	return truncateStyled(prefix+content, width)
}

func lineNoText(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncateStyled(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func visibleRange(cursor, height, total int) (int, int) {
	if total == 0 {
		return 0, 0
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > total {
		end = total
		start = max(0, end-height)
	}
	return start, end
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

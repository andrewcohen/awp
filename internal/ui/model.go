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
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
)

type Focus int

const (
	FocusFiles Focus = iota
	FocusHunks
	FocusFilter
)

const DefaultRefreshInterval = 0

// minBodyHeight is the smallest two-pane body we will render into. Below
// this the panes have no room for content and the borders collide.
const minBodyHeight = 6

// chromeHeight is the rows this model's own header + footer occupy when it
// runs standalone (`awp diff`). Embedded, the host owns the chrome and
// calls SetSize with the body budget directly.
const chromeHeight = 4

// hScrollStep is how many columns h/l pan the hunk pane. Single columns make
// reading the tail of a long line tedious; a tab-ish step gets there in a
// few presses while still landing predictably.
const hScrollStep = 8

// minVisibleColumns is how much of the longest line must stay on screen, so
// panning can't leave the pane blank.
const minVisibleColumns = 16

type OpenFunc func(filePath string, line int) tea.Cmd

type Model struct {
	RepoRoot        string
	RefreshInterval time.Duration
	LoadDiff        func() (string, error)
	OpenFile        OpenFunc

	files       []diff.FileDiff
	filtered    []diff.FileDiff
	filesCursor int
	hunksCursor int
	hunkScroll  int
	focus       Focus
	filterInput textinput.Model
	width       int
	// bodyHeight is the height of the two-pane body (file list + hunks),
	// excluding this model's own header/footer. Standalone it is derived
	// from the terminal height; embedded (the deck's `c` modal) the host
	// sets it directly via SetSize, since the host owns the chrome.
	bodyHeight int
	// hunkWidth is the hunk pane's content width, cached so the update path
	// (scroll clamping, cursor sync) can lay out rows at the same width the
	// renderer will use. Wrapped lines occupy more than one row, so that
	// geometry is width-dependent.
	hunkWidth int
	wrap      bool
	// hunkHScroll is how many columns the hunk pane's line content is panned
	// left. Only meaningful when wrap is off — wrapped lines have no
	// horizontal overflow.
	hunkHScroll int
	status      string
	statusErr   bool
	refreshing  bool
}

// SetSize sizes the viewer for a host that owns its own chrome: width is
// the full width available to the body, bodyHeight the number of rows the
// two panes may occupy. Standalone use goes through tea.WindowSizeMsg
// instead.
func (m *Model) SetSize(width, bodyHeight int) {
	m.width = width
	m.bodyHeight = max(minBodyHeight, bodyHeight)
	_, right := paneWidths(width)
	m.hunkWidth = right - 4
	m.clampHunkScroll()
}

// paneWidths splits the body between the file list and the hunk pane. Both
// View and SetSize go through this so the cached hunkWidth matches what the
// renderer actually uses.
func paneWidths(width int) (left, right int) {
	left = max(24, width/3)
	return left, max(30, width-left)
}

// Status returns the viewer's status text and whether it is an error, so a
// host can surface it in its own footer.
func (m Model) Status() (string, bool) { return m.status, m.statusErr }

// Filtering reports whether the filter input has focus. A host must not
// treat keys as its own bindings while this is true — they belong to the
// filter.
func (m Model) Filtering() bool { return m.focus == FocusFilter }

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
		m.SetSize(msg.Width, msg.Height-chromeHeight)
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
		m.resetHunkView()
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
	case "w":
		// Toggling changes how many rows each line occupies, so the scroll
		// offset and hunk cursor have to be re-derived against the new
		// geometry. Wrapped lines have no horizontal overflow left to pan
		// over, so the column offset is dropped rather than kept hidden.
		m.wrap = !m.wrap
		if m.wrap {
			m.hunkHScroll = 0
		}
		m.clampHunkScroll()
		m.syncHunkCursorToScroll()
		return m, nil
	case "tab", "shift+tab":
		if m.focus == FocusFiles {
			m.focus = FocusHunks
		} else {
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
				m.resetHunkView()
			}
		case "k", "up":
			if m.filesCursor > 0 {
				m.filesCursor--
				m.resetHunkView()
			}
		// Drill into the selected file. tab does the same thing, but enter is
		// what a two-pane layout invites you to press on a list row.
		case "enter":
			m.focus = FocusHunks
		case "e":
			return m, m.openCurrentFile()
		}
	}

	if m.focus == FocusHunks {
		if len(m.filtered) == 0 {
			return m, nil
		}
		switch key {
		// j/k scroll the pane a line at a time, like ctrl+d/ctrl+u scroll it
		// a half-page at a time. Moving the hunk cursor instead would leave
		// a file with a single large hunk unscrollable.
		case "j", "down":
			m.scrollHunks(1)
		case "k", "up":
			m.scrollHunks(-1)
		case "l", "right":
			m.scrollHunksHorizontally(hScrollStep)
		case "h", "left":
			m.scrollHunksHorizontally(-hScrollStep)
		case "}", "]":
			m.jumpHunk(1)
		case "{", "[":
			m.jumpHunk(-1)
		case "e":
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
		m.resetHunkView()
	}
}

func (m *Model) pageDown() {
	step := m.pageStep()
	if m.focus == FocusHunks {
		m.scrollHunks(step)
		return
	}
	if len(m.filtered) == 0 {
		return
	}
	m.filesCursor = min(len(m.filtered)-1, m.filesCursor+step)
	m.resetHunkView()
}

func (m *Model) pageUp() {
	step := m.pageStep()
	if m.focus == FocusHunks {
		m.scrollHunks(-step)
		return
	}
	m.filesCursor = max(0, m.filesCursor-step)
	m.resetHunkView()
}

// resetHunkView returns the hunk pane to the top-left. Called whenever the
// selected file changes — carrying scroll or pan across files would open the
// next one mid-line.
func (m *Model) resetHunkView() {
	m.hunksCursor = 0
	m.hunkScroll = 0
	m.hunkHScroll = 0
}

// scrollHunksHorizontally pans the hunk pane's line content by delta
// columns. No-op under wrap, where nothing overflows the pane.
func (m *Model) scrollHunksHorizontally(delta int) {
	if m.wrap {
		return
	}
	m.hunkHScroll = max(0, m.hunkHScroll+delta)
	m.clampHunkHScroll()
}

// clampHunkHScroll stops the pan before the longest line has scrolled
// entirely out of view, so the pane can't be panned into empty space.
func (m *Model) clampHunkHScroll() {
	f, ok := m.currentFile()
	if !ok {
		m.hunkHScroll = 0
		return
	}
	m.hunkHScroll = min(m.hunkHScroll, max(0, maxContentWidth(f)-minVisibleColumns))
}

// maxContentWidth is the display width of the longest line in a file's
// hunks. Measured on the raw content (no styling applied yet), so it needs
// no ANSI handling.
func maxContentWidth(f diff.FileDiff) int {
	widest := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			widest = max(widest, lipgloss.Width(l.Content))
		}
	}
	return widest
}

// scrollHunks scrolls the hunk pane by delta rows and re-points the hunk
// cursor at whatever is now on top.
func (m *Model) scrollHunks(delta int) {
	m.hunkScroll += delta
	m.clampHunkScroll()
	m.syncHunkCursorToScroll()
}

// syncHunkCursorToScroll points the hunk cursor at the hunk owning the top
// visible row, so the highlighted header — and the line `e` opens — track
// what's actually on screen rather than a cursor the user can no longer see.
func (m *Model) syncHunkCursorToScroll() {
	layout, ok := m.hunkLayout()
	if !ok {
		return
	}
	m.hunksCursor = layout.hunkAtRow(m.hunkScroll)
}

// jumpHunk moves the hunk cursor by delta hunks and scrolls that hunk's
// header to the top of the pane — the gesture j/k used to serve before it
// became a line scroll.
func (m *Model) jumpHunk(delta int) {
	layout, ok := m.hunkLayout()
	if !ok {
		return
	}
	next := m.hunksCursor + delta
	if next < 0 || next >= len(layout.starts) {
		return
	}
	m.hunksCursor = next
	m.hunkScroll = layout.starts[next]
	m.clampHunkScroll()
}

// currentFile is the file the cursor is on, if any.
func (m Model) currentFile() (diff.FileDiff, bool) {
	if len(m.filtered) == 0 || m.filesCursor >= len(m.filtered) {
		return diff.FileDiff{}, false
	}
	return m.filtered[m.filesCursor], true
}

func (m *Model) clampHunkScroll() {
	layout, ok := m.hunkLayout()
	if !ok {
		m.hunkScroll = 0
		return
	}
	maxScroll := max(0, len(layout.rows)-m.hunkContentHeight())
	m.hunkScroll = min(maxScroll, max(0, m.hunkScroll))
}

func (m Model) hunkContentHeight() int {
	return max(1, m.bodyHeight-1)
}

func (m Model) pageStep() int {
	return max(1, m.bodyHeight/2)
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
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		m.Body(m.width, m.bodyHeight),
		m.renderFooter(),
	)
}

// Body renders just the two panes — file list and hunks — at the given
// size, without this model's header or footer. A host that owns its own
// chrome (the deck's `c` modal) renders this instead of View.
func (m Model) Body(width, height int) string {
	if width <= 0 {
		return ""
	}
	height = max(minBodyHeight, height)
	leftWidth, rightWidth := paneWidths(width)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderFileList(leftWidth, height),
		m.renderHunkPanel(rightWidth, height),
	)
}

var (
	styleHeader             = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Accent)).Padding(0, 1)
	styleSelected           = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	styleDim                = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleMuted              = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	stylePathDir            = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	stylePathBase           = lipgloss.NewStyle().Bold(true)
	styleAdded              = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success))
	styleDeleted            = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger))
	styleContext            = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleLineNo             = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleStatus             = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Padding(0, 1)
	styleStatusErr          = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Padding(0, 1)
	styleHunkHeader         = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true)
	styleFocusBorder        = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(charm.Accent))
	styleNormalBorder       = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(charm.Muted))
	styleAddedBadge         = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success)).Bold(true).Padding(0, 1)
	styleDeletedBadge       = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Bold(true).Padding(0, 1)
	styleModifiedBadge      = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true).Padding(0, 1)
	styleRenameBadge        = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true).Padding(0, 1)
	styleSelectedBadge      = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true).Padding(0, 1)
	styleSelectedPathDir    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning))
	styleSelectedPathBase   = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning))
	styleSelectedLineNo     = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning))
	styleSelectedHunkHeader = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
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
	hint := "j/k:move  h/l:pan  {/}:hunk  ctrl+u/d:page  tab/enter:pane  e:open  w:wrap  r:refresh  /:filter  q:quit"
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

	contentRows := layoutHunks(f, width-4, m.wrap, m.hunkHScroll, m.selectedHunkStyler()).rows

	visibleHeight := max(1, height-1)
	scroll := min(max(0, m.hunkScroll), max(0, len(contentRows)-visibleHeight))
	end := min(len(contentRows), scroll+visibleHeight)
	rows = append(rows, contentRows[scroll:end]...)
	for len(rows) < height {
		rows = append(rows, "")
	}
	return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

// hunkLayout is the rendered geometry of the hunk pane for one file: every
// content row, plus the row index each hunk's header landed on.
//
// It exists because a wrapped line occupies more than one row, so nothing
// can assume "one row per hunk line" — scroll clamping, the cursor sync and
// `{`/`}` all have to agree with what was actually rendered. Deriving them
// from one layout pass keeps that impossible to get out of sync.
type hunkLayout struct {
	rows   []string
	starts []int
}

// hunkAtRow is the index of the hunk owning a given content row.
func (l hunkLayout) hunkAtRow(row int) int {
	found := 0
	for i, start := range l.starts {
		if start > row {
			break
		}
		found = i
	}
	return found
}

// layoutHunks renders a file's hunks into content rows. styleHeader picks the
// style for each hunk header, letting the caller mark the selected one.
func layoutHunks(f diff.FileDiff, width int, wrap bool, hScroll int, styleHeader func(int) lipgloss.Style) hunkLayout {
	// Row *counts* must stay correct even before the first size message, or
	// scrolling is dead until a resize. At width 1 every line still occupies
	// one row (nothing wraps into a single column), so the geometry is right
	// and only the text is unreadable — which is moot, since nothing is being
	// displayed at that size anyway.
	width = max(1, width)
	layout := hunkLayout{starts: make([]int, 0, len(f.Hunks))}
	for i, h := range f.Hunks {
		layout.starts = append(layout.starts, len(layout.rows))
		header := fmt.Sprintf(" @@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		layout.rows = append(layout.rows, styleHeader(i).Width(width).Render(header))
		layout.rows = append(layout.rows, renderHunkLines(h, width, wrap, hScroll)...)
	}
	return layout
}

// hunkLayout lays out the file under the cursor at the current pane width.
func (m Model) hunkLayout() (hunkLayout, bool) {
	f, ok := m.currentFile()
	if !ok || len(f.Hunks) == 0 {
		return hunkLayout{}, false
	}
	return layoutHunks(f, m.hunkWidth, m.wrap, m.hunkHScroll, m.selectedHunkStyler()), true
}

// selectedHunkStyler returns the per-hunk header styler, highlighting the
// cursor's hunk only while the pane has focus.
func (m Model) selectedHunkStyler() func(int) lipgloss.Style {
	return func(i int) lipgloss.Style {
		if i == m.hunksCursor && m.focus == FocusHunks {
			return styleSelectedHunkHeader
		}
		return styleHunkHeader
	}
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

func renderHunkLines(h diff.Hunk, width int, wrap bool, hScroll int) []string {
	if width <= 0 {
		return nil
	}
	oldWidth, newWidth := hunkLineNumberWidths(h)
	lines := make([]string, 0, len(h.Lines))
	oldLine, newLine := h.OldStart, h.NewStart
	for _, l := range h.Lines {
		switch l.Type {
		case '+':
			lines = append(lines, renderDecoratedLine('+', 0, newLine, oldWidth, newWidth, styleAdded.Render(l.Content), width, wrap, hScroll, false)...)
			newLine++
		case '-':
			lines = append(lines, renderDecoratedLine('-', oldLine, 0, oldWidth, newWidth, styleDeleted.Render(l.Content), width, wrap, hScroll, false)...)
			oldLine++
		default:
			lines = append(lines, renderDecoratedLine(' ', oldLine, newLine, oldWidth, newWidth, styleContext.Render(l.Content), width, wrap, hScroll, false)...)
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

// renderDecoratedLine renders one diff line as one or more pane rows: a
// single truncated row normally, or — when wrap is on — the line soft-wrapped
// across rows, with continuations indented under the code so the gutter
// column stays clean.
func renderDecoratedLine(kind byte, oldLine, newLine int, oldWidth, newWidth int, content string, width int, wrap bool, hScroll int, selected bool) []string {
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
	switch kind {
	case '+':
		gutterStyle = styleAdded
	case '-':
		gutterStyle = styleDeleted
	}
	numbers := fmt.Sprintf("%*s %*s ", oldWidth, oldText, newWidth, newText)
	prefix := lineStyle.Render(numbers) + gutterStyle.Render(gutter+" ")
	if !wrap {
		// Pan the code only — line numbers and the +/- marker stay pinned,
		// so a panned line is still identifiable. TruncateLeft is
		// ANSI-aware, so dropping columns doesn't strip the styling of
		// what's left.
		if hScroll > 0 {
			content = ansi.TruncateLeft(content, hScroll, "")
		}
		return []string{truncateStyled(prefix+content, width)}
	}

	prefixWidth := len(numbers) + 2 // gutter glyph + space
	avail := width - prefixWidth
	if avail < 1 {
		return []string{truncateStyled(prefix+content, width)}
	}
	wrapped := strings.Split(lipgloss.NewStyle().Width(avail).Render(content), "\n")
	rows := make([]string, 0, len(wrapped))
	indent := strings.Repeat(" ", prefixWidth)
	for i, part := range wrapped {
		if i == 0 {
			rows = append(rows, prefix+part)
			continue
		}
		rows = append(rows, indent+part)
	}
	return rows
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

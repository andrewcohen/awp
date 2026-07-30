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

// The app-wide selection marker: a left bar on the selected row, and an
// equally wide blank on every other row so labels line up.
const (
	selectionPrefixBar   = "┃ "
	selectionPrefixBlank = "  "
)

type OpenFunc func(filePath string, line int) tea.Cmd

type Model struct {
	RepoRoot        string
	RefreshInterval time.Duration
	LoadDiff        func() (string, error)
	OpenFile        OpenFunc

	files       []diff.FileDiff
	filtered    []diff.FileDiff
	filesCursor int
	// stream is the row geometry of the whole diff (see stream.go). Rebuilt
	// only when the file set, width or wrap changes.
	stream streamIndex
	// streamScroll is the index of the top visible stream row.
	streamScroll int
	focus        Focus
	filterInput  textinput.Model
	width        int
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
	m.rebuildStream()
}

// rebuildStream re-indexes the diff for the current width and wrap setting,
// then re-clamps the scroll against the new geometry. Every mutation of the
// file set, width or wrap must go through here — it is the only place the
// index is built, so it cannot silently go stale.
func (m *Model) rebuildStream() {
	m.stream = buildStream(m.filtered, m.hunkWidth, m.wrap)
	m.clampStreamScroll()
	m.syncFileCursorToScroll()
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
		m.resetStreamView()
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
		// Wrap changes how many rows each line occupies, so the geometry has
		// to be rebuilt and the scroll re-clamped against it. Wrapped lines
		// have no horizontal overflow left to pan over, so the column offset
		// is dropped rather than kept hidden.
		m.wrap = !m.wrap
		if m.wrap {
			m.hunkHScroll = 0
		}
		m.rebuildStream()
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
		// The file list is an index into the stream: moving it seeks.
		case "j", "down":
			m.seekToFile(m.filesCursor + 1)
		case "k", "up":
			m.seekToFile(m.filesCursor - 1)
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
		// One continuous scroll over the whole diff — there is no file
		// boundary to stop at.
		case "j", "down":
			m.scrollStream(1)
		case "k", "up":
			m.scrollStream(-1)
		case "l", "right":
			m.scrollHunksHorizontally(hScrollStep)
		case "h", "left":
			m.scrollHunksHorizontally(-hScrollStep)
		case "0", "home":
			m.panToLineStart()
		case "$", "end":
			m.panToLineEnd()
		case "}", "]":
			m.jumpHunk(1)
		case "{", "[":
			m.jumpHunk(-1)
		case "g":
			m.streamScroll = 0
			m.syncFileCursorToScroll()
		case "G":
			m.streamScroll = m.maxStreamScroll()
			m.syncFileCursorToScroll()
		case "e":
			return m, m.openAtCursor()
		}
	}
	return m, nil
}

func (m *Model) applyFilter() {
	needle := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	m.filtered = m.filtered[:0]
	if needle == "" {
		m.filtered = append(m.filtered, m.files...)
	} else {
		for _, f := range m.files {
			if strings.Contains(strings.ToLower(diff.DisplayPath(f)), needle) {
				m.filtered = append(m.filtered, f)
			}
		}
	}
	if m.filesCursor >= len(m.filtered) {
		m.filesCursor = max(0, len(m.filtered)-1)
		m.resetStreamView()
	}
	// The file set just changed, so the geometry has to be rebuilt — and this
	// must happen on every path out of applyFilter, including the unfiltered
	// one.
	m.rebuildStream()
}

func (m *Model) pageDown() {
	step := m.pageStep()
	if m.focus == FocusHunks {
		m.scrollStream(step)
		return
	}
	if len(m.filtered) == 0 {
		return
	}
	m.seekToFile(min(len(m.filtered)-1, m.filesCursor+step))
}

func (m *Model) pageUp() {
	step := m.pageStep()
	if m.focus == FocusHunks {
		m.scrollStream(-step)
		return
	}
	m.seekToFile(max(0, m.filesCursor-step))
}

// resetStreamView returns the stream to its start. Used when the diff
// reloads or the filter changes the file set out from under the scroll.
func (m *Model) resetStreamView() {
	m.streamScroll = 0
	m.hunkHScroll = 0
}

// scrollHunksHorizontally pans the stream's line content by delta columns.
// No-op under wrap, where nothing overflows the pane.
func (m *Model) scrollHunksHorizontally(delta int) {
	if m.wrap {
		return
	}
	m.hunkHScroll = max(0, m.hunkHScroll+delta)
	m.clampHunkHScroll()
}

// panToLineStart returns the pane to the left edge (vim's `0`).
func (m *Model) panToLineStart() {
	if m.wrap {
		return
	}
	m.hunkHScroll = 0
}

// panToLineEnd pans as far right as the clamp allows (vim's `$`).
func (m *Model) panToLineEnd() {
	if m.wrap {
		return
	}
	m.hunkHScroll = m.maxHunkHScroll()
}

// clampHunkHScroll stops the pan before the longest line has scrolled
// entirely out of view, so the pane can't be panned into empty space.
func (m *Model) clampHunkHScroll() {
	m.hunkHScroll = min(m.hunkHScroll, m.maxHunkHScroll())
}

// maxHunkHScroll is the furthest the pane may pan: enough to reach the end of
// the longest line in the current file while keeping minVisibleColumns of it
// on screen.
func (m Model) maxHunkHScroll() int {
	f, ok := m.currentFile()
	if !ok {
		return 0
	}
	return max(0, maxContentWidth(f)-minVisibleColumns)
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

// scrollStream moves the viewport by delta rows and re-derives which file the
// cursor is in.
func (m *Model) scrollStream(delta int) {
	m.streamScroll += delta
	m.clampStreamScroll()
	m.syncFileCursorToScroll()
}

func (m *Model) clampStreamScroll() {
	m.streamScroll = min(m.maxStreamScroll(), max(0, m.streamScroll))
}

// maxStreamScroll is the furthest the stream can scroll: far enough to bring
// any row — including the last — to the top.
//
// Stopping earlier (when the final row reaches the *bottom*) would avoid the
// blank space below the end, but it also makes a late file's header
// unreachable: seeking to the last file would clamp short of it, leaving the
// file list pointing at one file while the top row belongs to another. Vim
// scrolls this way for the same reason.
func (m Model) maxStreamScroll() int {
	return max(0, len(m.stream.rows)-1)
}

// syncFileCursorToScroll points the file list at whichever file owns the top
// visible row. The two directions are deliberately one-way each — seeking
// sets the scroll, scrolling sets the cursor — so they can't feed back into
// each other.
func (m *Model) syncFileCursorToScroll() {
	if len(m.stream.rows) == 0 {
		m.filesCursor = 0
		return
	}
	m.filesCursor = m.stream.fileAt(m.streamScroll)
}

// seekToFile scrolls the stream to a file's header row.
func (m *Model) seekToFile(i int) {
	if i < 0 || i >= len(m.stream.fileStart) {
		return
	}
	m.filesCursor = i
	m.streamScroll = m.stream.fileStart[i]
	m.hunkHScroll = 0
	m.clampStreamScroll()
}

// jumpHunk scrolls to the next or previous hunk header anywhere in the diff —
// with one continuous stream, hunk hops are no longer confined to a file.
func (m *Model) jumpHunk(delta int) {
	var target int
	if delta > 0 {
		target = m.stream.nextHunkStart(m.streamScroll)
	} else {
		target = m.stream.prevHunkStart(m.streamScroll)
	}
	if target < 0 {
		return
	}
	m.streamScroll = target
	m.clampStreamScroll()
	m.syncFileCursorToScroll()
}

// currentFile is the file the cursor is on, if any.
func (m Model) currentFile() (diff.FileDiff, bool) {
	if len(m.filtered) == 0 || m.filesCursor >= len(m.filtered) {
		return diff.FileDiff{}, false
	}
	return m.filtered[m.filesCursor], true
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

// openAtCursor opens the file for the row at the top of the viewport, at the
// line that row shows. With one continuous stream, "the file you are looking
// at" is a property of the scroll position rather than a separate selection.
func (m Model) openAtCursor() tea.Cmd {
	if len(m.stream.rows) == 0 || m.OpenFile == nil {
		return nil
	}
	r := m.stream.rows[min(max(m.streamScroll, 0), len(m.stream.rows)-1)]
	if r.file < 0 || r.file >= len(m.filtered) {
		return nil
	}
	f := m.filtered[r.file]
	line := diff.FirstChangedLine(f)
	switch r.kind {
	case rowLine:
		// A removed line has no new-side number; fall back to the old one so
		// the editor still lands in the right neighbourhood.
		if r.newNo > 0 {
			line = r.newNo
		} else if r.oldNo > 0 {
			line = r.oldNo
		}
	case rowHunkHeader:
		if r.hunk >= 0 && r.hunk < len(f.Hunks) {
			line = diff.HunkChangedLine(f.Hunks[r.hunk])
		}
	}
	return m.OpenFile(m.resolveFilePath(f), line)
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
		m.renderStreamPanel(rightWidth, height),
	)
}

var (
	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Accent)).Padding(0, 1)
	styleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	stylePathDir  = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	// The stream's file divider is a structural header, so it carries the
	// accent hue (see the design system in CLAUDE.md) — or the selection hue
	// when it is the file the cursor is in.
	styleFileRule         = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent))
	styleFileRuleBase     = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true)
	styleFileRuleCurrent  = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning))
	styleFileRuleCurBase  = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	stylePathBase         = lipgloss.NewStyle().Bold(true)
	styleAdded            = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success))
	styleDeleted          = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger))
	styleContext          = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleLineNo           = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleStatus           = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Padding(0, 1)
	styleStatusErr        = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Padding(0, 1)
	styleHunkHeader       = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true)
	styleFocusBorder      = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(charm.Accent))
	styleNormalBorder     = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(charm.Muted))
	styleAddedBadge       = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success)).Bold(true).Padding(0, 1)
	styleDeletedBadge     = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Bold(true).Padding(0, 1)
	styleModifiedBadge    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true).Padding(0, 1)
	styleRenameBadge      = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true).Padding(0, 1)
	styleSelectedBadge    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true).Padding(0, 1)
	styleSelectedPathDir  = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	styleSelectedPathBase = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
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
	hint := "j/k:scroll  {/}:hunk  g/G:ends  h/l/0/$:pan  tab:pane  e:open  w:wrap  r:refresh  /:filter  q:quit"
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
		// No outer selected-row wrap: renderFileRow already emits per-segment
		// ANSI attributes, so an enclosing style can't add bold to text that
		// has already declared its own.
		rows = append(rows, m.renderFileRow(m.filtered[i], contentWidth, i == m.filesCursor))
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m Model) renderFileRow(f diff.FileDiff, width int, selected bool) string {
	// The `┃ ` bar is the app-wide selection marker (see the design system in
	// CLAUDE.md). Unselected rows reserve the same two columns so labels stay
	// aligned down the list.
	prefix := selectionPrefixBlank
	if selected {
		prefix = styleSelected.Render(selectionPrefixBar)
	}
	badge := statusBadge(f.Status, selected)
	avail := width - lipgloss.Width(selectionPrefixBlank) - lipgloss.Width(badge) - 1
	path := renderPath(diff.DisplayPath(f), avail, selected)
	return prefix + badge + " " + path
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
	dirStyle, baseStyle := stylePathDir, stylePathBase
	if selected {
		dirStyle, baseStyle = styleSelectedPathDir, styleSelectedPathBase
	}
	return renderPathWith(path, width, dirStyle, baseStyle)
}

// renderPathWith renders a path with caller-chosen styles for its directory
// and basename, so surfaces needing a different hue (the stream's file
// divider) don't re-implement rename-arrow splitting and truncation.
func renderPathWith(path string, width int, dirStyle, baseStyle lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if strings.Contains(path, " → ") {
		parts := strings.SplitN(path, " → ", 2)
		left := renderSinglePathWith(parts[0], max(1, (width-3)/2), dirStyle, baseStyle)
		right := renderSinglePathWith(parts[1], max(1, width-lipgloss.Width(left)-3), dirStyle, baseStyle)
		return truncateStyled(left+styleMuted.Render(" → ")+right, width)
	}
	return renderSinglePathWith(path, width, dirStyle, baseStyle)
}

func renderSinglePathWith(path string, width int, dirStyle, baseStyle lipgloss.Style) string {
	path = truncate(path, width)
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if dir == "." || dir == string(filepath.Separator) {
		dir = ""
	}
	if dir == "" {
		return baseStyle.Render(base)
	}
	return dirStyle.Render(dir+"/") + baseStyle.Render(base)
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

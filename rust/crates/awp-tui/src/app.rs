//! The deck application: state, executor, keymap, and render.
//!
//! One `tea::Program`-style loop (there is no second full-screen program): the
//! reducer in `awp-core` owns all state mutation; this module performs the
//! resulting [`Effect`]s (attach a session, persist a row) and projects the
//! state onto a ratatui frame. Redraws are coalesced to ~60fps and only the
//! active pane is streamed — the "max speed switching" requirement.

use crate::pane::PaneWidget;
use crate::theme::{self, Palette};
use anyhow::Result;
use awp_core::{reduce, AppState, Effect, Event, Focus, Status, WorkspaceId};
use awp_session::{session_name, SessionBackend, SessionId, SessionSpec, WindowId};
use awp_store::Store;
use awp_vt::VtEngine;
use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Paragraph};
use ratatui::Frame;
use std::collections::BTreeMap;

const SIDEBAR_WIDTH: u16 = 40;

/// The live, attached pane: its input/output channel plus the VT engine mirror.
struct ActivePane {
    id: WorkspaceId,
    attached: awp_session::Attached,
    engine: Box<dyn VtEngine>,
    cols: u16,
    rows: u16,
}

/// Construct the VT engine. There is one engine — libghostty — so this is not a
/// choice; it drives the native library when linked and the embedded core
/// otherwise, behind an identical surface.
fn new_engine(rows: u16, cols: u16) -> Box<dyn VtEngine> {
    Box::new(awp_vt::LibghosttyEngine::new(rows, cols))
}

/// A transient filter-input buffer (the `/` mode).
struct FilterInput {
    buffer: String,
}

/// The deck.
pub struct App {
    state: AppState,
    store: Store,
    backend: Box<dyn SessionBackend>,
    pane: Option<ActivePane>,
    filter: Option<FilterInput>,
    /// Pending-`g` flag for the `gg` jump-to-top chord.
    pending_g: bool,
    /// Pending-`m` flag for the `m<reg>` pin chord.
    pending_m: bool,
    /// Last panel body area, so a freshly-opened pane is sized correctly.
    panel_body: Rect,
    last_data_version: i64,
}

impl App {
    /// Build a deck over an already-open store and session backend.
    pub fn new(store: Store, backend: Box<dyn SessionBackend>) -> Result<Self> {
        let mut state = AppState::default();
        let projects = store.load_roster()?;
        reduce(&mut state, Event::RosterLoaded(projects));
        for pg in store.load_pin_groups()? {
            state.pin_aliases.insert(pg.name, pg.label);
        }
        let dv = store.data_version().unwrap_or(0);
        Ok(Self {
            state,
            store,
            backend,
            pane: None,
            filter: None,
            pending_g: false,
            pending_m: false,
            panel_body: Rect::new(SIDEBAR_WIDTH + 1, 2, 40, 20),
            last_data_version: dv,
        })
    }

    pub fn should_quit(&self) -> bool {
        self.state.should_quit
    }

    /// Whether the open pane's workspace is actively working (drives the header
    /// spinner).
    fn pane_is_working(&self) -> bool {
        self.pane
            .as_ref()
            .and_then(|p| self.state.workspace(&p.id))
            .is_some_and(|w| w.status == Status::Working)
    }

    // --- event application -------------------------------------------------

    /// Apply a core event and execute the resulting effects.
    pub fn dispatch(&mut self, event: Event) {
        let effects = reduce(&mut self.state, event);
        for effect in effects {
            if let Err(err) = self.execute(effect) {
                tracing::warn!(%err, "effect failed");
                self.state.status_flash = Some(format!("error: {err}"));
            }
        }
    }

    fn execute(&mut self, effect: Effect) -> Result<()> {
        match effect {
            Effect::OpenWorkspace(id) => self.open_workspace(&id),
            Effect::PersistStatus {
                id,
                status,
                prompt,
                unread,
            } => {
                self.store
                    .update_status(&id, status, prompt.as_deref(), unread)?;
                Ok(())
            }
            Effect::PersistPin { id, group } => {
                let g = (!group.is_empty()).then_some(group);
                self.store.set_pin(&id, g.as_deref())?;
                Ok(())
            }
            Effect::FetchPr { .. } => Ok(()), // background enrichment: follow-up
            Effect::ReloadRoster => {
                let projects = self.store.load_roster()?;
                self.dispatch_no_exec(Event::RosterLoaded(projects));
                Ok(())
            }
        }
    }

    /// Apply an event without executing effects (used from inside `execute` to
    /// avoid recursion; roster reload emits none).
    fn dispatch_no_exec(&mut self, event: Event) {
        let _ = reduce(&mut self.state, event);
    }

    fn open_workspace(&mut self, id: &WorkspaceId) -> Result<()> {
        let Some(ws) = self.state.workspace(id).cloned() else {
            return Ok(());
        };
        let repo = basename(&ws.repo_root);
        let name = session_name(&repo, &ws.name);
        let cols = self.panel_body.width.max(1);
        let rows = self.panel_body.height.max(1);
        let mut spec = SessionSpec::new(name.clone(), ws.path.clone());
        spec.cols = cols;
        spec.rows = rows;
        spec.env.insert("AWP_WORKSPACE".into(), ws.name.clone());
        spec.env.insert("AWP_REPO".into(), repo);
        spec.env
            .insert("AWP_REPO_ROOT".into(), ws.repo_root.clone());

        let info = self
            .backend
            .ensure(id, &spec)
            .map_err(|e| anyhow::anyhow!("ensure session: {e}"))?;
        let win = info
            .windows
            .first()
            .map(|w| w.id.clone())
            .unwrap_or(WindowId("shell".into()));
        let attached = self
            .backend
            .attach(&SessionId(name), &win)
            .map_err(|e| anyhow::anyhow!("attach session: {e}"))?;
        let engine = new_engine(rows, cols);
        self.pane = Some(ActivePane {
            id: id.clone(),
            attached,
            engine,
            cols,
            rows,
        });
        Ok(())
    }

    // --- key handling ------------------------------------------------------

    /// Translate a key press into deck/panel behavior. Returns nothing; state
    /// changes flow through `dispatch`.
    pub fn on_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::{KeyCode, KeyModifiers};

        // Ctrl-a toggles focus everywhere.
        if key.code == KeyCode::Char('a') && key.modifiers.contains(KeyModifiers::CONTROL) {
            self.dispatch(Event::ToggleFocus);
            return;
        }

        // Filter-input mode captures typed text.
        if self.filter.is_some() {
            self.on_filter_key(key);
            return;
        }

        match self.state.focus {
            Focus::Panel => self.forward_to_pane(key),
            Focus::Deck => self.on_deck_key(key),
        }
    }

    fn on_deck_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::{KeyCode, KeyModifiers};
        // Second key of the `m` pin chord takes priority.
        if self.pending_m {
            self.on_pin_key(key);
            return;
        }
        let g_was_pending = self.pending_g;
        self.pending_g = false;
        match key.code {
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                self.dispatch(Event::Quit)
            }
            KeyCode::Char('q') => self.dispatch(Event::Quit),
            KeyCode::Char('j') | KeyCode::Down => self.dispatch(Event::SelectNext),
            KeyCode::Char('k') | KeyCode::Up => self.dispatch(Event::SelectPrev),
            KeyCode::Char('G') => self.dispatch(Event::SelectLast),
            KeyCode::Char('g') => {
                if g_was_pending {
                    self.dispatch(Event::SelectFirst);
                } else {
                    self.pending_g = true;
                }
            }
            KeyCode::Enter => self.dispatch(Event::OpenSelected),
            KeyCode::Char('P') => self.dispatch(Event::CycleScope),
            KeyCode::Char('/') => {
                self.filter = Some(FilterInput {
                    buffer: self.state.filter.clone().unwrap_or_default(),
                });
            }
            // Window commands: open (or focus) a named tmux window running a
            // tool, ported from the Go deck's e/s/c/C/v/i/a keys.
            KeyCode::Char('a') => self.dispatch(Event::OpenSelected), // agent (base window)
            KeyCode::Char('s') => self.run_window("shell", &[]),
            KeyCode::Char('e') => {
                let editor = std::env::var("EDITOR").unwrap_or_else(|_| "vi".into());
                self.run_window("editor", &[editor.as_str()]);
            }
            KeyCode::Char('c') => self.run_window("review", &["tuicr", "-r", "@"]),
            KeyCode::Char('C') => self.run_window("review", &["tuicr", "-r", "main..@"]),
            KeyCode::Char('v') => self.run_window("vcs", &["jjui"]),
            KeyCode::Char('i') => self.run_window("ci", &["gh", "run", "watch"]),
            // Pin chord: `m` then a register key (m/default, a–z, D unpin).
            KeyCode::Char('m') => self.pending_m = true,
            KeyCode::Esc => self.dispatch(Event::ClearFilter),
            _ => {}
        }
    }

    /// Handle the second key of the `m` pin chord.
    fn on_pin_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::KeyCode;
        self.pending_m = false;
        let Some(id) = self.state.selected_id() else {
            return;
        };
        let group = match key.code {
            KeyCode::Char('m') => "default".to_string(),
            KeyCode::Char('D') => String::new(), // unpin
            KeyCode::Char(c @ 'a'..='z') => c.to_string(),
            _ => return,
        };
        self.dispatch(Event::SetPin { id, group });
    }

    /// Open (or focus) a named tmux window running `command` in the selected
    /// (or currently-open) workspace's session, and move focus to the pane.
    fn run_window(&mut self, name: &str, command: &[&str]) {
        let Some(id) = self
            .pane
            .as_ref()
            .map(|p| p.id.clone())
            .or_else(|| self.state.selected_id())
        else {
            return;
        };
        // Make sure the pane is attached to this workspace's session so window
        // switches show up in place.
        if self.pane.as_ref().map(|p| &p.id) != Some(&id) {
            if let Err(err) = self.open_workspace(&id) {
                self.state.status_flash = Some(format!("error: {err}"));
                return;
            }
        }
        let repo = basename(&id.repo_root);
        let session = SessionId(session_name(&repo, &id.name));
        let cmd: Vec<String> = command.iter().map(|s| s.to_string()).collect();
        if let Err(err) = self.backend.open_window(&session, name, &cmd) {
            self.state.status_flash = Some(format!("error: {err}"));
            return;
        }
        self.state.focus = Focus::Panel;
    }

    fn on_filter_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::KeyCode;
        let Some(filter) = self.filter.as_mut() else {
            return;
        };
        match key.code {
            KeyCode::Esc => {
                self.filter = None;
                self.dispatch(Event::ClearFilter);
            }
            KeyCode::Enter => {
                let text = filter.buffer.clone();
                self.filter = None;
                if text.is_empty() {
                    self.dispatch(Event::ClearFilter);
                } else {
                    self.dispatch(Event::SetFilter(text));
                }
            }
            KeyCode::Backspace => {
                filter.buffer.pop();
                let live = filter.buffer.clone();
                self.dispatch(Event::SetFilter(live));
            }
            KeyCode::Char(c) => {
                filter.buffer.push(c);
                let live = filter.buffer.clone();
                self.dispatch(Event::SetFilter(live));
            }
            _ => {}
        }
    }

    /// Forward a raw keystroke to the live pane's PTY.
    fn forward_to_pane(&mut self, key: crossterm::event::KeyEvent) {
        let Some(pane) = self.pane.as_mut() else {
            return;
        };
        if let Some(bytes) = encode_key(key) {
            if let Err(e) = pane.attached.write_input(&bytes) {
                tracing::warn!(%e, "pane write failed");
            }
        }
    }

    // --- background pumps --------------------------------------------------

    /// Drain PTY output into the active engine. Cheap; called every loop.
    pub fn pump_pane(&mut self) {
        let Some(pane) = self.pane.as_mut() else {
            return;
        };
        while let Ok(chunk) = pane.attached.output().try_recv() {
            pane.engine.process(&chunk);
        }
    }

    /// Cross-process change detection: on a `data_version` bump, reload dirty
    /// rows so another session's `report-status` write shows up. No file
    /// watching, no lost updates.
    pub fn poll_store(&mut self) {
        let Ok(dv) = self.store.data_version() else {
            return;
        };
        if dv == self.last_data_version {
            return;
        }
        self.last_data_version = dv;
        if let Ok(projects) = self.store.load_roster() {
            self.dispatch(Event::RosterLoaded(projects));
        }
    }

    // --- render ------------------------------------------------------------

    /// Compute the panel body rect for a given full area (so a pane can be
    /// sized before the first draw).
    fn compute_panel_body(area: Rect) -> Rect {
        let cols = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Length(SIDEBAR_WIDTH), Constraint::Min(20)])
            .split(area);
        let panel = cols[1];
        // Title row (1) + tab strip (1) + footer (1) carved off the panel.
        let inner = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1),
                Constraint::Min(1),
                Constraint::Length(1),
            ])
            .split(panel);
        inner[1]
    }

    /// Update the cached panel size and resize a live pane to match.
    pub fn sync_size(&mut self, area: Rect) {
        let body = Self::compute_panel_body(area);
        self.panel_body = body;
        if let Some(pane) = self.pane.as_mut() {
            let (cols, rows) = (body.width.max(1), body.height.max(1));
            if cols != pane.cols || rows != pane.rows {
                pane.cols = cols;
                pane.rows = rows;
                pane.engine.resize(cols, rows);
                let _ = pane.attached.resize(cols, rows);
            }
        }
    }

    /// Draw one frame.
    pub fn draw(&self, frame: &mut Frame) {
        let area = frame.area();
        let cols = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Length(SIDEBAR_WIDTH), Constraint::Min(20)])
            .split(area);
        self.draw_sidebar(frame, cols[0]);
        self.draw_panel(frame, cols[1]);
    }

    fn draw_sidebar(&self, frame: &mut Frame, area: Rect) {
        let rows = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1),
                Constraint::Min(1),
                Constraint::Length(1),
            ])
            .split(area);

        // Title + scope label.
        let title = Line::from(vec![
            Span::styled("awp deck", Style::default().add_modifier(Modifier::BOLD)),
            Span::raw("  "),
            Span::styled(
                format!("scope: {}", self.state.scope.label()),
                theme::muted_style(),
            ),
        ]);
        frame.render_widget(Paragraph::new(title), rows[0]);

        // Body: project headers + workspace rows.
        let lines = self.sidebar_lines();
        let body = Paragraph::new(lines).block(Block::default().borders(Borders::NONE));
        frame.render_widget(body, rows[1]);

        // Footer: filter input or hints.
        frame.render_widget(Paragraph::new(self.footer_line()), rows[2]);
    }

    fn sidebar_lines(&self) -> Vec<Line<'static>> {
        let visible = self.state.visible();
        let selected = self.state.selected_id();
        // Group visible ids back under their project header for display.
        let mut by_project: BTreeMap<String, Vec<WorkspaceId>> = BTreeMap::new();
        let mut order: Vec<String> = Vec::new();
        for id in &visible {
            let header = self
                .state
                .workspace(id)
                .map(|w| basename(&w.repo_root))
                .unwrap_or_default();
            if !by_project.contains_key(&header) {
                order.push(header.clone());
            }
            by_project.entry(header).or_default().push(id.clone());
        }

        let mut lines: Vec<Line<'static>> = Vec::new();
        for header in order {
            lines.push(Line::styled(header.clone(), theme::project_header_style()));
            for id in by_project.get(&header).into_iter().flatten() {
                lines.push(self.row_line(id, selected.as_ref() == Some(id)));
            }
        }
        if lines.is_empty() {
            lines.push(Line::styled("no workspaces", theme::muted_style()));
        }
        lines
    }

    fn row_line(&self, id: &WorkspaceId, is_selected: bool) -> Line<'static> {
        let ws = self.state.workspace(id);
        let (status, unread, label, pr) = match ws {
            Some(w) => (w.status, w.unread, w.name.clone(), w.pr_number),
            None => (Status::Idle, false, id.name.clone(), None),
        };
        let (glyph, glyph_style) = theme::status_glyph(status, unread);
        let mut spans: Vec<Span<'static>> = Vec::new();
        if is_selected {
            spans.push(Span::styled(
                theme::SELECTION_PREFIX,
                theme::selection_style(),
            ));
        } else {
            spans.push(Span::raw("  "));
        }
        spans.push(Span::styled(glyph.to_string(), glyph_style));
        spans.push(Span::raw(" "));
        let label_style = if is_selected {
            theme::selection_style()
        } else {
            Style::default()
        };
        spans.push(Span::styled(label, label_style));
        // PR number in Info (blue), matching the Go deck's PR-number color.
        if let Some(number) = pr {
            spans.push(Span::raw(" "));
            spans.push(Span::styled(format!("#{number}"), theme::pr_style()));
        }
        Line::from(spans)
    }

    fn footer_line(&self) -> Line<'static> {
        if let Some(filter) = &self.filter {
            return Line::from(vec![
                Span::styled("/", theme::selection_style()),
                Span::raw(filter.buffer.clone()),
            ]);
        }
        if let Some(flash) = &self.state.status_flash {
            return Line::styled(flash.clone(), theme::muted_style());
        }
        Line::styled(
            "j/k move · enter open · e edit · s shell · c review · v vcs · i ci · m pin · / filter · P scope · ^a focus · q quit",
            theme::muted_style(),
        )
    }

    fn draw_panel(&self, frame: &mut Frame, area: Rect) {
        let rows = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(1),
                Constraint::Min(1),
                Constraint::Length(1),
            ])
            .split(area);

        // Tab strip: focus chip + a spinner for a working agent + the title.
        let title = self
            .pane
            .as_ref()
            .map(|p| p.id.name.clone())
            .unwrap_or_else(|| "(no workspace open)".to_string());
        let focus_hint = if self.state.focus == Focus::Panel {
            Span::styled(
                " [pane] ",
                Style::default()
                    .fg(Palette::ACCENT)
                    .add_modifier(Modifier::BOLD),
            )
        } else {
            Span::styled(" [deck] ", theme::muted_style())
        };
        let mut header = vec![focus_hint];
        if self.pane_is_working() {
            header.push(Span::styled("● ", theme::spinner_style()));
        }
        header.push(Span::styled(title, theme::strong_style()));
        frame.render_widget(Paragraph::new(Line::from(header)), rows[0]);

        // Live pane body.
        if let Some(pane) = &self.pane {
            let screen = pane.engine.screen();
            frame.render_widget(PaneWidget::new(&screen), rows[1]);
            if self.state.focus == Focus::Panel && screen.cursor.visible {
                let cx = rows[1].x + screen.cursor.col.min(rows[1].width.saturating_sub(1));
                let cy = rows[1].y + screen.cursor.row.min(rows[1].height.saturating_sub(1));
                frame.set_cursor_position((cx, cy));
            }
        } else {
            frame.render_widget(
                Paragraph::new(Line::styled(
                    "select a workspace and press enter",
                    theme::muted_style(),
                )),
                rows[1],
            );
        }

        // Panel footer.
        frame.render_widget(
            Paragraph::new(Line::styled(
                "^a → deck · shift+←/→ tabs",
                theme::muted_style(),
            )),
            rows[2],
        );
    }

    /// Test/inspection helper: the current visible workspace ids.
    #[cfg(test)]
    pub fn visible(&self) -> Vec<WorkspaceId> {
        self.state.visible()
    }
}

/// A convenience for tests + the executor: the repo-root basename.
fn basename(path: &str) -> String {
    path.trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or(path)
        .to_string()
}

/// Encode a key press into the bytes a PTY expects. Covers the common control
/// keys plus printable characters; unhandled keys produce nothing.
fn encode_key(key: crossterm::event::KeyEvent) -> Option<Vec<u8>> {
    use crossterm::event::{KeyCode, KeyModifiers};
    let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);
    match key.code {
        KeyCode::Char(c) if ctrl => {
            // Control byte: Ctrl-A => 0x01, etc.
            let upper = c.to_ascii_uppercase();
            if upper.is_ascii_alphabetic() {
                Some(vec![(upper as u8) - b'A' + 1])
            } else {
                Some(vec![c as u8])
            }
        }
        KeyCode::Char(c) => {
            let mut b = [0u8; 4];
            Some(c.encode_utf8(&mut b).as_bytes().to_vec())
        }
        KeyCode::Enter => Some(vec![b'\r']),
        KeyCode::Tab => Some(vec![b'\t']),
        KeyCode::Backspace => Some(vec![0x7f]),
        KeyCode::Esc => Some(vec![0x1b]),
        KeyCode::Left => Some(b"\x1b[D".to_vec()),
        KeyCode::Right => Some(b"\x1b[C".to_vec()),
        KeyCode::Up => Some(b"\x1b[A".to_vec()),
        KeyCode::Down => Some(b"\x1b[B".to_vec()),
        KeyCode::Home => Some(b"\x1b[H".to_vec()),
        KeyCode::End => Some(b"\x1b[F".to_vec()),
        KeyCode::PageUp => Some(b"\x1b[5~".to_vec()),
        KeyCode::PageDown => Some(b"\x1b[6~".to_vec()),
        KeyCode::Delete => Some(b"\x1b[3~".to_vec()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use awp_core::Workspace;
    use awp_session::LocalBackend;
    use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
    use ratatui::backend::TestBackend;
    use ratatui::Terminal;

    fn seeded_app() -> App {
        let store = Store::open_in_memory().unwrap();
        for (repo, name, status) in [
            ("/r/alpha", "main", Status::Idle),
            ("/r/alpha", "feature", Status::Working),
            ("/r/beta", "wip", Status::Waiting),
        ] {
            store
                .upsert_workspace(&Workspace {
                    repo_root: repo.into(),
                    name: name.into(),
                    path: format!("{repo}/{name}"),
                    status,
                    ..Default::default()
                })
                .unwrap();
        }
        App::new(store, Box::new(LocalBackend::new())).unwrap()
    }

    fn key(c: char) -> KeyEvent {
        KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE)
    }

    #[test]
    fn renders_sidebar_with_headers_and_rows() {
        let app = seeded_app();
        let backend = TestBackend::new(80, 24);
        let mut term = Terminal::new(backend).unwrap();
        term.draw(|f| app.draw(f)).unwrap();
        let buf = term.backend().buffer().clone();
        let text: String = buf
            .content()
            .iter()
            .map(|c| c.symbol())
            .collect::<Vec<_>>()
            .join("");
        assert!(text.contains("awp deck"));
        assert!(text.contains("alpha"));
        assert!(text.contains("feature"));
        assert!(text.contains("beta"));
    }

    #[test]
    fn navigation_keys_move_selection() {
        let mut app = seeded_app();
        assert_eq!(app.state.selected, 0);
        app.on_key(key('j'));
        assert_eq!(app.state.selected, 1);
        app.on_key(key('k'));
        assert_eq!(app.state.selected, 0);
        app.on_key(key('G'));
        assert_eq!(app.state.selected, app.visible().len() - 1);
    }

    #[test]
    fn gg_chord_jumps_to_top() {
        let mut app = seeded_app();
        app.on_key(key('G'));
        assert!(app.state.selected > 0);
        app.on_key(key('g'));
        app.on_key(key('g'));
        assert_eq!(app.state.selected, 0);
    }

    #[test]
    fn scope_key_cycles_and_flashes() {
        let mut app = seeded_app();
        app.on_key(key('P'));
        assert_eq!(app.state.scope, awp_core::Scope::Attention);
        assert!(app.state.status_flash.is_some());
    }

    #[test]
    fn filter_mode_narrows_then_clears() {
        let mut app = seeded_app();
        app.on_key(key('/'));
        for c in "feat".chars() {
            app.on_key(key(c));
        }
        assert_eq!(app.visible().len(), 1);
        // Esc clears silently.
        app.on_key(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE));
        assert_eq!(app.visible().len(), 3);
        assert!(app.state.status_flash.is_none());
    }

    #[test]
    fn ctrl_a_toggles_focus() {
        let mut app = seeded_app();
        assert_eq!(app.state.focus, Focus::Deck);
        app.on_key(KeyEvent::new(KeyCode::Char('a'), KeyModifiers::CONTROL));
        assert_eq!(app.state.focus, Focus::Panel);
    }

    #[test]
    fn enter_opens_a_live_pane() {
        let mut app = seeded_app();
        app.sync_size(Rect::new(0, 0, 100, 30));
        app.on_key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE));
        assert!(app.pane.is_some(), "a pane should be attached");
        assert_eq!(app.state.focus, Focus::Panel);
    }

    #[test]
    fn quit_key_sets_should_quit() {
        let mut app = seeded_app();
        app.on_key(key('q'));
        assert!(app.should_quit());
    }

    #[test]
    fn m_chord_pins_selected_to_default_register() {
        let mut app = seeded_app();
        let id = app.state.selected_id().unwrap();
        app.on_key(key('m'));
        assert!(app.pending_m);
        app.on_key(key('m'));
        assert!(!app.pending_m);
        assert!(app.state.workspace(&id).unwrap().is_pinned());
        // `m D` unpins.
        app.on_key(key('m'));
        app.on_key(KeyEvent::new(KeyCode::Char('D'), KeyModifiers::NONE));
        assert!(!app.state.workspace(&id).unwrap().is_pinned());
    }

    #[test]
    fn encode_key_control_and_arrows() {
        let ctrl_c = encode_key(KeyEvent::new(KeyCode::Char('c'), KeyModifiers::CONTROL));
        assert_eq!(ctrl_c, Some(vec![3]));
        let left = encode_key(KeyEvent::new(KeyCode::Left, KeyModifiers::NONE));
        assert_eq!(left, Some(b"\x1b[D".to_vec()));
    }
}

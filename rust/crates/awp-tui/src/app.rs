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

/// Which modal the deck is in. `Normal` is the row list; every other variant
/// routes keys and rendering to a modal (form / picker / confirm / overlay),
/// following the Go deck's "one program, states inside the model" pattern.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
enum Mode {
    #[default]
    Normal,
    /// `/` filter — edits `input`, applied live.
    Filter,
    /// `m` pin chord — waiting for the register key.
    Pin,
    /// `n` new-workspace form — edits `form`.
    NewWorkspace,
    /// `R` rename — edits `input`, acts on `target`.
    Rename,
    /// `D` confirm delete of `target`.
    ConfirmDelete,
    /// `p` PR action menu for `target`.
    PrMenu,
    /// `p s` set PR number — edits `input`, acts on `target`.
    SetPr,
    /// `B` link bookmark — edits `input`, acts on `target`.
    Bookmark,
    /// `A` send prompt — edits `input`, acts on `target`.
    Prompt,
    /// `?` help overlay.
    Help,
    /// `J` jobs / operation-log overlay.
    Jobs,
    /// `f` easymotion find — accumulates a hint in `input`.
    Find,
}

/// A recorded deck operation (workspace create/delete/rename, PR merge/open),
/// surfaced in the `J` overlay. Operations run synchronously; this is the log.
#[derive(Debug, Clone)]
struct Job {
    title: String,
    /// `Ok(summary)` on success, `Err(message)` on failure.
    outcome: std::result::Result<String, String>,
}

/// The three-field new-workspace form.
#[derive(Debug, Default, Clone)]
struct NewForm {
    name: String,
    bookmark: String,
    prompt: String,
    /// Focused field: 0 name, 1 bookmark, 2 prompt.
    field: usize,
    /// Repo root the workspace is created under (the selected row's project).
    repo_root: String,
}

/// The deck.
pub struct App {
    state: AppState,
    store: Store,
    backend: Box<dyn SessionBackend>,
    pane: Option<ActivePane>,
    /// The active modal (or `Normal`).
    mode: Mode,
    /// Shared single-line input buffer for the text modes.
    input: String,
    /// New-workspace form state.
    form: NewForm,
    /// The workspace a modal acts on (rename / delete / PR / bookmark / prompt).
    target: Option<WorkspaceId>,
    /// Easymotion hint labels for the currently-visible rows (find mode).
    find_hints: Vec<String>,
    /// The previously-open workspace, for the `L` last-session jump.
    last_opened: Option<WorkspaceId>,
    /// Recorded operations, newest last, shown in the `J` overlay.
    jobs: Vec<Job>,
    /// Pending-`g` flag for the `gg` jump-to-top chord.
    pending_g: bool,
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
            mode: Mode::Normal,
            input: String::new(),
            form: NewForm::default(),
            target: None,
            find_hints: Vec::new(),
            last_opened: None,
            jobs: Vec::new(),
            pending_g: false,
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
            Effect::CreateWorkspace {
                repo_root,
                name,
                bookmark,
                prompt,
            } => {
                let r = self.create_workspace(&repo_root, &name, &bookmark, &prompt);
                self.finish_job(format!("create {name}"), r);
                Ok(())
            }
            Effect::RenameWorkspace { id, new_name } => {
                let r = self.rename_workspace(&id, &new_name);
                self.finish_job(format!("rename {} → {new_name}", id.name), r);
                Ok(())
            }
            Effect::DeleteWorkspace { id } => {
                let r = self.delete_workspace(&id);
                self.finish_job(format!("delete {}", id.name), r);
                Ok(())
            }
            Effect::PersistPr { id, number } => {
                if let Some(mut ws) = self.store.get_workspace(&id)? {
                    ws.pr_number = (number != 0).then_some(number);
                    self.store.upsert_workspace(&ws)?;
                }
                Ok(())
            }
            Effect::PersistBookmark { id, bookmark } => {
                if let Some(mut ws) = self.store.get_workspace(&id)? {
                    ws.bookmark = (!bookmark.is_empty()).then_some(bookmark);
                    self.store.upsert_workspace(&ws)?;
                }
                Ok(())
            }
            Effect::SendPrompt { id, text } => self.send_prompt(&id, &text),
            Effect::OpenPrWeb { id, number } => {
                let dir = self
                    .state
                    .workspace(&id)
                    .map(|w| w.path.clone())
                    .unwrap_or_default();
                let r =
                    awp_agent::pr::open_web(&dir, number).map(|()| format!("opened PR #{number}"));
                self.finish_job(format!("open PR #{number}"), r);
                Ok(())
            }
            Effect::MergePr { id, number } => {
                let dir = self
                    .state
                    .workspace(&id)
                    .map(|w| w.path.clone())
                    .unwrap_or_default();
                let r = awp_agent::pr::merge_squash(&dir, number)
                    .map(|_| format!("merged PR #{number}"));
                self.finish_job(format!("merge PR #{number}"), r);
                Ok(())
            }
        }
    }

    /// Record a completed operation in the job log and flash its result.
    fn finish_job(&mut self, title: String, result: Result<String>) {
        let outcome = match result {
            Ok(summary) => {
                self.state.status_flash = Some(summary.clone());
                Ok(summary)
            }
            Err(err) => {
                let msg = format!("{err:#}");
                self.state.status_flash = Some(format!("error: {msg}"));
                Err(msg)
            }
        };
        self.jobs.push(Job { title, outcome });
        // Bound the log.
        if self.jobs.len() > 100 {
            self.jobs.remove(0);
        }
    }

    /// Create a jj workspace, persist the row, start its session, and refocus.
    fn create_workspace(
        &mut self,
        repo_root: &str,
        name: &str,
        bookmark: &str,
        prompt: &str,
    ) -> Result<String> {
        let path = awp_agent::workspace::create(repo_root, name, bookmark)?;
        let norm = awp_agent::workspace::normalize_name(name);
        let ws = awp_core::Workspace {
            repo_root: repo_root.to_string(),
            name: norm.clone(),
            path: path.to_string_lossy().to_string(),
            bookmark: (!bookmark.trim().is_empty()).then(|| bookmark.trim().to_string()),
            active_prompt: (!prompt.trim().is_empty()).then(|| prompt.trim().to_string()),
            status: Status::Starting,
            ..Default::default()
        };
        self.store.upsert_workspace(&ws)?;
        let id = ws.id();
        self.dispatch_no_exec(Event::UpsertWorkspace(ws));
        self.open_workspace(&id)?;
        Ok(format!("created {norm}"))
    }

    /// Rename a workspace on disk (jj) and re-key its store row.
    fn rename_workspace(&mut self, id: &WorkspaceId, new_name: &str) -> Result<String> {
        // The reducer already re-keyed the in-RAM row; move the store row too.
        let Some(mut ws) = self.store.get_workspace(id)? else {
            return Ok(format!("renamed to {new_name}"));
        };
        let final_name = awp_agent::workspace::rename(&ws.path, new_name)?;
        self.store.delete_workspace(id)?;
        ws.name = final_name.clone();
        self.store.upsert_workspace(&ws)?;
        Ok(format!("renamed to {final_name}"))
    }

    /// Forget the jj workspace, kill its session, and drop the store row.
    fn delete_workspace(&mut self, id: &WorkspaceId) -> Result<String> {
        let repo = basename(&id.repo_root);
        let session = SessionId(session_name(&repo, &id.name));
        let _ = self.backend.kill(&session);
        if self.pane.as_ref().map(|p| &p.id) == Some(id) {
            self.pane = None;
            self.state.focus = Focus::Deck;
        }
        awp_agent::workspace::delete(&id.repo_root, &id.name)?;
        self.store.delete_workspace(id)?;
        Ok(format!("deleted {}", id.name))
    }

    /// Send a prompt to the workspace's live agent by writing it (plus Enter) to
    /// the pane's PTY. Falls back silently if the workspace isn't open.
    fn send_prompt(&mut self, id: &WorkspaceId, text: &str) -> Result<()> {
        if self.pane.as_ref().map(|p| &p.id) != Some(id) {
            self.open_workspace(id)?;
        }
        if let Some(pane) = self.pane.as_mut() {
            let mut bytes = text.as_bytes().to_vec();
            bytes.push(b'\r');
            let _ = pane.attached.write_input(&bytes);
        }
        Ok(())
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
        // Remember the outgoing workspace for the `L` last-session jump.
        if let Some(prev) = self.pane.as_ref().map(|p| p.id.clone()) {
            if prev != *id {
                self.last_opened = Some(prev);
            }
        }
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

    /// Translate a key press into deck/panel/modal behavior.
    pub fn on_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::{KeyCode, KeyModifiers};

        // A modal owns all keys while it is open.
        if self.mode != Mode::Normal {
            self.on_mode_key(key);
            return;
        }
        // Ctrl-a toggles deck ↔ panel focus (normal mode only).
        if key.code == KeyCode::Char('a') && key.modifiers.contains(KeyModifiers::CONTROL) {
            self.dispatch(Event::ToggleFocus);
            return;
        }
        match self.state.focus {
            Focus::Panel => self.forward_to_pane(key),
            Focus::Deck => self.on_deck_key(key),
        }
    }

    fn on_deck_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::{KeyCode, KeyModifiers};
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
                self.input = self.state.filter.clone().unwrap_or_default();
                self.mode = Mode::Filter;
            }
            // Window commands (Go deck e/s/c/C/v/i/a).
            KeyCode::Char('a') => self.dispatch(Event::OpenSelected),
            KeyCode::Char('s') => self.run_window("shell", &[]),
            KeyCode::Char('e') => {
                let editor = std::env::var("EDITOR").unwrap_or_else(|_| "vi".into());
                self.run_window("editor", &[editor.as_str()]);
            }
            KeyCode::Char('c') => self.run_window("review", &["tuicr", "-r", "@"]),
            KeyCode::Char('C') => self.run_window("review", &["tuicr", "-r", "main..@"]),
            KeyCode::Char('v') => self.run_window("vcs", &["jjui"]),
            KeyCode::Char('i') => self.run_window("ci", &["gh", "run", "watch"]),
            // Modal openers.
            KeyCode::Char('m') => self.mode = Mode::Pin,
            KeyCode::Char('n') => self.open_new_form(),
            KeyCode::Char('R') => self.open_target_input(Mode::Rename, |ws| ws.name.clone()),
            KeyCode::Char('D') => {
                if let Some(id) = self.state.selected_id() {
                    self.target = Some(id);
                    self.mode = Mode::ConfirmDelete;
                }
            }
            KeyCode::Char('p') => {
                if let Some(id) = self.state.selected_id() {
                    self.target = Some(id);
                    self.mode = Mode::PrMenu;
                }
            }
            KeyCode::Char('B') => {
                self.open_target_input(Mode::Bookmark, |ws| ws.bookmark.clone().unwrap_or_default())
            }
            KeyCode::Char('A') => self.open_target_input(Mode::Prompt, |_| String::new()),
            KeyCode::Char('f') => self.open_find(),
            KeyCode::Char('J') => self.mode = Mode::Jobs,
            KeyCode::Char('L') => {
                if let Some(id) = self.last_opened.clone() {
                    if let Err(err) = self.open_workspace(&id) {
                        self.state.status_flash = Some(format!("error: {err}"));
                    } else {
                        self.state.focus = Focus::Panel;
                    }
                }
            }
            KeyCode::Char('?') => self.mode = Mode::Help,
            _ => {}
        }
    }

    /// Route a key while a modal is open.
    fn on_mode_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::KeyCode;
        match self.mode {
            Mode::Filter => self.on_filter_key(key.code),
            Mode::Pin => self.on_pin_key(key.code),
            Mode::NewWorkspace => self.on_new_form_key(key),
            Mode::Rename => {
                if let Some(text) = self.on_text_key(key.code) {
                    if let Some(id) = self.target.take() {
                        self.dispatch(Event::RenameWorkspace { id, new_name: text });
                    }
                }
            }
            Mode::SetPr => {
                if let Some(text) = self.on_text_key(key.code) {
                    if let Some(id) = self.target.take() {
                        let number = text.trim().parse::<u64>().unwrap_or(0);
                        self.dispatch(Event::SetPr { id, number });
                    }
                }
            }
            Mode::Bookmark => {
                if let Some(text) = self.on_text_key(key.code) {
                    if let Some(id) = self.target.take() {
                        self.dispatch(Event::LinkBookmark { id, bookmark: text });
                    }
                }
            }
            Mode::Prompt => {
                if let Some(text) = self.on_text_key(key.code) {
                    if let Some(id) = self.target.take() {
                        self.dispatch(Event::SendPrompt { id, text });
                    }
                }
            }
            Mode::ConfirmDelete => match key.code {
                KeyCode::Char('y') | KeyCode::Char('Y') => {
                    if let Some(id) = self.target.take() {
                        self.dispatch(Event::DeleteWorkspace { id });
                    }
                    self.close_mode();
                }
                _ => self.close_mode(),
            },
            Mode::PrMenu => self.on_pr_menu_key(key.code),
            Mode::Help | Mode::Jobs => self.close_mode(),
            Mode::Find => self.on_find_key(key.code),
            Mode::Normal => {}
        }
    }

    fn on_filter_key(&mut self, code: crossterm::event::KeyCode) {
        use crossterm::event::KeyCode;
        match code {
            KeyCode::Esc => {
                self.close_mode();
                self.dispatch(Event::ClearFilter);
            }
            KeyCode::Enter => {
                let text = std::mem::take(&mut self.input);
                self.close_mode();
                if text.is_empty() {
                    self.dispatch(Event::ClearFilter);
                } else {
                    self.dispatch(Event::SetFilter(text));
                }
            }
            KeyCode::Backspace => {
                self.input.pop();
                let live = self.input.clone();
                self.dispatch(Event::SetFilter(live));
            }
            KeyCode::Char(c) => {
                self.input.push(c);
                let live = self.input.clone();
                self.dispatch(Event::SetFilter(live));
            }
            _ => {}
        }
    }

    fn on_pin_key(&mut self, code: crossterm::event::KeyCode) {
        use crossterm::event::KeyCode;
        self.close_mode();
        let Some(id) = self.state.selected_id() else {
            return;
        };
        let group = match code {
            KeyCode::Char('m') => "default".to_string(),
            KeyCode::Char('D') => String::new(),
            KeyCode::Char(c @ 'a'..='z') => c.to_string(),
            _ => return,
        };
        self.dispatch(Event::SetPin { id, group });
    }

    fn on_pr_menu_key(&mut self, code: crossterm::event::KeyCode) {
        use crossterm::event::KeyCode;
        let Some(id) = self.target.clone() else {
            self.close_mode();
            return;
        };
        match code {
            KeyCode::Char('o') => {
                self.close_mode();
                self.dispatch(Event::OpenPr { id });
            }
            KeyCode::Char('m') => {
                self.close_mode();
                self.dispatch(Event::MergePr { id });
            }
            KeyCode::Char('d') => {
                self.close_mode();
                self.run_window("pr", &["sh", "-c", "gh pr view | ${PAGER:-less}"]);
            }
            KeyCode::Char('s') => {
                // Switch to the numeric SetPr input, keeping the target.
                self.input.clear();
                self.mode = Mode::SetPr;
            }
            _ => self.close_mode(),
        }
    }

    /// Generic single-line text edit. Returns `Some(text)` on Enter (submit),
    /// `None` otherwise; Esc cancels back to Normal.
    fn on_text_key(&mut self, code: crossterm::event::KeyCode) -> Option<String> {
        use crossterm::event::KeyCode;
        match code {
            KeyCode::Esc => {
                self.close_mode();
                None
            }
            KeyCode::Enter => {
                let text = std::mem::take(&mut self.input);
                self.close_mode();
                Some(text)
            }
            KeyCode::Backspace => {
                self.input.pop();
                None
            }
            KeyCode::Char(c) => {
                self.input.push(c);
                None
            }
            _ => None,
        }
    }

    /// Open a single-line input mode seeded from the selected workspace.
    fn open_target_input(&mut self, mode: Mode, seed: impl Fn(&awp_core::Workspace) -> String) {
        let Some(id) = self.state.selected_id() else {
            return;
        };
        self.input = self.state.workspace(&id).map(seed).unwrap_or_default();
        self.target = Some(id);
        self.mode = mode;
    }

    /// Open the new-workspace form under the selected row's project.
    fn open_new_form(&mut self) {
        let repo_root = self
            .state
            .selected_id()
            .map(|id| id.repo_root)
            .or_else(|| self.state.projects.first().map(|p| p.repo_root.clone()))
            .unwrap_or_default();
        self.form = NewForm {
            repo_root,
            ..Default::default()
        };
        self.mode = Mode::NewWorkspace;
    }

    fn on_new_form_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::KeyCode;
        match key.code {
            KeyCode::Esc => self.close_mode(),
            KeyCode::Tab | KeyCode::Down => self.form.field = (self.form.field + 1) % 3,
            KeyCode::BackTab | KeyCode::Up => self.form.field = (self.form.field + 2) % 3,
            KeyCode::Enter => {
                if self.form.name.trim().is_empty() {
                    self.state.status_flash = Some("name required".into());
                    return;
                }
                let form = std::mem::take(&mut self.form);
                self.close_mode();
                self.dispatch(Event::CreateWorkspace {
                    repo_root: form.repo_root,
                    name: form.name,
                    bookmark: form.bookmark,
                    prompt: form.prompt,
                });
            }
            KeyCode::Backspace => {
                self.form_field_mut().pop();
            }
            KeyCode::Char(c) => self.form_field_mut().push(c),
            _ => {}
        }
    }

    fn form_field_mut(&mut self) -> &mut String {
        match self.form.field {
            0 => &mut self.form.name,
            1 => &mut self.form.bookmark,
            _ => &mut self.form.prompt,
        }
    }

    /// Assign easymotion hint letters to the visible rows and enter find mode.
    fn open_find(&mut self) {
        let n = self.state.visible().len();
        self.find_hints = (0..n)
            .map(|i| ((b'a' + (i % 26) as u8) as char).to_string())
            .collect();
        self.input.clear();
        self.mode = Mode::Find;
    }

    fn on_find_key(&mut self, code: crossterm::event::KeyCode) {
        use crossterm::event::KeyCode;
        match code {
            KeyCode::Esc => self.close_mode(),
            KeyCode::Char(c) => {
                if let Some(idx) = self.find_hints.iter().position(|h| h == &c.to_string()) {
                    self.close_mode();
                    self.state.selected = idx;
                    self.dispatch(Event::OpenSelected);
                } else {
                    self.close_mode();
                }
            }
            _ => self.close_mode(),
        }
    }

    /// Return to the row list, clearing transient modal state.
    fn close_mode(&mut self) {
        self.mode = Mode::Normal;
        self.input.clear();
        self.find_hints.clear();
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
        // Centered overlays for the multi-line modals.
        match self.mode {
            Mode::NewWorkspace => self.draw_new_form(frame, area),
            Mode::Help => self.draw_help(frame, area),
            Mode::Jobs => self.draw_jobs(frame, area),
            _ => {}
        }
    }

    fn draw_jobs(&self, frame: &mut Frame, area: Rect) {
        use ratatui::widgets::Clear;
        let rect = Self::overlay_rect(area, 70, 20);
        frame.render_widget(Clear, rect);
        let mut lines = vec![
            Line::styled("jobs — recent operations", theme::project_header_style()),
            Line::raw(""),
        ];
        if self.jobs.is_empty() {
            lines.push(Line::styled("no operations yet", theme::muted_style()));
        }
        // Newest first, capped to the box height.
        for job in self.jobs.iter().rev().take(15) {
            let (glyph, style, detail) = match &job.outcome {
                Ok(s) => ("✓", Style::default().fg(Palette::SUCCESS), s.clone()),
                Err(e) => ("✗", Style::default().fg(Palette::DANGER), e.clone()),
            };
            lines.push(Line::from(vec![
                Span::styled(format!("{glyph} "), style),
                Span::styled(format!("{:<24}", job.title), Style::default()),
                Span::styled(detail, theme::muted_style()),
            ]));
        }
        lines.push(Line::raw(""));
        lines.push(Line::styled("any key closes", theme::muted_style()));
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Palette::ACCENT))
            .padding(ratatui::widgets::Padding::new(1, 1, 0, 0));
        frame.render_widget(Paragraph::new(lines).block(block), rect);
    }

    /// A centered bordered box, `frac`% of the area, cleared behind.
    fn overlay_rect(area: Rect, width: u16, height: u16) -> Rect {
        let w = width.min(area.width.saturating_sub(2));
        let h = height.min(area.height.saturating_sub(2));
        let x = area.x + (area.width.saturating_sub(w)) / 2;
        let y = area.y + (area.height.saturating_sub(h)) / 2;
        Rect::new(x, y, w, h)
    }

    fn draw_new_form(&self, frame: &mut Frame, area: Rect) {
        use ratatui::widgets::Clear;
        let rect = Self::overlay_rect(area, 60, 9);
        frame.render_widget(Clear, rect);
        let repo = basename(&self.form.repo_root);
        let field_line = |idx: usize, label: &str, value: &str| -> Line<'static> {
            let focused = self.form.field == idx;
            let marker = if focused { "▶ " } else { "  " };
            let style = if focused {
                theme::selection_style()
            } else {
                Style::default()
            };
            Line::from(vec![
                Span::styled(marker, theme::selection_style()),
                Span::styled(format!("{label:<9}"), theme::muted_style()),
                Span::styled(value.to_string(), style),
                if focused {
                    Span::styled("▏", theme::muted_style())
                } else {
                    Span::raw("")
                },
            ])
        };
        let lines = vec![
            Line::styled(
                format!("new workspace in {repo}"),
                theme::project_header_style(),
            ),
            Line::raw(""),
            field_line(0, "name", &self.form.name),
            field_line(1, "bookmark", &self.form.bookmark),
            field_line(2, "prompt", &self.form.prompt),
            Line::raw(""),
            Line::styled("tab next · enter create · esc cancel", theme::muted_style()),
        ];
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Palette::ACCENT))
            .padding(ratatui::widgets::Padding::new(1, 1, 0, 0));
        frame.render_widget(Paragraph::new(lines).block(block), rect);
    }

    fn draw_help(&self, frame: &mut Frame, area: Rect) {
        use ratatui::widgets::Clear;
        let rect = Self::overlay_rect(area, 60, 20);
        frame.render_widget(Clear, rect);
        let key = |k: &str, d: &str| -> Line<'static> {
            Line::from(vec![
                Span::styled(format!("{k:<10}"), theme::selection_style()),
                Span::raw(d.to_string()),
            ])
        };
        let lines = vec![
            Line::styled("awp deck — keys", theme::project_header_style()),
            Line::raw(""),
            key("j / k", "move selection"),
            key("gg / G", "jump top / bottom"),
            key("enter / a", "open agent pane"),
            key("L", "jump to last session"),
            key("s e c v i", "shell / editor / review / vcs / ci window"),
            key("n", "new workspace"),
            key("R / D", "rename / delete workspace"),
            key("p", "PR menu (open/merge/desc/set#)"),
            key("B", "link bookmark"),
            key("A", "send prompt to agent"),
            key("m …", "pin (m default / a–z / D unpin)"),
            key("f", "find (easymotion)"),
            key("J", "jobs / operation log"),
            key("/", "filter"),
            key("P", "cycle scope (all/attention/inbox)"),
            key("^a", "toggle deck / pane focus"),
            key("? / q", "help / quit"),
        ];
        let block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Palette::ACCENT))
            .padding(ratatui::widgets::Padding::new(1, 1, 0, 0));
        frame.render_widget(Paragraph::new(lines).block(block), rect);
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

        // In find mode, map each visible id to its hint letter.
        let hints: std::collections::HashMap<WorkspaceId, String> = if self.mode == Mode::Find {
            visible
                .iter()
                .cloned()
                .zip(self.find_hints.iter().cloned())
                .collect()
        } else {
            std::collections::HashMap::new()
        };

        let mut lines: Vec<Line<'static>> = Vec::new();
        for header in order {
            lines.push(Line::styled(header.clone(), theme::project_header_style()));
            for id in by_project.get(&header).into_iter().flatten() {
                let hint = hints.get(id).map(String::as_str);
                lines.push(self.row_line(id, selected.as_ref() == Some(id), hint));
            }
        }
        if lines.is_empty() {
            lines.push(Line::styled("no workspaces", theme::muted_style()));
        }
        lines
    }

    fn row_line(&self, id: &WorkspaceId, is_selected: bool, hint: Option<&str>) -> Line<'static> {
        let ws = self.state.workspace(id);
        let (status, unread, label, pr) = match ws {
            Some(w) => (w.status, w.unread, w.name.clone(), w.pr_number),
            None => (Status::Idle, false, id.name.clone(), None),
        };
        let (glyph, glyph_style) = theme::status_glyph(status, unread);
        let mut spans: Vec<Span<'static>> = Vec::new();
        if let Some(h) = hint {
            // Find-mode hint replaces the prefix slot.
            spans.push(Span::styled(format!("{h} "), theme::selection_style()));
        } else if is_selected {
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
        // Prompt-style footers for the single-line modal inputs.
        let input_prompt = |label: &str| -> Line<'static> {
            Line::from(vec![
                Span::styled(format!("{label}: "), theme::selection_style()),
                Span::raw(self.input.clone()),
                Span::styled("▏", theme::muted_style()),
            ])
        };
        match self.mode {
            Mode::Filter => {
                return Line::from(vec![
                    Span::styled("/", theme::selection_style()),
                    Span::raw(self.input.clone()),
                ])
            }
            Mode::Rename => return input_prompt("rename"),
            Mode::SetPr => return input_prompt("PR #"),
            Mode::Bookmark => return input_prompt("bookmark"),
            Mode::Prompt => return input_prompt("prompt"),
            Mode::Pin => {
                return Line::styled(
                    "pin → m default · a–z register · D unpin",
                    theme::muted_style(),
                )
            }
            Mode::PrMenu => {
                return Line::styled(
                    "PR → o open · m merge · d description · s set number · esc",
                    theme::muted_style(),
                )
            }
            Mode::ConfirmDelete => {
                let name = self
                    .target
                    .as_ref()
                    .map(|id| id.name.clone())
                    .unwrap_or_default();
                return Line::from(vec![
                    Span::styled(
                        format!("delete {name}? "),
                        Style::default().fg(Palette::DANGER),
                    ),
                    Span::styled("y", theme::selection_style()),
                    Span::raw(" / "),
                    Span::styled("n", theme::selection_style()),
                ]);
            }
            Mode::Find => {
                return Line::styled("find: press a hint letter · esc", theme::muted_style())
            }
            _ => {}
        }
        if let Some(flash) = &self.state.status_flash {
            return Line::styled(flash.clone(), theme::muted_style());
        }
        Line::styled(
            "j/k move · enter open · n new · R rename · D del · p PR · B mark · A prompt · f find · m pin · / filter · P scope · ? help · q quit",
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
        assert_eq!(app.mode, Mode::Pin);
        app.on_key(key('m'));
        assert_eq!(app.mode, Mode::Normal);
        assert!(app.state.workspace(&id).unwrap().is_pinned());
        // `m D` unpins.
        app.on_key(key('m'));
        app.on_key(KeyEvent::new(KeyCode::Char('D'), KeyModifiers::NONE));
        assert!(!app.state.workspace(&id).unwrap().is_pinned());
    }

    #[test]
    fn new_form_collects_fields_and_dispatches_create() {
        let mut app = seeded_app();
        app.on_key(key('n'));
        assert_eq!(app.mode, Mode::NewWorkspace);
        for c in "feat".chars() {
            app.on_key(key(c));
        }
        assert_eq!(app.form.name, "feat");
        app.on_key(KeyEvent::new(KeyCode::Tab, KeyModifiers::NONE));
        for c in "main".chars() {
            app.on_key(key(c));
        }
        assert_eq!(app.form.bookmark, "main");
        // Enter dispatches CreateWorkspace (create_workspace runs jj → may error
        // in a sandbox, but the mode closes regardless).
        app.on_key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE));
        assert_eq!(app.mode, Mode::Normal);
    }

    #[test]
    fn new_form_requires_name() {
        let mut app = seeded_app();
        app.on_key(key('n'));
        app.on_key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE));
        // Still open, flashed the requirement.
        assert_eq!(app.mode, Mode::NewWorkspace);
        assert!(app.state.status_flash.is_some());
    }

    #[test]
    fn confirm_delete_removes_on_y() {
        let mut app = seeded_app();
        // Select beta/wip (a single-workspace project) to avoid touching others.
        while app.state.selected_id() != Some(WorkspaceId::new("/r/beta", "wip")) {
            app.on_key(key('j'));
        }
        app.on_key(key('D'));
        assert_eq!(app.mode, Mode::ConfirmDelete);
        app.on_key(key('y'));
        assert_eq!(app.mode, Mode::Normal);
        assert!(app
            .state
            .workspace(&WorkspaceId::new("/r/beta", "wip"))
            .is_none());
    }

    #[test]
    fn confirm_delete_cancels_on_n() {
        let mut app = seeded_app();
        app.on_key(key('D'));
        app.on_key(key('n'));
        assert_eq!(app.mode, Mode::Normal);
        assert_eq!(app.visible().len(), 3);
    }

    #[test]
    fn pr_menu_set_number_flow() {
        let mut app = seeded_app();
        let id = app.state.selected_id().unwrap();
        app.on_key(key('p'));
        assert_eq!(app.mode, Mode::PrMenu);
        app.on_key(key('s'));
        assert_eq!(app.mode, Mode::SetPr);
        for c in "123".chars() {
            app.on_key(key(c));
        }
        app.on_key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE));
        assert_eq!(app.mode, Mode::Normal);
        assert_eq!(app.state.workspace(&id).unwrap().pr_number, Some(123));
    }

    #[test]
    fn bookmark_input_links() {
        let mut app = seeded_app();
        let id = app.state.selected_id().unwrap();
        app.on_key(key('B'));
        assert_eq!(app.mode, Mode::Bookmark);
        for c in "andrew/x".chars() {
            app.on_key(key(c));
        }
        app.on_key(KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE));
        assert_eq!(
            app.state.workspace(&id).unwrap().bookmark.as_deref(),
            Some("andrew/x")
        );
    }

    #[test]
    fn delete_records_a_job_and_jobs_overlay_opens() {
        let mut app = seeded_app();
        while app.state.selected_id() != Some(WorkspaceId::new("/r/beta", "wip")) {
            app.on_key(key('j'));
        }
        app.on_key(key('D'));
        app.on_key(key('y'));
        // A job was recorded (delete succeeds against the in-memory store even
        // though jj forget is a no-op here).
        assert!(!app.jobs.is_empty());
        assert_eq!(app.jobs.last().unwrap().title, "delete wip");
        // Overlay opens on J and closes on any key.
        app.on_key(key('J'));
        assert_eq!(app.mode, Mode::Jobs);
        let backend = TestBackend::new(100, 30);
        let mut term = Terminal::new(backend).unwrap();
        term.draw(|f| app.draw(f)).unwrap();
        app.on_key(key('x'));
        assert_eq!(app.mode, Mode::Normal);
    }

    #[test]
    fn help_overlay_opens_and_closes() {
        let mut app = seeded_app();
        app.on_key(key('?'));
        assert_eq!(app.mode, Mode::Help);
        app.on_key(key('x'));
        assert_eq!(app.mode, Mode::Normal);
    }

    #[test]
    fn find_hint_jumps_to_row() {
        let mut app = seeded_app();
        app.on_key(key('f'));
        assert_eq!(app.mode, Mode::Find);
        assert_eq!(app.find_hints.len(), 3);
        // Third row's hint is 'c'.
        app.sync_size(Rect::new(0, 0, 100, 30));
        app.on_key(key('c'));
        assert_eq!(app.mode, Mode::Normal);
        assert_eq!(app.state.selected, 2);
    }

    #[test]
    fn help_and_form_render_without_panicking() {
        let mut app = seeded_app();
        let backend = TestBackend::new(100, 30);
        let mut term = Terminal::new(backend).unwrap();
        app.on_key(key('?'));
        term.draw(|f| app.draw(f)).unwrap();
        app.close_mode();
        app.on_key(key('n'));
        term.draw(|f| app.draw(f)).unwrap();
    }

    #[test]
    fn encode_key_control_and_arrows() {
        let ctrl_c = encode_key(KeyEvent::new(KeyCode::Char('c'), KeyModifiers::CONTROL));
        assert_eq!(ctrl_c, Some(vec![3]));
        let left = encode_key(KeyEvent::new(KeyCode::Left, KeyModifiers::NONE));
        assert_eq!(left, Some(b"\x1b[D".to_vec()));
    }
}

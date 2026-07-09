//! The deck's design system: the semantic ANSI-16 palette and shared styles.
//!
//! Ports awp's palette (`internal/charm/palette.go`) and selection treatment.
//! All colors route through these tokens — **never** a raw 256-color code
//! inline — so the user's terminal palette remaps them. The tokens are ANSI 16
//! indices ("0"–"15").

use awp_core::Status;
use ratatui::style::{Color, Modifier, Style};

/// Semantic palette tokens. The ANSI index each maps to is in the comment.
pub struct Palette;

impl Palette {
    pub const ACCENT: Color = Color::Indexed(6); // teal — project headers, focus
    pub const INFO: Color = Color::Indexed(4); // blue — PR numbers, :port
    pub const SUCCESS: Color = Color::Indexed(2); // green — working/approved/done
    pub const WARNING: Color = Color::Indexed(3); // yellow — waiting/draft/selection
    pub const DANGER: Color = Color::Indexed(1); // red — errors, CI failing
    pub const SPINNER: Color = Color::Indexed(5); // magenta — spinner only
    pub const STRONG: Color = Color::Indexed(15); // bright white — emphasized
    pub const MUTED: Color = Color::Indexed(8); // bright black — hints, meta
}

/// The selection prefix bar, shared by every list/picker/overlay.
pub const SELECTION_PREFIX: &str = "┃ ";

/// The row-selection style: warning fg, bold. Paired with the `┃ ` prefix.
pub fn selection_style() -> Style {
    Style::default()
        .fg(Palette::WARNING)
        .add_modifier(Modifier::BOLD)
}

/// A teal, bold project header (all / attention scopes).
pub fn project_header_style() -> Style {
    Style::default()
        .fg(Palette::ACCENT)
        .add_modifier(Modifier::BOLD)
}

/// Muted style for hints / meta text / footer.
pub fn muted_style() -> Style {
    Style::default().fg(Palette::MUTED)
}

/// PR-number style: Info (blue), matching the Go deck.
pub fn pr_style() -> Style {
    Style::default().fg(Palette::INFO)
}

/// Spinner style: magenta, reserved for the spinner glyph only.
pub fn spinner_style() -> Style {
    Style::default().fg(Palette::SPINNER)
}

/// Emphasized text: bright white, bold. Used for the active pane's title.
pub fn strong_style() -> Style {
    Style::default()
        .fg(Palette::STRONG)
        .add_modifier(Modifier::BOLD)
}

/// The status-dot color for a workspace row. Mirrors Go `statusColor`.
/// Returns `None` when no dot should render (see [`Status::glyph_visible`]).
pub fn status_dot(status: Status, unread: bool) -> Option<Color> {
    if !status.glyph_visible(unread) {
        return None;
    }
    Some(match status {
        Status::Working => Palette::SUCCESS,
        Status::Waiting => Palette::WARNING,
        Status::Error => Palette::DANGER,
        Status::Starting => Palette::ACCENT,
        // idle / done / exited — only reach here when unread (notified).
        _ => Palette::MUTED,
    })
}

/// The status glyph — a colored `●`, or a blank when not visible.
pub fn status_glyph(status: Status, unread: bool) -> (&'static str, Style) {
    match status_dot(status, unread) {
        Some(color) => ("●", Style::default().fg(color)),
        None => (" ", Style::default()),
    }
}

/// The full semantic palette, so every token stays part of the design system
/// even before every screen uses it (PR numbers → `INFO`, the spinner →
/// `SPINNER`, emphasized text → `STRONG`).
#[cfg(test)]
pub fn all_tokens() -> [Color; 8] {
    [
        Palette::ACCENT,
        Palette::INFO,
        Palette::SUCCESS,
        Palette::WARNING,
        Palette::DANGER,
        Palette::SPINNER,
        Palette::STRONG,
        Palette::MUTED,
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn palette_tokens_are_ansi16_indices() {
        // All tokens are ANSI 0–15 so the user's terminal palette remaps them.
        for token in all_tokens() {
            match token {
                Color::Indexed(i) => assert!(i < 16, "token {i} escapes ANSI-16"),
                other => panic!("non-indexed palette token: {other:?}"),
            }
        }
    }

    #[test]
    fn working_status_shows_green_dot_without_unread() {
        assert_eq!(status_dot(Status::Working, false), Some(Palette::SUCCESS));
        assert_eq!(status_dot(Status::Exited, true), None);
    }
}

//! Agent status states and their semantics.
//!
//! Ports the Go deck's status model (`internal/deckui/model.go::statusColor` /
//! `statusGlyphVisible` and `internal/workspace` helpers). Colors live in the
//! TUI's theme module — the core only knows the *semantics* of each state
//! (does it want attention, is the agent gone, is it always shown).

use serde::{Deserialize, Serialize};
use std::fmt;

/// The closed set of agent statuses a workspace row can be in.
///
/// The Go version stored status as a free string; here it is a typed enum with
/// a lenient parser so legacy/hook strings (`"in progress"`, `"in_progress"`,
/// `"running"`) still map onto the canonical states.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize, Default)]
pub enum Status {
    #[default]
    Idle,
    Starting,
    Working,
    Waiting,
    Exited,
    Error,
    Done,
}

impl Status {
    /// Parse a status string leniently. Unknown/empty strings map to `Idle`.
    /// Mirrors the Go `statusColor` switch, which folds `"in progress"`,
    /// `"in_progress"` and `"running"` into the working state.
    pub fn parse(s: &str) -> Self {
        match s.trim().to_ascii_lowercase().as_str() {
            "working" | "in progress" | "in_progress" | "running" => Status::Working,
            "waiting" => Status::Waiting,
            "starting" => Status::Starting,
            "exited" => Status::Exited,
            "error" => Status::Error,
            "done" => Status::Done,
            _ => Status::Idle,
        }
    }

    /// The canonical lowercase string persisted to the store and emitted by
    /// `report-status`.
    pub fn as_str(self) -> &'static str {
        match self {
            Status::Idle => "idle",
            Status::Starting => "starting",
            Status::Working => "working",
            Status::Waiting => "waiting",
            Status::Exited => "exited",
            Status::Error => "error",
            Status::Done => "done",
        }
    }

    /// The set of states an agent hook is allowed to report. Matches the Go
    /// `validReportStates` map.
    pub fn is_reportable(self) -> bool {
        matches!(
            self,
            Status::Working | Status::Idle | Status::Waiting | Status::Exited
        )
    }

    /// True for states that render a status dot unconditionally (regardless of
    /// the unread flag). Mirrors Go `alwaysShownStatus`.
    pub fn always_shown(self) -> bool {
        matches!(self, Status::Working)
    }

    /// True for states that want the user's attention, flipping `unread` on the
    /// transition into them. Mirrors Go `workspace.WantsAttention`
    /// (waiting/idle are the attention states; working is loud but not
    /// "unread").
    pub fn wants_attention(self) -> bool {
        matches!(self, Status::Waiting | Status::Idle)
    }

    /// True when the agent is gone. `report-status` clears the unread badge on
    /// this transition. Mirrors Go `workspace.IsExited`.
    pub fn is_exited(self) -> bool {
        matches!(self, Status::Exited)
    }

    /// Whether a status dot is visible for this status/unread combination.
    /// Mirrors Go `statusGlyphVisible`: exited never shows; working always
    /// shows; everything else only when unread.
    pub fn glyph_visible(self, unread: bool) -> bool {
        if self.is_exited() {
            return false;
        }
        self.always_shown() || unread
    }
}

impl fmt::Display for Status {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_folds_working_aliases() {
        for s in [
            "working",
            "in progress",
            "in_progress",
            "running",
            "RUNNING",
        ] {
            assert_eq!(Status::parse(s), Status::Working, "input {s:?}");
        }
    }

    #[test]
    fn parse_unknown_is_idle() {
        assert_eq!(Status::parse(""), Status::Idle);
        assert_eq!(Status::parse("garbage"), Status::Idle);
    }

    #[test]
    fn exited_never_shows_a_glyph() {
        assert!(!Status::Exited.glyph_visible(true));
        assert!(!Status::Exited.glyph_visible(false));
    }

    #[test]
    fn working_always_shows_but_others_need_unread() {
        assert!(Status::Working.glyph_visible(false));
        assert!(!Status::Waiting.glyph_visible(false));
        assert!(Status::Waiting.glyph_visible(true));
        assert!(!Status::Idle.glyph_visible(false));
    }

    #[test]
    fn roundtrip_str() {
        for st in [
            Status::Idle,
            Status::Starting,
            Status::Working,
            Status::Waiting,
            Status::Exited,
            Status::Error,
            Status::Done,
        ] {
            assert_eq!(Status::parse(st.as_str()), st);
        }
    }
}

//! `awp-vt` — the VT engine trait and a ratatui-free [`Screen`].
//!
//! One engine: [`LibghosttyEngine`], backed by the real Ghostty terminal core
//! (`libghostty-vt`). `VtEngine` stays a trait so the renderer depends on a
//! boundary rather than a concrete type, but there is no engine *choice* — it is
//! libghostty. Feed bytes in, read a [`Screen`] out.

mod libghostty_engine;
mod screen;

pub use libghostty_engine::LibghosttyEngine;
pub use screen::{Cell, CellAttrs, Color, Cursor, Screen};

/// A virtual-terminal engine: bytes in, a renderable [`Screen`] out.
pub trait VtEngine {
    /// Feed output bytes from the PTY/session stream.
    fn process(&mut self, bytes: &[u8]);
    /// Resize the terminal grid (cols, then rows — matches the PTY winsize
    /// convention used across the session layer).
    fn resize(&mut self, cols: u16, rows: u16);
    /// A snapshot of the current screen for rendering.
    fn screen(&self) -> Screen;
}

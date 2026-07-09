//! `LibghosttyEngine` — the VT engine, backed by the real Ghostty terminal core
//! via the `libghostty-vt` crate (which links `libghostty-vt-sys`'s vendored
//! native library).
//!
//! This is not a fallback or a shim: bytes go into a real `ghostty` terminal
//! (`Terminal::vt_write`), and the screen is read back through Ghostty's render
//! state (row/cell iterators, resolved fg/bg colors and styles). The engine
//! projects that onto the ratatui-free [`Screen`] the deck renders.

use crate::screen::{Cell, CellAttrs, Color, Cursor, Screen};
use crate::VtEngine;
use libghostty_vt::render::{CellIterator, RenderState, RowIterator};
use libghostty_vt::style::{RgbColor, Underline};
use libghostty_vt::terminal::{Options, Terminal};
use std::cell::RefCell;

/// A live Ghostty terminal plus a reusable render state.
pub struct LibghosttyEngine {
    terminal: Terminal<'static, 'static>,
    // RenderState is reused across frames (allocating one per frame is wasteful).
    // RefCell because `screen(&self)` needs to mutate the render state to pull a
    // fresh snapshot while the trait method takes `&self`.
    render: RefCell<RenderState<'static>>,
    cols: u16,
    rows: u16,
}

impl LibghosttyEngine {
    pub fn new(rows: u16, cols: u16) -> Self {
        let cols = cols.max(1);
        let rows = rows.max(1);
        let terminal = Terminal::new(Options {
            cols,
            rows,
            max_scrollback: 0,
        })
        .expect("libghostty: create terminal");
        let render = RenderState::new().expect("libghostty: create render state");
        Self {
            terminal,
            render: RefCell::new(render),
            cols,
            rows,
        }
    }

    /// Always true: this build links the real native libghostty library.
    pub fn is_native() -> bool {
        true
    }
}

impl VtEngine for LibghosttyEngine {
    fn process(&mut self, bytes: &[u8]) {
        self.terminal.vt_write(bytes);
    }

    fn resize(&mut self, cols: u16, rows: u16) {
        let cols = cols.max(1);
        let rows = rows.max(1);
        // cell pixel dims are irrelevant for a cell grid renderer.
        let _ = self.terminal.resize(cols, rows, 0, 0);
        self.cols = cols;
        self.rows = rows;
    }

    fn screen(&self) -> Screen {
        let mut render = self.render.borrow_mut();
        let Ok(snap) = render.update(&self.terminal) else {
            return Screen::blank(self.rows, self.cols);
        };
        let cols = snap.cols().unwrap_or(self.cols).max(1);
        let rows = snap.rows().unwrap_or(self.rows).max(1);
        let mut cells = vec![Cell::default(); rows as usize * cols as usize];

        // Walk rows, then cells within each row, reading Ghostty's resolved
        // per-cell text/color/style.
        if let Ok(mut row_iter) = RowIterator::new() {
            if let Ok(mut rows_it) = row_iter.update(&snap) {
                let mut y: u16 = 0;
                while let Some(row) = rows_it.next() {
                    if y >= rows {
                        break;
                    }
                    if let Ok(mut cell_iter) = CellIterator::new() {
                        if let Ok(mut cells_it) = cell_iter.update(row) {
                            let mut x: u16 = 0;
                            while let Some(cell) = cells_it.next() {
                                if x >= cols {
                                    break;
                                }
                                let mut text = String::new();
                                let _ = cell.graphemes_utf8(&mut text);
                                let fg = cell.fg_color().ok().flatten();
                                let bg = cell.bg_color().ok().flatten();
                                let style = cell.style().unwrap_or_default();
                                let idx = y as usize * cols as usize + x as usize;
                                cells[idx] = Cell {
                                    content: text,
                                    fg: map_color(fg),
                                    bg: map_color(bg),
                                    attrs: CellAttrs {
                                        bold: style.bold,
                                        italic: style.italic,
                                        underline: style.underline != Underline::None,
                                        inverse: style.inverse,
                                    },
                                };
                                x += 1;
                            }
                        }
                    }
                    y += 1;
                }
            }
        }

        let cursor = snap.cursor_viewport().ok().flatten();
        let visible = snap.cursor_visible().unwrap_or(false);
        let (crow, ccol) = cursor.map(|c| (c.y, c.x)).unwrap_or((0, 0));
        Screen::from_cells(
            rows,
            cols,
            cells,
            Cursor {
                row: crow,
                col: ccol,
                visible,
            },
        )
    }
}

/// Map a Ghostty resolved cell color to the engine-agnostic [`Color`]. `None`
/// (no explicit color) maps to `Default` so the terminal's own default shows
/// through.
fn map_color(c: Option<RgbColor>) -> Color {
    match c {
        Some(rgb) => Color::Rgb(rgb.r, rgb.g, rgb.b),
        None => Color::Default,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn engine_renders_plain_text() {
        let mut e = LibghosttyEngine::new(4, 20);
        e.process(b"ghostty");
        assert_eq!(e.screen().row_text(0), "ghostty");
    }

    #[test]
    fn engine_captures_bold_and_color() {
        let mut e = LibghosttyEngine::new(2, 10);
        e.process(b"\x1b[1;31mhi\x1b[0m");
        let s = e.screen();
        let c = s.cell(0, 0).unwrap();
        assert_eq!(c.content, "h");
        assert!(c.attrs.bold);
        // Red resolves to an RGB value (palette resolved by ghostty).
        assert!(matches!(c.fg, Color::Rgb(_, _, _)));
    }

    #[test]
    fn resize_changes_dimensions() {
        let mut e = LibghosttyEngine::new(4, 20);
        e.resize(40, 10);
        let s = e.screen();
        assert_eq!(s.rows, 10);
        assert_eq!(s.cols, 40);
    }

    #[test]
    fn is_native_reports_true() {
        assert!(LibghosttyEngine::is_native());
    }
}

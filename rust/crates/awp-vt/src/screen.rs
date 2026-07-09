//! A ratatui-free terminal screen snapshot.
//!
//! Both VT engines (`vt100`, `libghostty`) produce this same `Screen` type, so
//! `awp-tui` needs exactly one widget to render either. Keeping it free of
//! ratatui means the VT layer can be unit-tested without a terminal and swapped
//! without touching call sites.

/// A terminal color, engine-agnostic.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Color {
    /// The terminal's default fg/bg — lets the user's palette show through.
    #[default]
    Default,
    /// A 256-color palette index.
    Indexed(u8),
    /// A true-color RGB triple.
    Rgb(u8, u8, u8),
}

/// Per-cell text attributes.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct CellAttrs {
    pub bold: bool,
    pub italic: bool,
    pub underline: bool,
    pub inverse: bool,
}

/// One screen cell: its grapheme plus styling.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Cell {
    /// The cell's text (usually one grapheme; empty for a blank cell).
    pub content: String,
    pub fg: Color,
    pub bg: Color,
    pub attrs: CellAttrs,
}

impl Default for Cell {
    fn default() -> Self {
        Self {
            content: String::new(),
            fg: Color::Default,
            bg: Color::Default,
            attrs: CellAttrs::default(),
        }
    }
}

/// Cursor position and visibility.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct Cursor {
    pub row: u16,
    pub col: u16,
    pub visible: bool,
}

/// A full screen snapshot: a `rows × cols` grid of cells plus the cursor.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Screen {
    pub rows: u16,
    pub cols: u16,
    cells: Vec<Cell>,
    pub cursor: Cursor,
}

impl Screen {
    /// A blank screen of the given size.
    pub fn blank(rows: u16, cols: u16) -> Self {
        Self {
            rows,
            cols,
            cells: vec![Cell::default(); rows as usize * cols as usize],
            cursor: Cursor::default(),
        }
    }

    /// Build from a row-major cell vector. Panics only in debug if the length
    /// doesn't match `rows * cols`.
    pub fn from_cells(rows: u16, cols: u16, cells: Vec<Cell>, cursor: Cursor) -> Self {
        debug_assert_eq!(cells.len(), rows as usize * cols as usize);
        Self {
            rows,
            cols,
            cells,
            cursor,
        }
    }

    /// The cell at `(row, col)`, or `None` if out of bounds.
    pub fn cell(&self, row: u16, col: u16) -> Option<&Cell> {
        if row >= self.rows || col >= self.cols {
            return None;
        }
        self.cells
            .get(row as usize * self.cols as usize + col as usize)
    }

    /// Iterate cells in row-major order.
    pub fn iter_cells(&self) -> impl Iterator<Item = (u16, u16, &Cell)> {
        let cols = self.cols;
        self.cells.iter().enumerate().map(move |(i, c)| {
            let idx = i as u16;
            (idx / cols, idx % cols, c)
        })
    }

    /// Plain-text row content, trailing blanks trimmed. Handy for tests and
    /// logging.
    pub fn row_text(&self, row: u16) -> String {
        let mut s = String::new();
        for col in 0..self.cols {
            if let Some(c) = self.cell(row, col) {
                s.push_str(if c.content.is_empty() {
                    " "
                } else {
                    &c.content
                });
            }
        }
        s.trim_end().to_string()
    }
}

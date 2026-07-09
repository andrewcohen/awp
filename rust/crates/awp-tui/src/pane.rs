//! The live-pane widget: maps an `awp_vt::Screen` onto a ratatui `Buffer`.
//!
//! This is the "custom ratatui widget" the spec calls for — one renderer for
//! *any* `VtEngine` (vt100 today, libghostty later), because both produce the
//! same engine-agnostic `Screen`. That's what lets a VT-engine swap touch only
//! `awp-vt`, never the deck.

use awp_vt::{CellAttrs, Color as VtColor, Screen};
use ratatui::buffer::Buffer;
use ratatui::layout::Rect;
use ratatui::style::{Color, Modifier, Style};
use ratatui::widgets::Widget;

/// Renders a screen snapshot into an area, top-left aligned and clipped.
pub struct PaneWidget<'a> {
    screen: &'a Screen,
}

impl<'a> PaneWidget<'a> {
    pub fn new(screen: &'a Screen) -> Self {
        Self { screen }
    }
}

impl Widget for PaneWidget<'_> {
    fn render(self, area: Rect, buf: &mut Buffer) {
        let rows = area.height.min(self.screen.rows);
        let cols = area.width.min(self.screen.cols);
        for row in 0..rows {
            for col in 0..cols {
                let Some(vc) = self.screen.cell(row, col) else {
                    continue;
                };
                let Some(cell) = buf.cell_mut((area.x + col, area.y + row)) else {
                    continue;
                };
                let symbol = if vc.content.is_empty() {
                    " "
                } else {
                    vc.content.as_str()
                };
                cell.set_symbol(symbol);
                cell.set_style(cell_style(vc.fg, vc.bg, vc.attrs));
            }
        }
    }
}

fn map_color(c: VtColor) -> Color {
    match c {
        VtColor::Default => Color::Reset,
        VtColor::Indexed(i) => Color::Indexed(i),
        VtColor::Rgb(r, g, b) => Color::Rgb(r, g, b),
    }
}

fn cell_style(fg: VtColor, bg: VtColor, attrs: CellAttrs) -> Style {
    let mut style = Style::default().fg(map_color(fg)).bg(map_color(bg));
    let mut m = Modifier::empty();
    if attrs.bold {
        m |= Modifier::BOLD;
    }
    if attrs.italic {
        m |= Modifier::ITALIC;
    }
    if attrs.underline {
        m |= Modifier::UNDERLINED;
    }
    if attrs.inverse {
        m |= Modifier::REVERSED;
    }
    style.add_modifier = m;
    style
}

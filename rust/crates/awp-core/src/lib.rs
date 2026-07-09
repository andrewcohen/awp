//! `awp-core` — the UI-agnostic domain core for awp.
//!
//! This crate compiles with **zero I/O and zero UI dependencies**. It holds the
//! domain types, the single [`AppState`], and the single [`reduce`] function
//! through which every mutation flows. The store, session, VT, and TUI crates
//! all depend on this one; it depends on none of them. That strict, enforced
//! dependency direction is the fix for the Go version's ~7k-line god-object.

mod effect;
mod event;
mod model;
mod reduce;
mod state;
mod status;

pub use effect::Effect;
pub use event::{Event, Focus};
pub use model::{PinGroup, PrRef, Project, Scope, Workspace, WorkspaceId};
pub use reduce::reduce;
pub use state::AppState;
pub use status::Status;

#[cfg(test)]
mod reduce_tests;

//! `awp-agent` — orchestration: jj/gh subprocesses, Claude hook install +
//! self-heal, and the `report-status` write path.
//!
//! This crate holds the language-agnostic integration glue that the Go version
//! spread across `internal/agenthooks` and `internal/cli`. It depends on
//! `awp-core` (types) and `awp-store` (the row-level status write); it has no
//! UI dependency.

pub mod hooks;
pub mod repo;
pub mod report_status;
pub mod subprocess;

pub use report_status::{Ident, ReportArgs};

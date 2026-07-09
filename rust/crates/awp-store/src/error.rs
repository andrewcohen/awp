use std::path::PathBuf;

/// Errors surfaced by the store. Library-level errors use `thiserror`; the
/// binary edge wraps them in `anyhow`. No error is ever swallowed.
#[derive(Debug, thiserror::Error)]
pub enum StoreError {
    #[error("open store at {path}: {source}")]
    Open {
        path: PathBuf,
        #[source]
        source: rusqlite::Error,
    },

    #[error("sqlite: {0}")]
    Sqlite(#[from] rusqlite::Error),

    #[error("read {path}: {source}")]
    ReadFile {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },

    #[error("parse {path}: {source}")]
    ParseJson {
        path: PathBuf,
        #[source]
        source: serde_json::Error,
    },

    #[error("resolve home directory")]
    NoHome,
}

pub type Result<T> = std::result::Result<T, StoreError>;

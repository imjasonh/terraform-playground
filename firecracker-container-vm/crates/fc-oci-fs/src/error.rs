use thiserror::Error;

#[derive(Debug, Error)]
pub enum Error {
    #[error("registry error: {0}")]
    Registry(String),

    #[error("oci distribution error: {0}")]
    Oci(#[from] oci_distribution::errors::OciDistributionError),

    #[error("http error: {0}")]
    Http(#[from] reqwest::Error),

    #[error("io error: {0}")]
    Io(#[from] std::io::Error),

    #[error("gzip index error: {0}")]
    GzIndex(#[from] indexed_deflate::Error),

    #[error("invalid image reference: {0}")]
    InvalidReference(String),

    #[error("path not found: {0}")]
    NotFound(String),

    #[error("unsupported layer media type: {0}")]
    UnsupportedMediaType(String),

    #[error("json error: {0}")]
    Json(#[from] serde_json::Error),
    #[error("{0}")]
    Other(String),
}

pub type Result<T> = std::result::Result<T, Error>;

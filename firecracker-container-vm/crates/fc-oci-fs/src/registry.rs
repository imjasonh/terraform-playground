use std::collections::HashMap;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use oci_distribution::client::Client;
use oci_distribution::Reference;
use reqwest::blocking::Client as HttpClient;
use reqwest::header::{AUTHORIZATION, RANGE};
use reqwest::StatusCode;

use crate::docker_auth::ResolvedAuth;
use crate::error::{Error, Result};
use crate::metrics;

const GZIP_LAYER_TYPES: &[&str] = &[
    "application/vnd.oci.image.layer.v1.tar+gzip",
    "application/vnd.docker.image.rootfs.diff.tar.gzip",
];

/// Cached redirect + range capability for a blob, following the dagdotdev explore approach.
#[derive(Debug, Clone)]
struct BlobEndpoint {
    url: String,
    size: u64,
    supports_range: bool,
    auth_header: Option<String>,
}

/// HTTP `Read + Seek` over an OCI blob using range requests.
pub struct RangeBlob {
    endpoint: BlobEndpoint,
    http: HttpClient,
    pos: u64,
    cache: Vec<u8>,
    cache_start: u64,
}

impl RangeBlob {
    pub fn len(&self) -> u64 {
        self.endpoint.size
    }

    fn fetch_range(&mut self, start: u64, end: u64) -> Result<Vec<u8>> {
        metrics::record_range_request();
        let range = format!("bytes={}-{}", start, end.saturating_sub(1));
        let mut req = self.http.get(&self.endpoint.url).header(RANGE, range);
        if let Some(auth) = &self.endpoint.auth_header {
            req = req.header(AUTHORIZATION, auth);
        }
        let resp = req.send()?.error_for_status()?;
        let bytes = resp.bytes()?.to_vec();
        metrics::record_bytes_fetched(bytes.len() as u64);
        Ok(bytes)
    }

    fn refill_cache(&mut self, pos: u64) -> Result<()> {
        let chunk = 256 * 1024;
        let end = (pos + chunk).min(self.endpoint.size);
        if pos >= self.endpoint.size {
            self.cache.clear();
            self.cache_start = pos;
            return Ok(());
        }
        let data = if self.endpoint.supports_range {
            self.fetch_range(pos, end)?
        } else {
            let mut req = self.http.get(&self.endpoint.url);
            if let Some(auth) = &self.endpoint.auth_header {
                req = req.header(AUTHORIZATION, auth);
            }
            let full = req.send()?.error_for_status()?;
            let bytes = full.bytes()?.to_vec();
            metrics::record_bytes_fetched(bytes.len() as u64);
            bytes[pos as usize..end as usize].to_vec()
        };
        self.cache = data;
        self.cache_start = pos;
        Ok(())
    }
}

impl Read for RangeBlob {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        if self.pos >= self.endpoint.size {
            return Ok(0);
        }
        if self.cache.is_empty()
            || self.pos < self.cache_start
            || self.pos >= self.cache_start + self.cache.len() as u64
        {
            self.refill_cache(self.pos)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::Other, e))?;
        }
        let offset = (self.pos - self.cache_start) as usize;
        let n = buf.len().min(self.cache.len() - offset);
        buf[..n].copy_from_slice(&self.cache[offset..offset + n]);
        self.pos += n as u64;
        Ok(n)
    }
}

impl Seek for RangeBlob {
    fn seek(&mut self, pos: SeekFrom) -> std::io::Result<u64> {
        self.pos = match pos {
            SeekFrom::Start(off) => off,
            SeekFrom::End(off) => {
                if off >= 0 {
                    self.endpoint.size + off as u64
                } else {
                    self.endpoint.size.saturating_sub((-off) as u64)
                }
            }
            SeekFrom::Current(off) => {
                if off >= 0 {
                    self.pos + off as u64
                } else {
                    self.pos.saturating_sub((-off) as u64)
                }
            }
        };
        Ok(self.pos)
    }
}

/// Registry client with docker config auth, ping/token caching, and range-aware blob access.
#[derive(Clone)]
pub struct RegistryClient {
    oci: Arc<Mutex<Client>>,
    http: HttpClient,
    auth: Arc<Mutex<HashMap<String, ResolvedAuth>>>,
    blob_cache: Arc<Mutex<HashMap<String, BlobEndpoint>>>,
    cache_dir: PathBuf,
}

impl RegistryClient {
    pub fn new(cache_dir: impl AsRef<Path>) -> Self {
        metrics::process_started();
        Self {
            oci: Arc::new(Mutex::new(Client::new(oci_distribution::client::ClientConfig {
                protocol: oci_distribution::client::ClientProtocol::Https,
                ..Default::default()
            }))),
            http: HttpClient::new(),
            auth: Arc::new(Mutex::new(HashMap::new())),
            blob_cache: Arc::new(Mutex::new(HashMap::new())),
            cache_dir: cache_dir.as_ref().to_path_buf(),
        }
    }

    pub fn cache_dir(&self) -> &Path {
        &self.cache_dir
    }

    fn auth_for(&self, reference: &Reference) -> Result<ResolvedAuth> {
        let key = reference.resolve_registry().to_string();
        if let Some(auth) = self.auth.lock().unwrap().get(&key).cloned() {
            return Ok(auth);
        }
        let auth = ResolvedAuth::for_reference(reference)?;
        self.auth.lock().unwrap().insert(key, auth.clone());
        Ok(auth)
    }

    pub async fn resolve_layers(&mut self, image: &str) -> Result<(Reference, Vec<String>)> {
        let reference: Reference = image
            .parse::<Reference>()
            .map_err(|e| Error::InvalidReference(format!("{e}")))?;
        let auth = self.auth_for(&reference)?;
        let (manifest, _) = self
            .oci
            .lock()
            .unwrap()
            .pull_manifest(&reference, &auth.to_oci_auth())
            .await?;
        let image_manifest = match manifest {
            oci_distribution::manifest::OciManifest::Image(m) => m,
            oci_distribution::manifest::OciManifest::ImageIndex(_) => {
                return Err(Error::Other(
                    "multi-arch indexes require platform selection (not implemented in example)"
                        .into(),
                ));
            }
        };

        let mut digests = Vec::new();
        for layer in image_manifest.layers {
            if !GZIP_LAYER_TYPES.contains(&layer.media_type.as_str()) {
                return Err(Error::UnsupportedMediaType(layer.media_type));
            }
            let digest = layer
                .digest
                .strip_prefix("sha256:")
                .unwrap_or(&layer.digest)
                .to_string();
            digests.push(digest);
        }
        Ok((reference, digests))
    }

    pub async fn open_blob(&mut self, reference: &Reference, digest: &str) -> Result<RangeBlob> {
        let key = format!("{}@{}", reference.repository(), digest);
        let auth = self.auth_for(reference)?;
        let endpoint = {
            let cached = self.blob_cache.lock().unwrap().get(&key).cloned();
            if let Some(ep) = cached {
                ep
            } else {
                let ep = self.probe_blob(reference, digest, &auth).await?;
                self.blob_cache.lock().unwrap().insert(key, ep.clone());
                ep
            }
        };
        Ok(RangeBlob {
            endpoint,
            http: self.http.clone(),
            pos: 0,
            cache: Vec::new(),
            cache_start: 0,
        })
    }

    pub async fn layer_compressed_size(
        &mut self,
        reference: &Reference,
        digest: &str,
    ) -> Result<u64> {
        let blob = self.open_blob(reference, digest).await?;
        Ok(blob.len())
    }

    async fn probe_blob(
        &mut self,
        reference: &Reference,
        digest: &str,
        auth: &ResolvedAuth,
    ) -> Result<BlobEndpoint> {
        let digest = format!("sha256:{digest}");
        let url = format!(
            "https://{}/v2/{}/blobs/{}",
            reference.resolve_registry(),
            reference.repository(),
            digest
        );

        let _ = self
            .oci
            .lock()
            .unwrap()
            .pull_manifest(reference, &auth.to_oci_auth())
            .await?;

        let auth_header = auth.authorization_header();

        let mut req = self.http.head(&url);
        if let Some(ref header) = auth_header {
            req = req.header(AUTHORIZATION, header);
        }
        let head = req.send()?;
        let head = head.error_for_status()?;

        let final_url = head.url().as_str().trim_end_matches('/').to_string();
        let size = head
            .headers()
            .get(reqwest::header::CONTENT_LENGTH)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.parse().ok())
            .unwrap_or(0);
        let supports_range = head
            .headers()
            .get(reqwest::header::ACCEPT_RANGES)
            .and_then(|v| v.to_str().ok())
            .map(|v| v.contains("bytes"))
            .unwrap_or(false);

        let supports_range = if supports_range {
            true
        } else {
            let mut probe = self.http.get(&final_url).header(RANGE, "bytes=0-0");
            if let Some(ref header) = auth_header {
                probe = probe.header(AUTHORIZATION, header);
            }
            let resp = probe.send()?;
            let ok = resp.status() == StatusCode::PARTIAL_CONTENT;
            if ok {
                metrics::record_range_request();
                if let Ok(body) = resp.bytes() {
                    metrics::record_bytes_fetched(body.len() as u64);
                }
            }
            ok
        };

        Ok(BlobEndpoint {
            url: final_url,
            size,
            supports_range,
            auth_header,
        })
    }
}

/// Convenience wrapper for local fixture blobs used in tests and benchmarks.
pub struct LocalBlob {
    inner: std::fs::File,
    len: u64,
}

impl LocalBlob {
    pub fn open(path: impl AsRef<Path>) -> Result<Self> {
        let file = std::fs::File::open(path.as_ref())?;
        let len = file.metadata()?.len();
        Ok(Self { inner: file, len })
    }

    pub fn len(&self) -> u64 {
        self.len
    }
}

impl Read for LocalBlob {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        self.inner.read(buf)
    }
}

impl Seek for LocalBlob {
    fn seek(&mut self, pos: SeekFrom) -> std::io::Result<u64> {
        self.inner.seek(pos)
    }
}

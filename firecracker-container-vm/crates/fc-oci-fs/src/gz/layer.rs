use std::fs::File;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};

use indexed_deflate::{AccessPointSpan, GzDecoder, GzIndexBuilder};
use serde::{Deserialize, Serialize};

use crate::error::{Error, Result};
use crate::registry::{LocalBlob, RangeBlob};
use crate::tar_index::{TarEntry, TarIndex};

/// Source bytes for a single OCI image layer.
pub enum LayerSource {
    Remote(RangeBlob),
    Local(LocalBlob),
}

impl LayerSource {
    pub fn len(&self) -> u64 {
        match self {
            LayerSource::Remote(blob) => blob.len(),
            LayerSource::Local(blob) => blob.len(),
        }
    }
}

impl Read for LayerSource {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        match self {
            LayerSource::Remote(blob) => blob.read(buf),
            LayerSource::Local(blob) => blob.read(buf),
        }
    }
}

impl Seek for LayerSource {
    fn seek(&mut self, pos: SeekFrom) -> std::io::Result<u64> {
        match self {
            LayerSource::Remote(blob) => blob.seek(pos),
            LayerSource::Local(blob) => blob.seek(pos),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct CachedLayerIndex {
    digest: String,
    toc: Vec<TarEntry>,
}

/// A gzip-indexed OCI layer with a tar table-of-contents for random file access.
///
/// This follows the dagdotdev explore approach: build a zran-style gzip index
/// (via `indexed_deflate`) and record tar entry offsets in the uncompressed stream.
pub struct IndexedLayer {
    digest: String,
    blob_path: PathBuf,
    index_path: PathBuf,
    toc_path: PathBuf,
    toc: TarIndex,
}

impl IndexedLayer {
    pub fn digest(&self) -> &str {
        &self.digest
    }

    pub fn toc(&self) -> &TarIndex {
        &self.toc
    }

    pub fn open(
        digest: impl Into<String>,
        source: LayerSource,
        cache_dir: impl AsRef<Path>,
    ) -> Result<Self> {
        let digest = digest.into();
        let cache_dir = cache_dir.as_ref().to_path_buf();
        std::fs::create_dir_all(&cache_dir)?;
        let index_path = cache_dir.join(format!("{digest}.gz.idx"));
        let toc_path = cache_dir.join(format!("{digest}.toc.json"));

        let toc = if index_path.exists() && toc_path.exists() {
            let data = std::fs::read_to_string(&toc_path)?;
            let cached: CachedLayerIndex = serde_json::from_str(&data)?;
            TarIndex::from_entries(cached.toc)
        } else {
            build_index_and_toc(&digest, source, &index_path, &toc_path)?
        };

        Ok(Self {
            digest,
            blob_path: index_path.with_extension("blob"),
            index_path,
            toc_path,
            toc,
        })
    }

    pub fn read_file(&self, entry: &TarEntry, offset: u64, buf: &mut [u8]) -> Result<usize> {
        if offset >= entry.size {
            return Ok(0);
        }
        let to_read = buf.len().min((entry.size - offset) as usize);
        let gz = File::open(&self.blob_path)?;
        let index = File::open(&self.index_path)?;
        let mut decoder = GzDecoder::new(gz, index)?;
        decoder.seek(SeekFrom::Start(entry.offset + offset))?;
        decoder
            .read_exact(&mut buf[..to_read])
            .map_err(Error::from)?;
        Ok(to_read)
    }
}

fn build_index_and_toc(
    digest: &str,
    mut source: LayerSource,
    index_path: &Path,
    toc_path: &Path,
) -> Result<TarIndex> {
    let blob_path = index_path.with_extension("blob");
    if !blob_path.exists() {
        std::io::copy(&mut source, &mut File::create(&blob_path)?)?;
    }

    let gz = File::open(&blob_path)?;
    let mut index_file = File::options()
        .create(true)
        .truncate(true)
        .read(true)
        .write(true)
        .open(index_path)?;

    let mut builder = GzIndexBuilder::new(gz, &mut index_file, AccessPointSpan::default())?;

    let mut entries = Vec::new();
    let mut archive = tar::Archive::new(&mut builder);
    for entry in archive.entries_with_seek()? {
        let entry = entry?;
        let path = entry
            .path()
            .map_err(|e| Error::Other(e.to_string()))?
            .to_string_lossy()
            .into_owned();
        let offset = entry.raw_file_position();
        let size = entry.header().size()?;
        let mode = entry.header().mode()?;
        let entry_type = entry.header().entry_type();
        let link_target = entry.header().link_name()?.map(|p| p.to_string_lossy().into_owned());
        entries.push(TarEntry {
            path: normalize_tar_path(&path),
            offset,
            size,
            mode,
            entry_type: format!("{entry_type:?}"),
            link_target,
        });
    }
    builder.finish()?;

    let toc = TarIndex::from_entries(entries);
    let cached = CachedLayerIndex {
        digest: digest.to_string(),
        toc: toc.entries_cloned(),
    };
    std::fs::write(toc_path, serde_json::to_string_pretty(&cached)?)?;
    Ok(toc)
}

fn normalize_tar_path(path: &str) -> String {
    let trimmed = path.trim_start_matches("./");
    if trimmed.starts_with('/') {
        trimmed.to_string()
    } else {
        format!("/{trimmed}")
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use flate2::write::GzEncoder;
    use flate2::Compression;
    use tar::Builder;
    use tempfile::tempdir;

    fn sample_layer(dir: &Path) -> PathBuf {
        let tar_path = dir.join("layer.tar");
        let gz_path = dir.join("layer.tar.gz");
        {
            let tar = File::create(&tar_path).unwrap();
            let mut builder = Builder::new(tar);
            let data = b"hello from lazy layer";
            let mut header = tar::Header::new_gnu();
            header.set_size(data.len() as u64);
            header.set_mode(0o644);
            header.set_cksum();
            builder
                .append_data(&mut header, "etc/hostname", &data[..])
                .unwrap();
            builder.finish().unwrap();
        }
        {
            let mut gz = GzEncoder::new(Vec::new(), Compression::default());
            let mut input = File::open(&tar_path).unwrap();
            std::io::copy(&mut input, &mut gz).unwrap();
            std::fs::write(&gz_path, gz.finish().unwrap()).unwrap();
        }
        gz_path
    }

    #[test]
    fn builds_index_and_reads_file() {
        let dir = tempdir().unwrap();
        let gz_path = sample_layer(dir.path());
        let cache = dir.path().join("cache");
        let layer = IndexedLayer::open(
            "deadbeef",
            LayerSource::Local(LocalBlob::open(&gz_path).unwrap()),
            &cache,
        )
        .unwrap();
        let entry = layer.toc().get("/etc/hostname").expect("entry");
        let mut buf = vec![0u8; entry.size as usize];
        let n = layer.read_file(entry, 0, &mut buf).unwrap();
        assert_eq!(n, buf.len());
        assert_eq!(String::from_utf8_lossy(&buf), "hello from lazy layer");
    }
}

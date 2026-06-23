use std::path::Path;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::OnceLock;
use std::time::{Duration, Instant};

static STARTUP: OnceLock<Instant> = OnceLock::new();
static PROCESS_START: OnceLock<Instant> = OnceLock::new();

static BYTES_FETCHED: AtomicU64 = AtomicU64::new(0);
static BYTES_SAVED: AtomicU64 = AtomicU64::new(0);
static RANGE_REQUESTS: AtomicU64 = AtomicU64::new(0);
static FULL_BLOB_DOWNLOADS: AtomicU64 = AtomicU64::new(0);
static INDEX_BUILDS: AtomicU64 = AtomicU64::new(0);
static FUSE_REQUESTS: AtomicU64 = AtomicU64::new(0);
static FUSE_READS: AtomicU64 = AtomicU64::new(0);
static LAYER_BYTES_TOTAL: AtomicU64 = AtomicU64::new(0);
static STARTUP_READY_MS: AtomicU64 = AtomicU64::new(0);

pub fn process_started() {
    let _ = PROCESS_START.set(Instant::now());
}

pub fn mark_startup_begin() {
    let _ = STARTUP.set(Instant::now());
}

pub fn mark_startup_ready() {
    if let Some(t0) = STARTUP.get() {
        STARTUP_READY_MS.store(t0.elapsed().as_millis() as u64, Ordering::Relaxed);
    }
}

pub fn record_layer_sizes(total_compressed_bytes: u64) {
    LAYER_BYTES_TOTAL.store(total_compressed_bytes, Ordering::Relaxed);
}

pub fn record_bytes_fetched(n: u64) {
    BYTES_FETCHED.fetch_add(n, Ordering::Relaxed);
    recompute_bytes_saved();
}

pub fn record_range_request() {
    RANGE_REQUESTS.fetch_add(1, Ordering::Relaxed);
}

pub fn record_full_blob_download(bytes: u64) {
    FULL_BLOB_DOWNLOADS.fetch_add(1, Ordering::Relaxed);
    record_bytes_fetched(bytes);
}

pub fn record_index_build(duration: Duration) {
    INDEX_BUILDS.fetch_add(1, Ordering::Relaxed);
    let _ = duration;
}

pub fn record_fuse_request() {
    FUSE_REQUESTS.fetch_add(1, Ordering::Relaxed);
}

pub fn record_fuse_read(bytes: u64) {
    FUSE_READS.fetch_add(1, Ordering::Relaxed);
    let _ = bytes;
}

fn recompute_bytes_saved() {
    let total = LAYER_BYTES_TOTAL.load(Ordering::Relaxed);
    let fetched = BYTES_FETCHED.load(Ordering::Relaxed);
    if total > fetched {
        BYTES_SAVED.store(total - fetched, Ordering::Relaxed);
    }
}

pub fn cache_dir_bytes(cache_dir: &Path) -> u64 {
    dir_size(cache_dir).unwrap_or(0)
}

fn dir_size(path: &Path) -> std::io::Result<u64> {
    let mut total = 0u64;
    if !path.exists() {
        return Ok(0);
    }
    for entry in std::fs::read_dir(path)? {
        let entry = entry?;
        let meta = entry.metadata()?;
        if meta.is_dir() {
            total = total.saturating_add(dir_size(&entry.path())?);
        } else {
            total = total.saturating_add(meta.len());
        }
    }
    Ok(total)
}

pub fn rss_bytes() -> u64 {
    std::fs::read_to_string("/proc/self/status")
        .ok()
        .and_then(|s| {
            s.lines()
                .find(|l| l.starts_with("VmRSS:"))
                .and_then(|l| l.split_whitespace().nth(1))
                .and_then(|kb| kb.parse::<u64>().ok())
        })
        .map(|kb| kb * 1024)
        .unwrap_or(0)
}

pub fn uptime_seconds() -> f64 {
    PROCESS_START
        .get()
        .map(|t| t.elapsed().as_secs_f64())
        .unwrap_or(0.0)
}

pub fn render_prometheus(cache_dir: &Path) -> String {
    let fetched = BYTES_FETCHED.load(Ordering::Relaxed);
    let saved = BYTES_SAVED.load(Ordering::Relaxed);
    let layer_total = LAYER_BYTES_TOTAL.load(Ordering::Relaxed);
    let cache_bytes = cache_dir_bytes(cache_dir);
    let rss = rss_bytes();
    let startup_ms = STARTUP_READY_MS.load(Ordering::Relaxed);

    format!(
        r#"# HELP fc_bytes_fetched_from_registry Total compressed bytes read from the OCI registry.
# TYPE fc_bytes_fetched_from_registry counter
fc_bytes_fetched_from_registry {fetched}
# HELP fc_bytes_saved_vs_full_pull Bytes not downloaded compared to pulling full layer blobs.
# TYPE fc_bytes_saved_vs_full_pull gauge
fc_bytes_saved_vs_full_pull {saved}
# HELP fc_layer_compressed_bytes_total Sum of compressed layer blob sizes in the resolved image.
# TYPE fc_layer_compressed_bytes_total gauge
fc_layer_compressed_bytes_total {layer_total}
# HELP fc_registry_range_requests_total HTTP range requests issued to registry blob URLs.
# TYPE fc_registry_range_requests_total counter
fc_registry_range_requests_total {range_reqs}
# HELP fc_full_blob_downloads_total Number of full layer blobs copied to local cache.
# TYPE fc_full_blob_downloads_total counter
fc_full_blob_downloads_total {full_downloads}
# HELP fc_gzip_index_builds_total Gzip index builds performed for layers.
# TYPE fc_gzip_index_builds_total counter
fc_gzip_index_builds_total {index_builds}
# HELP fc_fuse_requests_total FUSE/virtio-fs requests handled by the daemon.
# TYPE fc_fuse_requests_total counter
fc_fuse_requests_total {fuse_reqs}
# HELP fc_fuse_reads_total File read operations served from lazy layers.
# TYPE fc_fuse_reads_total counter
fc_fuse_reads_total {fuse_reads}
# HELP fc_startup_ready_milliseconds Time from image open start until vhost socket is listening.
# TYPE fc_startup_ready_milliseconds gauge
fc_startup_ready_milliseconds {startup_ms}
# HELP fc_process_uptime_seconds Daemon uptime.
# TYPE fc_process_uptime_seconds gauge
fc_process_uptime_seconds {uptime}
# HELP fc_process_rss_bytes Resident set size of the vhost-fs daemon.
# TYPE fc_process_rss_bytes gauge
fc_process_rss_bytes {rss}
# HELP fc_cache_dir_bytes_on_disk Bytes used by layer blobs and gzip indexes on disk.
# TYPE fc_cache_dir_bytes_on_disk gauge
fc_cache_dir_bytes_on_disk {cache_bytes}
"#,
        fetched = fetched,
        saved = saved,
        layer_total = layer_total,
        range_reqs = RANGE_REQUESTS.load(Ordering::Relaxed),
        full_downloads = FULL_BLOB_DOWNLOADS.load(Ordering::Relaxed),
        index_builds = INDEX_BUILDS.load(Ordering::Relaxed),
        fuse_reqs = FUSE_REQUESTS.load(Ordering::Relaxed),
        fuse_reads = FUSE_READS.load(Ordering::Relaxed),
        startup_ms = startup_ms,
        uptime = uptime_seconds(),
        rss = rss,
        cache_bytes = cache_bytes,
    )
}

#[cfg(test)]
mod tests {
    use super::{
        mark_startup_begin, mark_startup_ready, process_started, record_bytes_fetched,
        record_layer_sizes, render_prometheus,
    };
    use tempfile::tempdir;

    #[test]
    fn prometheus_includes_saved_bytes() {
        process_started();
        mark_startup_begin();
        record_layer_sizes(1_000_000);
        record_bytes_fetched(250_000);
        mark_startup_ready();
        let dir = tempdir().unwrap();
        let body = render_prometheus(dir.path());
        assert!(body.contains("fc_bytes_saved_vs_full_pull 750000"));
        assert!(body.contains("fc_startup_ready_milliseconds"));
    }
}

use std::path::Path;
use std::time::Instant;

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use fc_oci_fs::{ImageFs, IndexedLayer, LayerSource};
use fc_oci_fs::registry::LocalBlob;
use flate2::write::GzEncoder;
use flate2::Compression;
use tar::Builder;
use tempfile::tempdir;

fn build_large_layer(dir: &Path, files: usize) -> std::path::PathBuf {
    let tar_path = dir.join("layer.tar");
    let gz_path = dir.join("layer.tar.gz");
    {
        let tar = std::fs::File::create(&tar_path).unwrap();
        let mut builder = Builder::new(tar);
        for i in 0..files {
            let path = format!("opt/data/file_{i:05}.txt");
            let payload = format!("payload-{i}-{}\n", "x".repeat(1024));
            let bytes = payload.as_bytes();
            let mut header = tar::Header::new_gnu();
            header.set_size(bytes.len() as u64);
            header.set_mode(0o644);
            header.set_cksum();
            builder.append_data(&mut header, path, bytes).unwrap();
        }
        builder.finish().unwrap();
    }
    let mut gz = GzEncoder::new(Vec::new(), Compression::default());
    std::io::copy(
        &mut std::fs::File::open(&tar_path).unwrap(),
        &mut gz,
    )
    .unwrap();
    std::fs::write(&gz_path, gz.finish().unwrap()).unwrap();
    gz_path
}

fn bench_layer_read(c: &mut Criterion) {
    let dir = tempdir().unwrap();
    let gz = build_large_layer(dir.path(), 200);
    let layer = IndexedLayer::open(
        "bench",
        LayerSource::Local(LocalBlob::open(&gz).unwrap()),
        dir.path().join("cache"),
    )
    .unwrap();
    let last = layer
        .toc()
        .entries()
        .max_by_key(|e| e.offset)
        .unwrap()
        .clone();

    c.bench_function("read_last_file_1kb", |b| {
        b.iter(|| {
            let mut buf = vec![0u8; 1024];
            let n = layer.read_file(black_box(&last), 0, &mut buf).unwrap();
            black_box(n);
        })
    });
}

fn bench_overlay_lookup(c: &mut Criterion) {
    let dir = tempdir().unwrap();
    let gz = build_large_layer(dir.path(), 50);
    let fs = ImageFs::open_local_layer("bench", &gz, dir.path().join("cache")).unwrap();
    use fuse_backend_rs::api::filesystem::{Context, FileSystem, ROOT_ID};
    use std::ffi::CString;
    let ctx = Context {
        uid: 0,
        gid: 0,
        pid: 1,
    };
    c.bench_function("lookup_opt_data_file", |b| {
        b.iter(|| {
            let entry = fs
                .overlay
                .lookup(&ctx, ROOT_ID, &CString::new("opt").unwrap())
                .unwrap();
            black_box(entry.inode);
        })
    });
}

criterion_group!(benches, bench_layer_read, bench_overlay_lookup);
criterion_main!(benches);

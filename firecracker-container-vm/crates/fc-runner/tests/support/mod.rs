use std::path::{Path, PathBuf};

use flate2::write::GzEncoder;
use flate2::Compression;
use tar::Builder;

pub fn build_fixture_image(dir: &Path) -> PathBuf {
    let tar_path = dir.join("layer.tar");
    let gz_path = dir.join("layer.tar.gz");
    {
        let tar = std::fs::File::create(&tar_path).unwrap();
        let mut builder = Builder::new(tar);
        let payload = b"#!/bin/sh\necho hello-from-lazy-rootfs\n";
        let mut header = tar::Header::new_gnu();
        header.set_size(payload.len() as u64);
        header.set_mode(0o755);
        header.set_cksum();
        builder
            .append_data(&mut header, "bin/hello", &payload[..])
            .unwrap();
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

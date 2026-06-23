//! vhost-user virtio-fs daemon exposing an on-demand OCI image rootfs.

mod backend;
mod metrics_server;

use std::path::PathBuf;
use std::sync::Arc;

use anyhow::Context as _;
use clap::Parser;
use fc_oci_fs::{metrics, ImageFs, RegistryClient};
use fuse_backend_rs::api::server::Server;
use log::info;
use tokio::runtime::Runtime;
use vhost::vhost_user::Listener;
use vhost_user_backend::VhostUserDaemon;
use vm_memory::{GuestAddress, GuestMemoryAtomic, GuestMemoryMmap};

use crate::backend::FsBackendHandler;

#[derive(Debug, Parser)]
#[command(name = "fc-vhostfsd", about = "vhost-user virtio-fs daemon for lazy OCI rootfs")]
struct Args {
    /// Container image reference (e.g. docker.io/library/alpine:3.20)
    #[arg(long)]
    image: String,

    /// Unix socket path for the vhost-user frontend (Firecracker, Cloud Hypervisor, etc.)
    #[arg(long, default_value = "/tmp/fc-vhostfs.sock")]
    socket: PathBuf,

    /// Cache directory for gzip indexes and downloaded layer blobs
    #[arg(long, default_value = "/tmp/fc-oci-cache")]
    cache_dir: PathBuf,

    /// virtio-fs tag visible inside the guest
    #[arg(long, default_value = "rootfs")]
    tag: String,

    /// Prometheus metrics listen address (GET /metrics)
    #[arg(long, default_value = "127.0.0.1:9100")]
    metrics_addr: String,
}

fn main() -> anyhow::Result<()> {
    env_logger::init();
    let args = Args::parse();

    let cache_dir: Arc<std::path::Path> = Arc::from(args.cache_dir.as_path());
    let _metrics = metrics_server::spawn_metrics_server(&args.metrics_addr, cache_dir.clone());

    let rt = Runtime::new().context("create tokio runtime")?;
    let mut registry = RegistryClient::new(&args.cache_dir);
    let image_fs = rt
        .block_on(ImageFs::open(&args.image, &args.cache_dir, &mut registry))
        .context("open OCI image filesystem")?;

    info!(
        "serving {} ({} layers) on {}",
        args.image,
        image_fs.image.layer_digests.len(),
        args.socket.display()
    );

    let server = Arc::new(Server::new(image_fs.overlay));
    let backend = FsBackendHandler::new(server, args.tag)?;
    let memfd = nix::sys::memfd::memfd_create("fc-vhostfs", nix::sys::memfd::MFdFlags::empty())
        .map_err(|e| anyhow::anyhow!("memfd_create: {e}"))?;
    let file = std::fs::File::from(memfd);
    file.set_len(0x100000)
        .map_err(|e| anyhow::anyhow!("set_len: {e}"))?;
    let file_offset = vm_memory::FileOffset::new(file, 0);
    let mem = GuestMemoryAtomic::new(
        GuestMemoryMmap::from_ranges_with_files(&[(GuestAddress(0x100000), 0x100000, Some(file_offset))])
            .map_err(|e| anyhow::anyhow!("guest memory: {e:?}"))?,
    );
    let backend = std::sync::Arc::new(std::sync::Mutex::new(backend));
    let mut daemon = VhostUserDaemon::new("fc-vhostfsd".to_string(), backend, mem)
        .map_err(|e| anyhow::anyhow!("create vhost-user daemon: {e:?}"))?;

    if let Some(parent) = args.socket.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let _ = std::fs::remove_file(&args.socket);
    let mut listener = Listener::new(&args.socket, true).context("bind vhost-user socket")?;
    daemon.start(&mut listener).map_err(|e| anyhow::anyhow!("start daemon: {e:?}"))?;
    metrics::mark_startup_ready();
    info!("vhost-user virtio-fs daemon listening on {}", args.socket.display());
    daemon.wait().map_err(|e| anyhow::anyhow!("daemon exited: {e:?}"))?;
    Ok(())
}

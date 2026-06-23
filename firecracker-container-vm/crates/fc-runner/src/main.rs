//! Example Firecracker runner that boots a microVM with a lazy OCI rootfs via virtio-fs.

mod firecracker;
mod vhostfs;

use std::path::PathBuf;

use anyhow::Context as _;
use clap::Parser;
use log::info;

#[derive(Debug, Parser)]
#[command(
    name = "fc-runner",
    about = "Boot Firecracker with on-demand OCI container image rootfs"
)]
struct Args {
    /// Container image reference used as the guest root filesystem
    #[arg(long)]
    image: String,

    /// Path to the Firecracker binary
    #[arg(long, default_value = "firecracker")]
    firecracker: PathBuf,

    /// Path to the guest kernel (vmlinux or bzImage with virtio_fs support)
    #[arg(long)]
    kernel: PathBuf,

    /// Guest memory in MiB
    #[arg(long, default_value_t = 512)]
    memory_mib: u32,

    /// Number of vCPUs
    #[arg(long, default_value_t = 1)]
    vcpus: u32,

    /// vhost-user socket for virtio-fs
    #[arg(long, default_value = "/tmp/fc-vhostfs.sock")]
    vhost_socket: PathBuf,

    /// OCI layer/index cache directory
    #[arg(long, default_value = "/tmp/fc-oci-cache")]
    cache_dir: PathBuf,

    /// virtio-fs mount tag inside the guest
    #[arg(long, default_value = "rootfs")]
    tag: String,

    /// Only generate the Firecracker configuration JSON and start the vhost-fs daemon
    #[arg(long)]
    dry_run: bool,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    env_logger::init();
    let args = Args::parse();

    let mut vhostfs = vhostfs::spawn(&args).context("start vhost-fs daemon")?;
    info!("vhost-fs daemon pid={}", vhostfs.child.id());

    if args.dry_run {
        let config = firecracker::build_config(&args)?;
        println!("{}", serde_json::to_string_pretty(&config)?);
        info!("dry-run: vhost-fs daemon running, firecracker not started");
        vhostfs.child.wait().context("wait for vhostfsd")?;
        return Ok(());
    }

    firecracker::run_vm(&args).context("run firecracker vm")?;
    let _ = vhostfs.child.kill();
    Ok(())
}

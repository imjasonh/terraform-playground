use std::path::PathBuf;
use std::process::{Child, Command, Stdio};

use anyhow::{bail, Context as _};

use crate::Args;

pub struct VhostFsProcess {
    pub child: Child,
}

pub fn spawn(args: &Args) -> anyhow::Result<VhostFsProcess> {
    let bin = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.join("fc-vhostfsd")))
        .filter(|p| p.exists())
        .unwrap_or_else(|| PathBuf::from("fc-vhostfsd"));

    if let Some(parent) = args.vhost_socket.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let _ = std::fs::remove_file(&args.vhost_socket);

    let child = Command::new(bin)
        .arg("--image")
        .arg(&args.image)
        .arg("--socket")
        .arg(&args.vhost_socket)
        .arg("--cache-dir")
        .arg(&args.cache_dir)
        .arg("--tag")
        .arg(&args.tag)
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit())
        .spawn()
        .context("spawn fc-vhostfsd")?;

    std::thread::sleep(std::time::Duration::from_millis(500));
    if !args.vhost_socket.exists() {
        bail!("vhost-user socket was not created at {}", args.vhost_socket.display());
    }
    Ok(VhostFsProcess { child })
}

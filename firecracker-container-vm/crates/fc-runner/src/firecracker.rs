use std::fs;
use std::path::PathBuf;
use std::process::{Command, Stdio};

use anyhow::Context as _;
use serde_json::json;

use crate::Args;

/// Firecracker API configuration for a microVM with generic vhost-user virtio-fs.
///
/// Requires a Firecracker build that includes the generic vhost-user frontend
/// (`PUT /vhost-user-devices/{id}`), as described in firecracker PR #5773.
pub fn build_config(args: &Args) -> anyhow::Result<serde_json::Value> {
    let api_sock = args
        .cache_dir
        .join("firecracker.api.sock");
    let log_path = args.cache_dir.join("firecracker.log");
    fs::create_dir_all(&args.cache_dir)?;

    Ok(json!({
        "boot-source": {
            "kernel_image_path": args.kernel,
            "boot_args": format!(
                "console=ttyS0 reboot=k panic=1 pci=off init=/bin/sh rootfstype=virtiofs root=/ {}",
                if args.tag.is_empty() { "root=rootfs".into() } else { format!("root={}", args.tag) }
            )
        },
        "drives": [],
        "machine-config": {
            "vcpu_count": args.vcpus,
            "mem_size_mib": args.memory_mib,
            "smt": false
        },
        "network-interfaces": [],
        "vsock": null,
        "_runner_meta": {
            "api_socket": api_sock,
            "log_path": log_path,
            "vhost_user_device": {
                "id": "rootfs",
                "socket": args.vhost_socket,
                "num_queues": 2,
                "queue_size": 1024
            }
        }
    }))
}

pub fn run_vm(args: &Args) -> anyhow::Result<()> {
    if !args.firecracker.exists() {
        anyhow::bail!(
            "firecracker binary not found at {} — install Firecracker or pass --firecracker",
            args.firecracker.display()
        );
    }
    if !args.kernel.exists() {
        anyhow::bail!("kernel not found at {}", args.kernel.display());
    }

    let api_sock = args.cache_dir.join("firecracker.api.sock");
    let log_path = args.cache_dir.join("firecracker.log");
    fs::create_dir_all(&args.cache_dir)?;
    let _ = fs::remove_file(&api_sock);

    let mut fc = Command::new(&args.firecracker)
        .arg("--api-sock")
        .arg(&api_sock)
        .arg("--log-path")
        .arg(&log_path)
        .arg("--level")
        .arg("Info")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::inherit())
        .spawn()
        .context("spawn firecracker")?;

    std::thread::sleep(std::time::Duration::from_millis(200));

    let boot = json!({
        "kernel_image_path": args.kernel,
        "boot_args": format!(
            "console=ttyS0 reboot=k panic=1 pci=off init=/bin/sh rootfstype=virtiofs root=/ {}",
            if args.tag.is_empty() { "root=rootfs".into() } else { format!("root={}", args.tag) }
        )
    });
    firecracker_put(&api_sock, "/boot-source", &boot)?;

    let machine = json!({
        "vcpu_count": args.vcpus,
        "mem_size_mib": args.memory_mib,
        "smt": false
    });
    firecracker_put(&api_sock, "/machine-config", &machine)?;

    let device = json!({
        "socket": args.vhost_socket,
        "num_queues": 2,
        "queue_size": 1024
    });
    firecracker_put(&api_sock, "/vhost-user-devices/rootfs", &device)?;

    firecracker_put(&api_sock, "/actions", &json!({"action_type": "InstanceStart"}))?;
    let status = fc.wait().context("wait for firecracker")?;
    if !status.success() {
        anyhow::bail!("firecracker exited with {status}");
    }
    Ok(())
}

fn firecracker_put(
    api_sock: &PathBuf,
    path: &str,
    body: &serde_json::Value,
) -> anyhow::Result<()> {
    let output = Command::new("curl")
        .arg("--silent")
        .arg("--show-error")
        .arg("--fail")
        .arg("--unix-socket")
        .arg(api_sock)
        .arg("-X")
        .arg("PUT")
        .arg(format!("http://localhost{path}"))
        .arg("-H")
        .arg("Content-Type: application/json")
        .arg("-d")
        .arg(body.to_string())
        .output()
        .context("curl firecracker api")?;
    if !output.status.success() {
        anyhow::bail!(
            "firecracker API PUT {path} failed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }
    Ok(())
}

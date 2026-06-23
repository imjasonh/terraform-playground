use std::path::Path;
use std::sync::{Arc, Barrier, Mutex};
use std::thread;

use std::fs::File;
use std::os::unix::io::AsRawFd;

use fc_oci_fs::ImageFs;
use fc_vhostfsd::backend::FsBackendHandler;
use fuse_backend_rs::api::server::Server;
use vhost::vhost_user::message::VhostUserHeaderFlag;
use vhost::vhost_user::{Frontend, Listener, VhostUserFrontend};
use vhost::VhostBackend;
use vhost_user_backend::VhostUserDaemon;
use vm_memory::{FileOffset, GuestAddress, GuestMemoryAtomic, GuestMemoryMmap};

mod support;

fn vhost_client(path: &Path, barrier: Arc<Barrier>) {
    barrier.wait();
    let mut frontend = Frontend::connect(path, 1).unwrap();
    frontend.set_hdr_flags(VhostUserHeaderFlag::NEED_REPLY);
    barrier.wait();

    let features = frontend.get_features().unwrap();
    let proto = frontend.get_protocol_features().unwrap();
    frontend.set_features(features).unwrap();
    frontend.set_protocol_features(proto).unwrap();

    assert_eq!(frontend.get_queue_num().unwrap(), 2);
    frontend.set_owner().unwrap();

    let (_cfg, data) = frontend
        .get_config(0, 36, vhost::vhost_user::message::VhostUserConfigFlags::empty(), &[0u8; 36])
        .unwrap();
    assert!(data.starts_with(b"rootfs"));
}

#[test]
fn vhost_user_virtiofs_daemon_handshake() {
    let dir = tempfile::tempdir().unwrap();
    let image = support::build_fixture_image(dir.path());
    let overlay = ImageFs::open_local_layer("fixture", &image, dir.path().join("cache"))
        .unwrap()
        .overlay;
    let server = Arc::new(Server::new(overlay));
    let backend = Arc::new(Mutex::new(
        FsBackendHandler::new(server, "rootfs".to_string()).unwrap(),
    ));
    let memfd = nix::sys::memfd::memfd_create("test", nix::sys::memfd::MFdFlags::empty()).unwrap();
    let file = File::from(memfd);
    file.set_len(0x100000).unwrap();
    let file_offset = FileOffset::new(file, 0);
    let mem = GuestMemoryAtomic::new(
        GuestMemoryMmap::from_ranges_with_files(&[(GuestAddress(0x100000), 0x100000, Some(file_offset))])
            .unwrap(),
    );
    let mut daemon = VhostUserDaemon::new("test".to_string(), backend, mem).unwrap();

    let barrier = Arc::new(Barrier::new(2));
    let socket = dir.path().join("vhost.sock");
    let barrier2 = barrier.clone();
    let socket2 = socket.clone();
    let client = thread::spawn(move || vhost_client(&socket2, barrier2));

    let mut listener = Listener::new(&socket, false).unwrap();
    barrier.wait();
    daemon.start(&mut listener).unwrap();
    barrier.wait();
    client.join().unwrap();
}

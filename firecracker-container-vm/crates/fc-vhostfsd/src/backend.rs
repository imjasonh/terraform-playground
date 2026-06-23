use std::io::Result;
use std::sync::{Arc, Mutex};

use fc_oci_fs::OverlayFs;
use fuse_backend_rs::api::server::Server;
use fuse_backend_rs::transport::{FsCacheReqHandler, Reader, VirtioFsWriter};
use vhost::vhost_user::message::VhostUserProtocolFeatures;
use vhost::vhost_user::Backend;
use vhost_user_backend::{VhostUserBackendMut, VringMutex, VringT};
use virtio_bindings::bindings::virtio_ring::{
    VIRTIO_RING_F_EVENT_IDX, VIRTIO_RING_F_INDIRECT_DESC,
};
use virtio_queue::DescriptorChain;
use virtio_queue::QueueOwnedT;
use vm_memory::{GuestAddressSpace, GuestMemoryAtomic, GuestMemoryLoadGuard, GuestMemoryMmap};
use vmm_sys_util::epoll::EventSet;
use vmm_sys_util::event::{EventConsumer, EventNotifier};

const VIRTIO_F_VERSION_1: u32 = 32;
const QUEUE_SIZE: usize = 1024;
const NUM_QUEUES: usize = 2;
const HIPRIO_QUEUE_EVENT: u16 = 0;
const REQ_QUEUE_EVENT: u16 = 1;

/// virtio-fs config space: 36-byte tag + reserved fields.
fn build_config(tag: &str) -> Vec<u8> {
    let mut cfg = vec![0u8; 128];
    let bytes = tag.as_bytes();
    let n = bytes.len().min(36);
    cfg[..n].copy_from_slice(&bytes[..n]);
    cfg
}

pub struct FsBackend {
    event_idx: bool,
    kill_evt: (EventConsumer, EventNotifier),
    mem: Option<GuestMemoryAtomic<GuestMemoryMmap>>,
    server: Arc<Server<Arc<OverlayFs>>>,
    config: Vec<u8>,
}

impl FsBackend {
    pub fn new(server: Arc<Server<Arc<OverlayFs>>>, tag: String) -> Result<Self> {
        Ok(Self {
            event_idx: false,
            kill_evt: vmm_sys_util::event::new_event_consumer_and_notifier(
                vmm_sys_util::event::EventFlag::NONBLOCK,
            )?,
            mem: None,
            server,
            config: build_config(&tag),
        })
    }

    fn process_queue(&mut self, vring_state: &mut vhost_user_backend::VringState) -> Result<()> {
        let guest_mem = self
            .mem
            .as_ref()
            .ok_or_else(|| std::io::Error::from(std::io::ErrorKind::NotConnected))?;
        let avail_chains: Vec<DescriptorChain<GuestMemoryLoadGuard<GuestMemoryMmap>>> = vring_state
            .get_queue_mut()
            .iter(guest_mem.memory())
            .map_err(|_| std::io::Error::from(std::io::ErrorKind::InvalidData))?
            .collect();

        for chain in avail_chains {
            let head_index = chain.head_index();
            let mem = chain.memory();
            let reader = Reader::from_descriptor_chain(mem, chain.clone())
                .map_err(|_| std::io::Error::from(std::io::ErrorKind::InvalidData))?;
            let writer = VirtioFsWriter::new(mem, chain.clone())
                .map_err(|_| std::io::Error::from(std::io::ErrorKind::InvalidData))?;

            self.server
                .handle_message(
                    reader,
                    fuse_backend_rs::transport::Writer::VirtioFs(writer),
                    None as Option<&mut dyn FsCacheReqHandler>,
                    None,
                )
                .map_err(|e| std::io::Error::other(e))?;

            if self.event_idx {
                if vring_state.add_used(head_index, 0).is_err() {
                    log::warn!("failed to add used descriptor");
                }
                match vring_state.needs_notification() {
                    Err(_) => {
                        vring_state.signal_used_queue().ok();
                    }
                    Ok(true) => {
                        vring_state.signal_used_queue().ok();
                    }
                    Ok(false) => {}
                }
            } else {
                let _ = vring_state.add_used(head_index, 0);
                vring_state.signal_used_queue().ok();
            }
        }
        Ok(())
    }
}

pub struct FsBackendHandler {
    backend: Mutex<FsBackend>,
}

impl FsBackendHandler {
    pub fn new(server: Arc<Server<Arc<OverlayFs>>>, tag: String) -> Result<Self> {
        Ok(Self {
            backend: Mutex::new(FsBackend::new(server, tag)?),
        })
    }
}

impl VhostUserBackendMut for FsBackendHandler {
    type Bitmap = ();
    type Vring = VringMutex;

    fn num_queues(&self) -> usize {
        NUM_QUEUES
    }

    fn max_queue_size(&self) -> usize {
        QUEUE_SIZE
    }

    fn features(&self) -> u64 {
        1 << VIRTIO_F_VERSION_1
            | 1 << VIRTIO_RING_F_INDIRECT_DESC
            | 1 << VIRTIO_RING_F_EVENT_IDX
            | vhost::vhost_user::message::VhostUserVirtioFeatures::PROTOCOL_FEATURES.bits()
    }

    fn protocol_features(&self) -> VhostUserProtocolFeatures {
        VhostUserProtocolFeatures::MQ
            | VhostUserProtocolFeatures::CONFIG
            | VhostUserProtocolFeatures::BACKEND_REQ
    }

    fn set_event_idx(&mut self, _enabled: bool) {
        self.backend.lock().unwrap().event_idx = true;
    }

    fn get_config(&self, offset: u32, size: u32) -> Vec<u8> {
        let backend = self.backend.lock().unwrap();
        let start = offset as usize;
        let end = (start + size as usize).min(backend.config.len());
        backend.config[start..end].to_vec()
    }

    fn update_memory(&mut self, mem: GuestMemoryAtomic<GuestMemoryMmap>) -> Result<()> {
        self.backend.lock().unwrap().mem = Some(mem);
        Ok(())
    }

    fn set_backend_req_fd(&mut self, backend: Backend) {
        let _ = backend;
    }

    fn exit_event(&self, _thread_index: usize) -> Option<(EventConsumer, EventNotifier)> {
        let backend = self.backend.lock().unwrap();
        Some((
            backend.kill_evt.0.try_clone().ok()?,
            backend.kill_evt.1.try_clone().ok()?,
        ))
    }

    fn handle_event(
        &mut self,
        device_event: u16,
        evset: EventSet,
        vrings: &[VringMutex],
        _thread_id: usize,
    ) -> Result<()> {
        if evset != EventSet::IN {
            return Err(std::io::Error::from(std::io::ErrorKind::InvalidInput));
        }
        let mut vring_state = match device_event {
            HIPRIO_QUEUE_EVENT => vrings[0].get_mut(),
            REQ_QUEUE_EVENT => vrings[1].get_mut(),
            _ => return Err(std::io::Error::from(std::io::ErrorKind::InvalidInput)),
        };
        self.backend
            .lock()
            .unwrap()
            .process_queue(&mut vring_state)
    }
}

use std::any::Any;
use std::collections::{BTreeMap, HashMap};
use std::ffi::CStr;
use std::io;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use fuse_backend_rs::abi::fuse_abi::stat64;
use fuse_backend_rs::api::filesystem::{Context, DirEntry, Entry, FileSystem, FsOptions, OpenOptions, SetattrValid, ZeroCopyWriter};
use fuse_backend_rs::api::filesystem::ROOT_ID;
use fuse_backend_rs::api::BackendFileSystem;

use crate::error::Result;
use crate::gz::IndexedLayer;
use crate::tar_index::TarEntry;

const ATTR_TIMEOUT: Duration = Duration::from_secs(3600);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum NodeKind {
    Directory,
    File,
    Symlink,
}

#[derive(Debug, Clone)]
struct Node {
    inode: u64,
    parent: u64,
    name: String,
    kind: NodeKind,
    mode: u32,
    size: u64,
    link_target: Option<Vec<u8>>,
    location: Option<FileLocation>,
    children: Vec<String>,
}

#[derive(Debug, Clone)]
struct FileLocation {
    layer: usize,
    entry: TarEntry,
}

/// Read-only overlay filesystem backed by indexed OCI layers.
pub struct OverlayFs {
    root: u64,
    nodes: Mutex<HashMap<u64, Node>>,
    path_to_inode: Mutex<HashMap<String, u64>>,
    next_inode: AtomicU64,
    layers: Vec<Arc<IndexedLayer>>,
}

impl OverlayFs {
    pub fn from_layers(layers: Vec<Arc<IndexedLayer>>) -> Result<Arc<Self>> {
        let fs = Arc::new(Self {
            root: ROOT_ID,
            nodes: Mutex::new(HashMap::new()),
            path_to_inode: Mutex::new(HashMap::new()),
            next_inode: AtomicU64::new(ROOT_ID + 1),
            layers,
        });
        fs.build_tree()?;
        Ok(fs)
    }

    fn alloc_inode(&self) -> u64 {
        self.next_inode.fetch_add(1, Ordering::Relaxed)
    }

    fn build_tree(&self) -> Result<()> {
        let mut nodes = self.nodes.lock().unwrap();
        let mut path_to_inode = self.path_to_inode.lock().unwrap();
        nodes.insert(
            ROOT_ID,
            Node {
                inode: ROOT_ID,
                parent: ROOT_ID,
                name: String::new(),
                kind: NodeKind::Directory,
                mode: 0o755,
                size: 0,
                link_target: None,
                location: None,
                children: Vec::new(),
            },
        );
        path_to_inode.insert("/".to_string(), ROOT_ID);

        let mut visible: BTreeMap<String, (usize, TarEntry)> = BTreeMap::new();
        let mut whiteouts: std::collections::HashSet<String> = std::collections::HashSet::new();

        for (layer_idx, layer) in self.layers.iter().enumerate() {
            for entry in layer.toc().entries() {
                let name = entry.path.rsplit('/').next().unwrap_or("");
                if name.starts_with(".wh.") {
                    let hidden = entry
                        .path
                        .rsplit_once('/')
                        .map(|(parent, wh)| format!("{}/{}", parent, &wh[4..]))
                        .unwrap_or_else(|| entry.path.trim_start_matches(".wh.").to_string());
                    whiteouts.insert(hidden);
                    continue;
                }
                if entry.entry_type.contains("Directory") {
                    visible.insert(entry.path.clone(), (layer_idx, entry.clone()));
                } else if entry.entry_type.contains("Regular") || entry.entry_type.contains("Link")
                {
                    if !whiteouts.contains(&entry.path) {
                        visible.insert(entry.path.clone(), (layer_idx, entry.clone()));
                    }
                } else if entry.entry_type.contains("Symlink") {
                    if !whiteouts.contains(&entry.path) {
                        visible.insert(entry.path.clone(), (layer_idx, entry.clone()));
                    }
                }
            }
        }

        for (path, (layer_idx, entry)) in visible {
            self.ensure_path(
                &mut nodes,
                &mut path_to_inode,
                &path,
                layer_idx,
                &entry,
            )?;
        }
        Ok(())
    }

    fn ensure_path(
        &self,
        nodes: &mut HashMap<u64, Node>,
        path_to_inode: &mut HashMap<String, u64>,
        path: &str,
        layer_idx: usize,
        entry: &TarEntry,
    ) -> Result<()> {
        if path_to_inode.contains_key(path) {
            return Ok(());
        }
        let parts: Vec<&str> = path.trim_start_matches('/').split('/').collect();
        let mut current_path = String::from("/");
        let mut parent = ROOT_ID;
        for (idx, part) in parts.iter().enumerate() {
            if part.is_empty() {
                continue;
            }
            if current_path != "/" {
                current_path.push('/');
            }
            current_path.push_str(part);
            if let Some(&inode) = path_to_inode.get(&current_path) {
                parent = inode;
                continue;
            }
            let inode = self.alloc_inode();
            let is_last = idx == parts.len() - 1;
            let kind = if is_last {
                if entry.entry_type.contains("Directory") {
                    NodeKind::Directory
                } else if entry.entry_type.contains("Symlink") {
                    NodeKind::Symlink
                } else {
                    NodeKind::File
                }
            } else {
                NodeKind::Directory
            };
            let node = Node {
                inode,
                parent,
                name: part.to_string(),
                kind,
                mode: if is_last { entry.mode } else { 0o755 },
                size: if is_last { entry.size } else { 0 },
                link_target: if is_last {
                    entry.link_target.as_ref().map(|s| s.as_bytes().to_vec())
                } else {
                    None
                },
                location: if is_last && matches!(kind, NodeKind::File) {
                    Some(FileLocation {
                        layer: layer_idx,
                        entry: entry.clone(),
                    })
                } else {
                    None
                },
                children: Vec::new(),
            };
            nodes.get_mut(&parent).unwrap().children.push(part.to_string());
            nodes.insert(inode, node);
            path_to_inode.insert(current_path.clone(), inode);
            parent = inode;
        }
        Ok(())
    }

    fn node_to_entry(&self, node: &Node) -> Entry {
        let mut attr: stat64 = unsafe { std::mem::zeroed() };
        attr.st_ino = node.inode;
        attr.st_mode = match node.kind {
            NodeKind::Directory => libc::S_IFDIR | (node.mode & 0o777),
            NodeKind::File => libc::S_IFREG | (node.mode & 0o777),
            NodeKind::Symlink => libc::S_IFLNK | (node.mode & 0o777),
        };
        attr.st_size = node.size as i64;
        attr.st_nlink = 1;
        Entry {
            inode: node.inode,
            generation: 0,
            attr,
            attr_flags: 0,
            attr_timeout: ATTR_TIMEOUT,
            entry_timeout: ATTR_TIMEOUT,
        }
    }
}

impl FileSystem for OverlayFs {
    type Inode = u64;
    type Handle = u64;

    fn init(&self, capable: FsOptions) -> io::Result<FsOptions> {
        let mut opts = FsOptions::empty();
        if capable.contains(FsOptions::ZERO_MESSAGE_OPEN) {
            opts |= FsOptions::ZERO_MESSAGE_OPEN;
        }
        Ok(opts)
    }

    fn lookup(&self, _ctx: &Context, parent: Self::Inode, name: &CStr) -> io::Result<Entry> {
        let name = name.to_string_lossy();
        let nodes = self.nodes.lock().unwrap();
        let parent_node = nodes
            .get(&parent)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        if !parent_node.children.iter().any(|c| c == name.as_ref()) {
            return Err(io::Error::from_raw_os_error(libc::ENOENT));
        }
        let path = if parent == ROOT_ID {
            format!("/{name}")
        } else {
            let mut parts = vec![];
            let mut cur = parent;
            while cur != ROOT_ID {
                let n = &nodes[&cur];
                parts.push(n.name.clone());
                cur = n.parent;
            }
            parts.reverse();
            format!("/{}/{}", parts.join("/"), name)
        };
        let inode = *self
            .path_to_inode
            .lock()
            .unwrap()
            .get(&path)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        Ok(self.node_to_entry(&nodes[&inode]))
    }

    fn getattr(
        &self,
        _ctx: &Context,
        inode: Self::Inode,
        _handle: Option<Self::Handle>,
    ) -> io::Result<(stat64, Duration)> {
        let nodes = self.nodes.lock().unwrap();
        let node = nodes
            .get(&inode)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        let entry = self.node_to_entry(node);
        Ok((entry.attr, ATTR_TIMEOUT))
    }

    fn read(
        &self,
        _ctx: &Context,
        inode: Self::Inode,
        _handle: Self::Handle,
        w: &mut dyn ZeroCopyWriter,
        size: u32,
        offset: u64,
        _lock_owner: Option<u64>,
        _flags: u32,
    ) -> io::Result<usize> {
        let nodes = self.nodes.lock().unwrap();
        let node = nodes
            .get(&inode)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        let location = node
            .location
            .as_ref()
            .ok_or_else(|| io::Error::from_raw_os_error(libc::EISDIR))?;
        let layer = &self.layers[location.layer];
        let mut buf = vec![0u8; size as usize];
        let n = layer
            .read_file(&location.entry, offset, &mut buf)
            .map_err(|e| io::Error::new(io::ErrorKind::Other, e))?;
        w.write(&buf[..n])?;
        Ok(n)
    }

    fn readdir(
        &self,
        _ctx: &Context,
        inode: Self::Inode,
        _handle: Self::Handle,
        mut size: u32,
        offset: u64,
        add_entry: &mut dyn FnMut(DirEntry) -> io::Result<usize>,
    ) -> io::Result<()> {
        let nodes = self.nodes.lock().unwrap();
        let node = nodes
            .get(&inode)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        if !matches!(node.kind, NodeKind::Directory) {
            return Err(io::Error::from_raw_os_error(libc::ENOTDIR));
        }
        let mut entries: Vec<(u64, String)> = node
            .children
            .iter()
            .filter_map(|child| {
                let path = if inode == ROOT_ID {
                    format!("/{child}")
                } else {
                    let mut parts = vec![];
                    let mut cur = inode;
                    while cur != ROOT_ID {
                        let n = &nodes[&cur];
                        parts.push(n.name.clone());
                        cur = n.parent;
                    }
                    parts.reverse();
                    format!("/{}/{}", parts.join("/"), child)
                };
                let ino = *self.path_to_inode.lock().unwrap().get(&path)?;
                Some((ino, child.clone()))
            })
            .collect();
        entries.sort_by(|a, b| a.1.cmp(&b.1));
        let start = offset.saturating_sub(1) as usize;
        for (idx, (ino, name)) in entries.into_iter().enumerate().skip(start) {
            if size == 0 {
                break;
            }
            let dirent = DirEntry {
                ino,
                offset: (idx + 2) as u64,
                type_: libc::DT_UNKNOWN as u32,
                name: name.as_bytes(),
            };
            let consumed = add_entry(dirent)?;
            size = size.saturating_sub(consumed as u32);
        }
        Ok(())
    }

    fn readlink(&self, _ctx: &Context, inode: Self::Inode) -> io::Result<Vec<u8>> {
        let nodes = self.nodes.lock().unwrap();
        let node = nodes
            .get(&inode)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        node.link_target
            .clone()
            .ok_or_else(|| io::Error::from_raw_os_error(libc::EINVAL))
    }

    fn open(
        &self,
        _ctx: &Context,
        inode: Self::Inode,
        _flags: u32,
        _fuse_flags: u32,
    ) -> io::Result<(Option<Self::Handle>, OpenOptions, Option<u32>)> {
        let nodes = self.nodes.lock().unwrap();
        let node = nodes
            .get(&inode)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        if !matches!(node.kind, NodeKind::File) {
            return Err(io::Error::from_raw_os_error(libc::EISDIR));
        }
        Ok((Some(inode), OpenOptions::empty(), None))
    }

    fn opendir(
        &self,
        _ctx: &Context,
        inode: Self::Inode,
        _flags: u32,
    ) -> io::Result<(Option<Self::Handle>, OpenOptions)> {
        Ok((Some(inode), OpenOptions::empty()))
    }

    fn setattr(
        &self,
        _ctx: &Context,
        _inode: Self::Inode,
        _attr: stat64,
        _handle: Option<Self::Handle>,
        _valid: SetattrValid,
    ) -> io::Result<(stat64, Duration)> {
        Err(io::Error::from_raw_os_error(libc::EROFS))
    }
}

impl BackendFileSystem for OverlayFs {
    fn mount(&self) -> io::Result<(Entry, u64)> {
        let nodes = self.nodes.lock().unwrap();
        let root = nodes
            .get(&ROOT_ID)
            .ok_or_else(|| io::Error::from_raw_os_error(libc::ENOENT))?;
        let max_ino = self.next_inode.load(Ordering::Relaxed);
        Ok((self.node_to_entry(root), max_ino))
    }

    fn as_any(&self) -> &dyn Any {
        self
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gz::{IndexedLayer, LayerSource};
    use crate::registry::LocalBlob;
    use flate2::write::GzEncoder;
    use flate2::Compression;
    use std::ffi::CString;
    use tar::Builder;
    use tempfile::tempdir;

    fn fixture_layer(dir: &std::path::Path) -> std::path::PathBuf {
        let tar_path = dir.join("layer.tar");
        let gz_path = dir.join("layer.tar.gz");
        {
            let tar = std::fs::File::create(&tar_path).unwrap();
            let mut builder = Builder::new(tar);
            for (path, content) in [("bin/sh", b"#!/bin/sh\n".as_slice()), ("etc/motd", b"welcome\n".as_slice())] {
                let mut header = tar::Header::new_gnu();
                header.set_size(content.len() as u64);
                header.set_mode(0o755);
                header.set_cksum();
                builder
                    .append_data(&mut header, path, &content[..])
                    .unwrap();
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

    #[test]
    fn overlay_lookup_and_getattr() {
        let dir = tempdir().unwrap();
        let gz = fixture_layer(dir.path());
        let layer = Arc::new(
            IndexedLayer::open(
                "layer0",
                LayerSource::Local(LocalBlob::open(&gz).unwrap()),
                dir.path().join("cache"),
            )
            .unwrap(),
        );
        let fs = OverlayFs::from_layers(vec![layer]).unwrap();
        let ctx = Context {
            uid: 0,
            gid: 0,
            pid: 1,
        };
        let entry = fs
            .lookup(&ctx, ROOT_ID, &CString::new("etc").unwrap())
            .unwrap();
        assert_eq!(entry.attr.st_mode & libc::S_IFDIR as u32, libc::S_IFDIR as u32);
    }
}

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TarEntry {
    pub path: String,
    pub offset: u64,
    pub size: u64,
    pub mode: u32,
    pub entry_type: String,
    pub link_target: Option<String>,
}

#[derive(Debug, Clone, Default)]
pub struct TarIndex {
    by_path: HashMap<String, TarEntry>,
}

impl TarIndex {
    pub fn from_entries(entries: Vec<TarEntry>) -> Self {
        let mut by_path = HashMap::new();
        for entry in entries {
            by_path.insert(entry.path.clone(), entry);
        }
        Self { by_path }
    }

    pub fn entries(&self) -> impl Iterator<Item = &TarEntry> {
        self.by_path.values()
    }

    pub fn entries_cloned(&self) -> Vec<TarEntry> {
        self.by_path.values().cloned().collect()
    }

    pub fn get(&self, path: &str) -> Option<&TarEntry> {
        self.by_path.get(path)
    }

    pub fn insert(&mut self, entry: TarEntry) {
        self.by_path.insert(entry.path.clone(), entry);
    }
}

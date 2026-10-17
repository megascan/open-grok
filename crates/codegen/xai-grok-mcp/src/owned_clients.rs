//! The session-owned MCP client map and its live inherited view.

use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Weak};

use crate::servers::{McpClient, McpServerName};

#[derive(Clone)]
pub(crate) struct PublishedClient {
    pub client: Arc<McpClient>,
    pub epoch: u64,
}

#[derive(Default)]
pub(crate) struct PublishedPool {
    pub clients: HashMap<McpServerName, PublishedClient>,
    pub disabled: HashMap<McpServerName, HashSet<String>>,
    next_epoch: u64,
}

pub(crate) struct LiveOwnedClients {
    pub id: u64,
    pub state: parking_lot::Mutex<PublishedPool>,
    pub changes: tokio::sync::watch::Sender<u64>,
}

impl LiveOwnedClients {
    fn new() -> Self {
        static NEXT_POOL_ID: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(1);
        Self {
            id: NEXT_POOL_ID.fetch_add(1, std::sync::atomic::Ordering::Relaxed),
            state: parking_lot::Mutex::new(PublishedPool::default()),
            changes: tokio::sync::watch::channel(0).0,
        }
    }

    fn changed(&self) {
        self.changes
            .send_modify(|revision| *revision = revision.wrapping_add(1));
    }
}

pub struct OwnedClients {
    clients: HashMap<McpServerName, Arc<McpClient>>,
    live: Arc<LiveOwnedClients>,
}

impl Default for OwnedClients {
    fn default() -> Self {
        Self::new()
    }
}

impl OwnedClients {
    pub fn new() -> Self {
        Self {
            clients: HashMap::new(),
            live: Arc::new(LiveOwnedClients::new()),
        }
    }

    pub(crate) fn id(&self) -> u64 {
        self.live.id
    }

    pub(crate) fn live(&self) -> Weak<LiveOwnedClients> {
        Arc::downgrade(&self.live)
    }

    pub fn insert(
        &mut self,
        name: McpServerName,
        client: Arc<McpClient>,
    ) -> Option<Arc<McpClient>> {
        // Publication and final admission use the same lock. An admission that
        // already cloned a client is in flight; later admissions see the epoch.
        {
            let mut state = self.live.state.lock();
            state.next_epoch = state.next_epoch.wrapping_add(1);
            let epoch = state.next_epoch;
            state.clients.insert(
                name.clone(),
                PublishedClient {
                    client: client.clone(),
                    epoch,
                },
            );
        }
        self.live.changed();
        let displaced = self.clients.insert(name, client);
        if let Some(old) = &displaced {
            cancel_watcher(old);
        }
        displaced
    }

    pub fn remove(&mut self, name: &str) -> Option<Arc<McpClient>> {
        let removed = self.clients.remove(name);
        if let Some(old) = &removed {
            self.live.state.lock().clients.remove(name);
            self.live.changed();
            cancel_watcher(old);
        }
        removed
    }

    pub fn clear(&mut self) {
        if !self.clients.is_empty() {
            self.live.state.lock().clients.clear();
            self.live.changed();
        }
        for client in self.clients.values() {
            cancel_watcher(client);
        }
        self.clients.clear();
    }

    /// Publish a parent server's disabled-tool mask and invalidate its catalog.
    pub fn set_disabled_tools(&mut self, name: &str, disabled: HashSet<String>) {
        let mut state = self.live.state.lock();
        let old = state.disabled.get(name).cloned().unwrap_or_default();
        if old == disabled {
            return;
        }
        if disabled.is_empty() {
            state.disabled.remove(name);
        } else {
            state.disabled.insert(name.to_owned(), disabled);
        }
        state.next_epoch = state.next_epoch.wrapping_add(1);
        let epoch = state.next_epoch;
        if let Some(client) = state.clients.get_mut(name) {
            client.epoch = epoch;
        }
        drop(state);
        self.live.changed();
    }

    pub(crate) fn current_epoch(&self, name: &str, client: &Arc<McpClient>) -> Option<u64> {
        self.live
            .state
            .lock()
            .clients
            .get(name)
            .filter(|entry| Arc::ptr_eq(&entry.client, client))
            .map(|entry| entry.epoch)
    }
    pub fn get(&self, name: &str) -> Option<&Arc<McpClient>> {
        self.clients.get(name)
    }
    pub fn contains_key(&self, name: &str) -> bool {
        self.clients.contains_key(name)
    }
    pub fn iter(&self) -> impl Iterator<Item = (&McpServerName, &Arc<McpClient>)> {
        self.clients.iter()
    }
    pub fn keys(&self) -> impl Iterator<Item = &McpServerName> {
        self.clients.keys()
    }
    pub fn values(&self) -> impl Iterator<Item = &Arc<McpClient>> {
        self.clients.values()
    }
    pub fn len(&self) -> usize {
        self.clients.len()
    }
    pub fn is_empty(&self) -> bool {
        self.clients.is_empty()
    }
}

fn cancel_watcher(client: &McpClient) {
    client.set_liveness_handle(None);
}

impl Drop for OwnedClients {
    fn drop(&mut self) {
        self.clear();
    }
}

impl FromIterator<(McpServerName, Arc<McpClient>)> for OwnedClients {
    fn from_iter<I: IntoIterator<Item = (McpServerName, Arc<McpClient>)>>(iter: I) -> Self {
        let mut owned = Self::new();
        for (name, client) in iter {
            owned.insert(name, client);
        }
        owned
    }
}

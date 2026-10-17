//! Per-connection transport liveness. A watcher owns one slot and observes
//! one rmcp service; a replacement connection installs its own watcher.

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use tokio::sync::{Mutex, mpsc::UnboundedSender};
use tokio_util::sync::{CancellationToken, DropGuard};

use crate::servers::{
    ClientState, LivenessCheck, McpClient, McpClientEvent, McpServerName, McpService,
};

pub const DEFAULT_POLL_INTERVAL: Duration = Duration::from_millis(500);
pub(crate) type SharedLivenessSlot = Arc<parking_lot::Mutex<Option<TransportLivenessHandle>>>;
static NEXT_WATCHER_ID: AtomicU64 = AtomicU64::new(1);

/// A retiring task may clear only its own handle; a replacement may already
/// have installed a different watcher in this same slot.
fn clear_liveness_slot(slot: &SharedLivenessSlot, watcher_id: u64) -> bool {
    let mut guard = slot.lock();
    if guard
        .as_ref()
        .is_some_and(|handle| handle.watcher_id == watcher_id)
    {
        guard.take();
        true
    } else {
        false
    }
}

pub struct TransportLivenessHandle {
    pub server_name: McpServerName,
    watcher_id: u64,
    service: std::sync::Weak<
        rmcp::service::RunningService<rmcp::service::RoleClient, crate::servers::GrokClientHandler>,
    >,
    _cancel: DropGuard,
}

impl std::fmt::Debug for TransportLivenessHandle {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("TransportLivenessHandle")
            .field("server_name", &self.server_name)
            .field("watcher_id", &self.watcher_id)
            .finish()
    }
}

impl TransportLivenessHandle {
    pub fn server_name(&self) -> &str {
        &self.server_name
    }

    pub(crate) fn watches(&self, service: &McpService) -> bool {
        self.service.ptr_eq(&Arc::downgrade(service))
    }
}

/// Spawn a watcher for exactly `service`. The state/slot are shared without
/// retaining the client itself; cancelling the handle releases the watcher.
pub(crate) fn spawn_transport_liveness(
    server_name: McpServerName,
    client_id: u64,
    state: Arc<Mutex<ClientState>>,
    service: McpService,
    poll_interval: Duration,
    on_event: UnboundedSender<McpClientEvent>,
    liveness_slot: SharedLivenessSlot,
) -> TransportLivenessHandle {
    let token = CancellationToken::new();
    let drop_guard = token.clone().drop_guard();
    let watcher_id = NEXT_WATCHER_ID.fetch_add(1, Ordering::Relaxed);
    let weak_service = Arc::downgrade(&service);
    let server_name_for_task = server_name.clone();
    let state = Arc::downgrade(&state);
    let liveness_slot = Arc::downgrade(&liveness_slot);
    tokio::spawn(async move {
        let mut tick = tokio::time::interval(poll_interval);
        tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            tokio::select! {
                biased;
                _ = token.cancelled() => return,
                _ = tick.tick() => {}
            }
            let Some(state) = state.upgrade() else { return };
            let status = tokio::select! {
                biased;
                _ = token.cancelled() => return,
                status = McpClient::liveness_for_service(&state, &service) => status,
            };
            drop(state);
            match status {
                LivenessCheck::Healthy => continue,
                LivenessCheck::Transient => {
                    if let Some(slot) = liveness_slot.upgrade() {
                        clear_liveness_slot(&slot, watcher_id);
                    }
                    return;
                }
                LivenessCheck::TransportClosed => {
                    if liveness_slot
                        .upgrade()
                        .is_some_and(|slot| clear_liveness_slot(&slot, watcher_id))
                    {
                        let _ = on_event.send(McpClientEvent::TransportClosed {
                            server: server_name_for_task.clone(),
                            client_id,
                        });
                    }
                    return;
                }
            }
        }
    });
    TransportLivenessHandle {
        server_name,
        watcher_id,
        service: weak_service,
        _cancel: drop_guard,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retired_watcher_cannot_clear_replacement_slot() {
        let token = CancellationToken::new();
        let slot: SharedLivenessSlot =
            Arc::new(parking_lot::Mutex::new(Some(TransportLivenessHandle {
                server_name: "test".into(),
                watcher_id: 2,
                service: std::sync::Weak::new(),
                _cancel: token.drop_guard(),
            })));
        assert!(!clear_liveness_slot(&slot, 1));
        assert!(slot.lock().is_some());
        assert!(clear_liveness_slot(&slot, 2));
        assert!(slot.lock().is_none());
    }
}

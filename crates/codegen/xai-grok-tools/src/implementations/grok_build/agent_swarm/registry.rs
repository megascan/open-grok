//! Session-scoped registry of detached `agent_swarm` cohorts.
//!
//! When a user steers during an orchestration wait, the parent tool returns a
//! partial result and the scheduler keeps running here so members survive the
//! tool-call boundary. `swarm_wait` rejoins a registered cohort; the shell
//! drains [`SwarmFinishedNotice`]s as mid-turn system reminders.

use std::{
    collections::HashMap,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};

use parking_lot::Mutex;
use tokio::sync::Notify;
use tokio_util::sync::CancellationToken;

use super::MemberResult;

/// Shared result slots written by the scheduler as members finish.
#[derive(Debug)]
pub(super) struct SharedMemberSlots {
    slots: Mutex<Vec<Option<MemberResult>>>,
}

impl SharedMemberSlots {
    pub(super) fn new(len: usize) -> Arc<Self> {
        Arc::new(Self {
            slots: Mutex::new(vec![None; len]),
        })
    }

    pub(super) fn store(&self, result: MemberResult) {
        let index = result.index as usize;
        let mut slots = self.slots.lock();
        if index < slots.len() {
            slots[index] = Some(result);
        }
    }

    pub(super) fn snapshot(&self) -> Vec<Option<MemberResult>> {
        self.slots.lock().clone()
    }

    pub(super) fn take_complete(self: &Arc<Self>) -> Option<Vec<MemberResult>> {
        let slots = self.slots.lock();
        if slots.iter().any(|slot| slot.is_none()) {
            return None;
        }
        Some(
            slots
                .iter()
                .map(|slot| slot.clone().expect("checked complete"))
                .collect(),
        )
    }
}

/// A swarm whose scheduler outlived the foreground `agent_swarm` tool call.
#[derive(Debug)]
pub struct DetachedSwarm {
    pub swarm_id: String,
    pub description: String,
    pub parent_session_id: String,
    pub expected_members: u32,
    pub(super) slots: Arc<SharedMemberSlots>,
    pub(super) done: Arc<Notify>,
    pub(super) finished: Arc<AtomicBool>,
    pub cancellation: CancellationToken,
}

impl DetachedSwarm {
    pub(super) fn mark_finished(&self) {
        self.finished.store(true, Ordering::SeqCst);
        self.done.notify_waiters();
    }

    pub fn is_finished(&self) -> bool {
        self.finished.load(Ordering::SeqCst)
    }

    pub async fn wait_finished(&self) {
        loop {
            let notified = self.done.notified();
            if self.is_finished() {
                return;
            }
            notified.await;
        }
    }
}

/// Compact notice for the shell turn loop when a detached swarm completes.
#[derive(Debug, Clone)]
pub struct SwarmFinishedNotice {
    pub swarm_id: String,
    pub description: String,
    pub parent_session_id: String,
    pub completed: usize,
    pub failed: usize,
    pub aborted: usize,
}

/// Session resource tracking detached swarms and their finished notices.
#[derive(Debug, Default, Clone)]
pub struct SwarmRegistry {
    inner: Arc<SwarmRegistryInner>,
}

#[derive(Debug, Default)]
struct SwarmRegistryInner {
    swarms: Mutex<HashMap<String, Arc<DetachedSwarm>>>,
    notices: Mutex<Vec<SwarmFinishedNotice>>,
}

impl SwarmRegistry {
    pub fn new() -> Self {
        Self::default()
    }

    pub(super) fn insert(
        &self,
        swarm: DetachedSwarm,
        scope: Option<&crate::notification::BackgroundDeliveryScope>,
    ) -> Arc<DetachedSwarm> {
        let swarm = Arc::new(swarm);
        let mut swarms = self.inner.swarms.lock();
        if scope.is_some_and(crate::notification::BackgroundDeliveryScope::is_session_revoked) {
            swarm.cancellation.cancel();
        }
        swarms.insert(swarm.swarm_id.clone(), Arc::clone(&swarm));
        swarm
    }

    pub fn get(&self, swarm_id: &str) -> Option<Arc<DetachedSwarm>> {
        self.inner.swarms.lock().get(swarm_id).cloned()
    }

    /// Prefer an explicit id; otherwise the sole unfinished swarm for this session.
    pub fn resolve(
        &self,
        swarm_id: Option<&str>,
        parent_session_id: &str,
    ) -> Result<Arc<DetachedSwarm>, String> {
        if let Some(id) = swarm_id.map(str::trim).filter(|id| !id.is_empty()) {
            return self
                .get(id)
                .ok_or_else(|| format!("No detached swarm with id '{id}'"));
        }
        let swarms: Vec<_> = self
            .inner
            .swarms
            .lock()
            .values()
            .filter(|swarm| swarm.parent_session_id == parent_session_id)
            .cloned()
            .collect();
        match swarms.as_slice() {
            [only] => Ok(Arc::clone(only)),
            [] => Err("No detached swarm is running in this session".to_string()),
            many => Err(format!(
                "Multiple detached swarms are running; pass swarm_id. Active: {}",
                many.iter()
                    .map(|swarm| swarm.swarm_id.as_str())
                    .collect::<Vec<_>>()
                    .join(", ")
            )),
        }
    }

    pub(super) fn push_notice(&self, notice: SwarmFinishedNotice) {
        self.inner.notices.lock().push(notice);
    }

    /// Drain finished notices owned by `parent_session_id`.
    pub fn drain_notices(&self, parent_session_id: &str) -> Vec<SwarmFinishedNotice> {
        let mut notices = self.inner.notices.lock();
        let mut kept = Vec::new();
        let mut drained = Vec::new();
        for notice in notices.drain(..) {
            if notice.parent_session_id == parent_session_id {
                drained.push(notice);
            } else {
                kept.push(notice);
            }
        }
        *notices = kept;
        drained
    }

    pub fn remove(&self, swarm_id: &str) -> Option<Arc<DetachedSwarm>> {
        self.inner.swarms.lock().remove(swarm_id)
    }

    /// Stop detached schedulers owned by an ending session.
    pub fn cancel_session(&self, parent_session_id: &str) {
        for swarm in self.inner.swarms.lock().values() {
            if swarm.parent_session_id == parent_session_id {
                swarm.cancellation.cancel();
            }
        }
    }
}

crate::register_resource!("grok_build", "SwarmRegistry", SwarmRegistry);

#[cfg(test)]
mod tests {
    use super::*;

    fn swarm(id: &str) -> DetachedSwarm {
        DetachedSwarm {
            swarm_id: id.into(),
            description: "test".into(),
            parent_session_id: "same-session".into(),
            expected_members: 0,
            slots: SharedMemberSlots::new(0),
            done: Arc::new(Notify::new()),
            finished: Arc::new(AtomicBool::new(false)),
            cancellation: CancellationToken::new(),
        }
    }

    #[test]
    fn late_detached_swarm_is_cancelled_only_for_destroyed_instance() {
        let registry = SwarmRegistry::new();
        let (handle, _rx) = crate::notification::ToolNotificationHandle::bridge_channel();
        let guard = handle.begin_turn_delivery();
        let scope = guard.scope();
        scope.revoke();
        let interactive = registry.insert(swarm("interactive"), Some(&scope));
        assert!(!interactive.cancellation.is_cancelled());
        handle.revoke_session_deliveries();
        registry.cancel_session("same-session");
        assert!(interactive.cancellation.is_cancelled());
        let late = registry.insert(swarm("late"), Some(&scope));
        assert!(late.cancellation.is_cancelled());
        let (resumed, _rx) = crate::notification::ToolNotificationHandle::bridge_channel();
        let resumed_guard = resumed.begin_turn_delivery();
        let fresh = registry.insert(swarm("fresh"), Some(&resumed_guard.scope()));
        assert!(!fresh.cancellation.is_cancelled());
    }
}

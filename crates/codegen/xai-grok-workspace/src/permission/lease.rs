//! Actor-issued authorization for an unrestricted external tool process.
//!
//! Changes and process registration serialize through the same lock. A
//! transition revokes leases and kills registered process groups before it
//! queues the new policy; the child owner remains responsible for reaping.

use std::sync::{Arc, Weak};

use parking_lot::Mutex;
use tokio_util::sync::CancellationToken;
use xai_tty_utils::ProcessGroup;

#[derive(Default)]
struct State {
    revision: u64,
    leases: Vec<Record>,
}

struct Record {
    lease: Weak<LeaseInner>,
    groups: Vec<Weak<ProcessGroup>>,
}

struct LeaseInner {
    revoked: CancellationToken,
}

/// Shared transition gate for a permission actor. Public only because the
/// actor-backed handle exposes its fields; callers cannot issue leases with it.
#[doc(hidden)]
#[derive(Default)]
pub struct PermissionLeaseRegistry {
    state: Mutex<State>,
}

impl PermissionLeaseRegistry {
    /// Serialize a command enqueue with other transitions and lease issuance.
    pub(super) fn enqueue_snapshot<T>(&self, enqueue: impl FnOnce(u64) -> T) -> T {
        let state = self.state.lock();
        enqueue(state.revision)
    }

    pub(super) fn transition<T>(&self, change: impl FnOnce() -> T) -> T {
        let mut state = self.state.lock();
        state.revision = state.revision.wrapping_add(1);
        for record in state.leases.drain(..) {
            if let Some(lease) = record.lease.upgrade() {
                lease.revoked.cancel();
                for group in record.groups {
                    if let Some(group) = group.upgrade() {
                        let _ = group.kill();
                    }
                }
            }
        }
        change()
    }

    pub(super) fn issue(self: &Arc<Self>, revision: u64) -> Option<UnrestrictedPermissionLease> {
        let mut state = self.state.lock();
        if revision != state.revision {
            return None;
        }
        state
            .leases
            .retain(|record| record.lease.strong_count() != 0);
        let inner = Arc::new(LeaseInner {
            revoked: CancellationToken::new(),
        });
        state.leases.push(Record {
            lease: Arc::downgrade(&inner),
            groups: Vec::new(),
        });
        Some(UnrestrictedPermissionLease {
            registry: self.clone(),
            inner,
            revision,
        })
    }
}

/// A permission-actor grant. Shell-level hooks, plan, sandbox and explicit
/// user enablement still need to be checked by the caller.
pub struct UnrestrictedPermissionLease {
    registry: Arc<PermissionLeaseRegistry>,
    inner: Arc<LeaseInner>,
    revision: u64,
}

/// The permission grant was revoked before a process could be registered.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LeaseRevoked;

impl std::fmt::Display for LeaseRevoked {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("unrestricted permission lease revoked")
    }
}

impl std::error::Error for LeaseRevoked {}

impl UnrestrictedPermissionLease {
    pub fn is_revoked(&self) -> bool {
        self.inner.revoked.is_cancelled()
    }

    pub async fn revoked(&self) {
        self.inner.revoked.cancelled().await;
    }

    /// Spawn and enroll synchronously while transitions are excluded. The
    /// returned group must also remain strongly owned by the process client.
    /// Errors from the closure are returned separately from lease revocation.
    pub fn with_active_process<T, E>(
        &self,
        spawn: impl FnOnce() -> Result<(T, Arc<ProcessGroup>), E>,
    ) -> Result<Result<T, E>, LeaseRevoked> {
        let mut state = self.registry.state.lock();
        if self.revision != state.revision || self.inner.revoked.is_cancelled() {
            return Err(LeaseRevoked);
        }
        let Some(record) = state
            .leases
            .iter_mut()
            .find(|record| record.lease.ptr_eq(&Arc::downgrade(&self.inner)))
        else {
            return Err(LeaseRevoked);
        };
        Ok(spawn().map(|(value, group)| {
            record.groups.push(Arc::downgrade(&group));
            value
        }))
    }
}

impl Drop for UnrestrictedPermissionLease {
    fn drop(&mut self) {
        let mut state = self.registry.state.lock();
        if let Some(index) = state
            .leases
            .iter()
            .position(|record| record.lease.ptr_eq(&Arc::downgrade(&self.inner)))
        {
            let record = state.leases.swap_remove(index);
            self.inner.revoked.cancel();
            for group in record.groups {
                if let Some(group) = group.upgrade() {
                    let _ = group.kill();
                }
            }
        }
    }
}

/// Ensure actor shutdown or abort revokes every outstanding lease.
pub(super) struct ActorLeaseGuard(pub Arc<PermissionLeaseRegistry>);

impl Drop for ActorLeaseGuard {
    fn drop(&mut self) {
        self.0.transition(|| ());
    }
}

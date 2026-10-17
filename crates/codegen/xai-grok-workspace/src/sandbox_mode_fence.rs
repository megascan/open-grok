//! Fence external-process authorization against in-process sandbox mode writes.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, OnceLock, Weak};

use parking_lot::Mutex;

type Revoke = dyn Fn() + Send + Sync;

#[derive(Default)]
struct State {
    revision: u64,
    registrations: Vec<Weak<Revoke>>,
}

#[derive(Default)]
struct Fence(Mutex<State>);

fn fence_for(root: &Path) -> Arc<Fence> {
    static FENCES: OnceLock<Mutex<HashMap<PathBuf, Weak<Fence>>>> = OnceLock::new();
    let root = xai_grok_sandbox::command::canonical_path(root);
    let mut fences = FENCES.get_or_init(Default::default).lock();
    fences.retain(|_, fence| fence.strong_count() != 0);
    if let Some(fence) = fences.get(&root).and_then(Weak::upgrade) {
        return fence;
    }
    let fence = Arc::new(Fence::default());
    fences.insert(root, Arc::downgrade(&fence));
    fence
}

/// Keep this registration alive for the session that holds the permission handle.
/// Read a revision before checking the live mode and acquiring an unrestricted
/// lease; reject the lease if `is_current` is false afterwards. Later mode
/// writes revoke that lease before changing the file.
#[derive(Clone)]
pub struct SandboxModeLeaseRegistration {
    fence: Arc<Fence>,
    _revoke: Arc<Revoke>,
}

impl SandboxModeLeaseRegistration {
    pub fn revision(&self) -> u64 {
        self.fence.0.lock().revision
    }

    pub fn is_current(&self, revision: u64) -> bool {
        self.fence.0.lock().revision == revision
    }
}

fn register(root: &Path, revoke: Arc<Revoke>) -> SandboxModeLeaseRegistration {
    let fence = fence_for(root);
    {
        let mut state = fence.0.lock();
        state
            .registrations
            .retain(|entry| entry.strong_count() != 0);
        state.registrations.push(Arc::downgrade(&revoke));
    }
    SandboxModeLeaseRegistration {
        fence,
        _revoke: revoke,
    }
}

pub(crate) fn register_permission(
    root: &Path,
    permission: crate::permission::PermissionHandle,
) -> SandboxModeLeaseRegistration {
    register(
        root,
        Arc::new(move || permission.revoke_unrestricted_leases()),
    )
}

/// Hold the transition closed until the new file is visible. Even a failed
/// write invalidates old leases; this avoids making failure an authorization gap.
pub(crate) fn transition<T>(root: &Path, write: impl FnOnce() -> T) -> T {
    update(root, || true, write)
}

/// Serialize a settings refresh with lease snapshots; revoke only if its
/// sandbox mode changed. The predicate and write run under the same fence.
pub(crate) fn update<T>(
    root: &Path,
    changed: impl FnOnce() -> bool,
    write: impl FnOnce() -> T,
) -> T {
    let fence = fence_for(root);
    let mut state = fence.0.lock();
    if changed() {
        state.revision = state.revision.wrapping_add(1);
        state.registrations.retain(|registration| {
            if let Some(revoke) = registration.upgrade() {
                revoke();
                true
            } else {
                false
            }
        });
    }
    write()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[test]
    fn write_revokes_and_invalidates_snapshots_for_aliases() {
        let dir = tempfile::tempdir().unwrap();
        let count = Arc::new(AtomicUsize::new(0));
        let seen = count.clone();
        let registration = register(
            dir.path(),
            Arc::new(move || {
                seen.fetch_add(1, Ordering::SeqCst);
            }),
        );
        let old = registration.revision();
        transition(&dir.path().join("."), || ());
        assert_eq!(count.load(Ordering::SeqCst), 1);
        assert!(!registration.is_current(old));
        assert!(registration.is_current(registration.revision()));
    }

    #[test]
    fn dropped_registration_is_not_retained() {
        let dir = tempfile::tempdir().unwrap();
        let count = Arc::new(AtomicUsize::new(0));
        let seen = count.clone();
        let registration = register(
            dir.path(),
            Arc::new(move || {
                seen.fetch_add(1, Ordering::SeqCst);
            }),
        );
        drop(registration);
        transition(dir.path(), || ());
        assert_eq!(count.load(Ordering::SeqCst), 0);
    }

    #[test]
    fn snapshot_cannot_complete_during_mode_write() {
        let dir = tempfile::tempdir().unwrap();
        let registration = register(dir.path(), Arc::new(|| {}));
        let old = registration.revision();
        let (entered_tx, entered_rx) = std::sync::mpsc::channel();
        let (release_tx, release_rx) = std::sync::mpsc::channel();
        let root = dir.path().to_owned();
        let writer = std::thread::spawn(move || {
            transition(&root, || {
                entered_tx.send(()).unwrap();
                release_rx.recv().unwrap();
            });
        });
        entered_rx.recv().unwrap();
        let (checked_tx, checked_rx) = std::sync::mpsc::channel();
        let reader = std::thread::spawn(move || {
            checked_tx.send(registration.is_current(old)).unwrap();
        });
        assert!(checked_rx.try_recv().is_err());
        release_tx.send(()).unwrap();
        assert!(!checked_rx.recv().unwrap());
        reader.join().unwrap();
        writer.join().unwrap();
    }
}

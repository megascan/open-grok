//! Tracks open TUI sessions in `~/.opengrok/active_sessions.json` for crash
//! recovery. Clean exit removes the entry; crash leaves it behind. On next
//! launch, [`collect_crashed`] finds orphaned entries (dead PIDs).

use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};

use agent_client_protocol as acp;
use chrono::{DateTime, Utc};
use fs2::FileExt;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ActiveSession {
    pub session_id: acp::SessionId,
    pub pid: u32,
    pub cwd: String,
    pub opened_at: DateTime<Utc>,
}

const DATA_FILENAME: &str = "active_sessions.json";
const LOCK_FILENAME: &str = "active_sessions.lock";
const TMP_FILENAME: &str = "active_sessions.json.tmp";

// -- Public API (delegates to `_in` variants with default grok home) --------

/// Register a session as active (idempotent by session_id).
pub fn register(session: ActiveSession) -> io::Result<()> {
    register_in(&crate::util::grok_home::grok_home(), session)
}

/// Register and return the saved corrupt-registry path so callers can warn users.
pub fn register_reporting(session: ActiveSession) -> io::Result<Option<PathBuf>> {
    register_reporting_in(&crate::util::grok_home::grok_home(), session)
}

/// Non-blocking unregister for signal handlers. Returns `Ok(false)` on
/// lock contention; the orphan is cleaned up by `collect_crashed` next launch.
pub fn try_unregister(session_id: &acp::SessionId) -> io::Result<bool> {
    try_unregister_in(&crate::util::grok_home::grok_home(), session_id)
}

/// Remove entries with dead PIDs and return them.
pub fn collect_crashed() -> io::Result<Vec<ActiveSession>> {
    collect_crashed_in(&crate::util::grok_home::grok_home())
}

// -- Injectable-root variants (`_in`) for testing ---------------------------

pub fn register_in(root: &Path, session: ActiveSession) -> io::Result<()> {
    register_reporting_in(root, session).map(|_| ())
}

pub fn register_reporting_in(root: &Path, session: ActiveSession) -> io::Result<Option<PathBuf>> {
    with_locked_state_report(root, |sessions| {
        sessions.retain(|s| s.session_id != session.session_id);
        sessions.push(session);
    })
    .map(|(_, backup)| backup)
}

pub fn unregister_in(root: &Path, session_id: &acp::SessionId) -> io::Result<()> {
    with_locked_state(root, |sessions| {
        sessions.retain(|s| s.session_id != *session_id);
    })
}

pub fn try_unregister_in(root: &Path, session_id: &acp::SessionId) -> io::Result<bool> {
    try_with_locked_state(root, |sessions| {
        sessions.retain(|s| s.session_id != *session_id);
    })
    .map(|opt| opt.is_some())
}

pub fn collect_crashed_in(root: &Path) -> io::Result<Vec<ActiveSession>> {
    with_locked_state(root, |sessions| {
        let (alive, dead): (Vec<_>, Vec<_>) = sessions.drain(..).partition(|s| is_pid_alive(s.pid));
        *sessions = alive;
        dead
    })
}

pub fn list_in(root: &Path) -> io::Result<Vec<ActiveSession>> {
    let data_path = root.join(DATA_FILENAME);
    read_data_file(&data_path, false).map(|(sessions, _)| sessions)
}

// -- Internal: locked read-modify-write -------------------------------------

fn with_locked_state<F, R>(root: &Path, mutate: F) -> io::Result<R>
where
    F: FnOnce(&mut Vec<ActiveSession>) -> R,
{
    with_locked_state_report(root, mutate).map(|(result, _)| result)
}

fn with_locked_state_report<F, R>(root: &Path, mutate: F) -> io::Result<(R, Option<PathBuf>)>
where
    F: FnOnce(&mut Vec<ActiveSession>) -> R,
{
    let lock_path = root.join(LOCK_FILENAME);
    let data_path = root.join(DATA_FILENAME);
    let tmp_path = root.join(TMP_FILENAME);

    fs::create_dir_all(root)?;
    let lock_file = open_lock_file(&lock_path)?;
    lock_file.lock_exclusive()?;

    let result = locked_mutate(&data_path, &tmp_path, mutate);

    let _ = lock_file.unlock();
    result
}

/// Non-blocking variant for signal handlers.
fn try_with_locked_state<F, R>(root: &Path, mutate: F) -> io::Result<Option<R>>
where
    F: FnOnce(&mut Vec<ActiveSession>) -> R,
{
    let lock_path = root.join(LOCK_FILENAME);
    let data_path = root.join(DATA_FILENAME);
    let tmp_path = root.join(TMP_FILENAME);

    fs::create_dir_all(root)?;
    let lock_file = open_lock_file(&lock_path)?;

    match lock_file.try_lock_exclusive() {
        Ok(()) => {
            let result = locked_mutate(&data_path, &tmp_path, mutate);
            let _ = lock_file.unlock();
            result.map(|(value, _)| Some(value))
        }
        Err(e) if e.kind() == io::ErrorKind::WouldBlock => Ok(None),
        Err(e) => Err(e),
    }
}

fn locked_mutate<F, R>(
    data_path: &Path,
    tmp_path: &Path,
    mutate: F,
) -> io::Result<(R, Option<PathBuf>)>
where
    F: FnOnce(&mut Vec<ActiveSession>) -> R,
{
    let (mut sessions, backup) = read_data_file(data_path, true)?;
    let result = mutate(&mut sessions);
    write_data_file_atomic(tmp_path, data_path, &sessions)?;
    Ok((result, backup))
}

fn open_lock_file(path: &Path) -> io::Result<File> {
    OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(path)
}

fn read_data_file(
    path: &Path,
    backup_corrupt: bool,
) -> io::Result<(Vec<ActiveSession>, Option<PathBuf>)> {
    match fs::read(path) {
        Ok(bytes) => match serde_json::from_slice::<Vec<ActiveSession>>(&bytes) {
            Ok(sessions) => Ok((sessions, None)),
            Err(e) => {
                let backup = if backup_corrupt {
                    Some(backup_corrupt_file(path, &bytes)?)
                } else {
                    None
                };
                let sessions = salvage_sessions(&bytes);
                tracing::warn!(
                    path = %path.display(),
                    error = %e,
                    backup = ?backup,
                    recovered = sessions.len(),
                    "active_sessions.json is corrupted; salvaged readable sessions"
                );
                Ok((sessions, backup))
            }
        },
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok((Vec::new(), None)),
        Err(e) => Err(e),
    }
}

/// Recover complete JSON objects even when another row or the array tail is damaged.
/// String escapes and nested objects must not be mistaken for row boundaries.
fn salvage_sessions(bytes: &[u8]) -> Vec<ActiveSession> {
    let mut sessions = Vec::new();
    let mut start = None;
    let mut depth = 0usize;
    let mut in_string = false;
    let mut escaped = false;
    for (i, &byte) in bytes.iter().enumerate() {
        if in_string {
            if escaped {
                escaped = false;
            } else if byte == b'\\' {
                escaped = true;
            } else if byte == b'"' {
                in_string = false;
            }
            continue;
        }
        match byte {
            b'"' if start.is_some() => in_string = true,
            b'{' => {
                if depth == 0 {
                    start = Some(i);
                }
                depth += 1;
            }
            b'}' if depth > 0 => {
                depth -= 1;
                if depth == 0 {
                    if let Some(begin) = start.take()
                        && let Some(row) = bytes.get(begin..=i)
                        && let Ok(session) = serde_json::from_slice::<ActiveSession>(row)
                    {
                        sessions.push(session);
                    }
                }
            }
            _ => {}
        }
    }
    sessions
}

fn backup_corrupt_file(path: &Path, bytes: &[u8]) -> io::Result<std::path::PathBuf> {
    for suffix in 0u64.. {
        let mut name = path.as_os_str().to_os_string();
        name.push(if suffix == 0 {
            ".corrupt".to_string()
        } else {
            format!(".corrupt.{suffix}")
        });
        let backup = std::path::PathBuf::from(name);
        match OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&backup)
        {
            Ok(mut file) => {
                if let Err(error) = file.write_all(bytes).and_then(|_| file.sync_all()) {
                    let _ = fs::remove_file(&backup);
                    return Err(error);
                }
                return Ok(backup);
            }
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => continue,
            Err(error) => return Err(error),
        }
    }
    unreachable!()
}

fn write_data_file_atomic(
    tmp_path: &Path,
    data_path: &Path,
    sessions: &[ActiveSession],
) -> io::Result<()> {
    let json = serde_json::to_string_pretty(sessions)
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    fs::write(tmp_path, json.as_bytes())?;
    fs::rename(tmp_path, data_path).inspect_err(|_| {
        let _ = fs::remove_file(tmp_path);
    })
}

/// PID liveness probe (also used by the session bus presence GC).
pub(crate) fn is_pid_alive(pid: u32) -> bool {
    #[cfg(unix)]
    {
        let pid_i = match i32::try_from(pid) {
            Ok(p) if p > 0 => p,
            _ => return false,
        };
        let ret = unsafe { libc::kill(pid_i as libc::pid_t, 0) };
        if ret == 0 {
            return true;
        }
        io::Error::last_os_error().raw_os_error() == Some(libc::EPERM)
    }
    #[cfg(windows)]
    {
        use windows::Win32::Foundation::CloseHandle;
        use windows::Win32::System::Threading::{OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION};
        let handle = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid) };
        match handle {
            Ok(h) => {
                let _ = unsafe { CloseHandle(h) };
                true
            }
            Err(_) => false,
        }
    }
    #[cfg(not(any(unix, windows)))]
    {
        // Conservative: assume alive if we can't check.
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn make_session(id: &str, pid: u32) -> ActiveSession {
        ActiveSession {
            session_id: acp::SessionId::new(id),
            pid,
            cwd: "/tmp/test".into(),
            opened_at: Utc::now(),
        }
    }

    #[test]
    fn register_is_idempotent() {
        let dir = TempDir::new().unwrap();
        let s = make_session("s1", std::process::id());
        register_in(dir.path(), s.clone()).unwrap();
        register_in(dir.path(), s).unwrap();
        assert_eq!(list_in(dir.path()).unwrap().len(), 1);
    }

    #[test]
    fn collect_crashed_partitions_by_pid_liveness() {
        let dir = TempDir::new().unwrap();
        register_in(dir.path(), make_session("alive", std::process::id())).unwrap();
        register_in(dir.path(), make_session("dead", 2_000_000_000)).unwrap();

        let crashed = collect_crashed_in(dir.path()).unwrap();
        assert_eq!(crashed.len(), 1);
        assert_eq!(&*crashed[0].session_id.0, "dead");
        assert_eq!(&*list_in(dir.path()).unwrap()[0].session_id.0, "alive");
    }

    #[test]
    fn concurrent_registers_no_corruption() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().to_path_buf();
        std::thread::scope(|s| {
            for i in 0..10 {
                let p = path.clone();
                s.spawn(move || {
                    register_in(&p, make_session(&format!("s{i}"), std::process::id())).unwrap()
                });
            }
        });
        assert_eq!(list_in(dir.path()).unwrap().len(), 10);
    }

    #[test]
    fn try_unregister_skips_if_locked() {
        let dir = TempDir::new().unwrap();
        let s = make_session("s1", std::process::id());
        register_in(dir.path(), s.clone()).unwrap();

        let lock_file = open_lock_file(&dir.path().join(LOCK_FILENAME)).unwrap();
        lock_file.lock_exclusive().unwrap();
        assert!(!try_unregister_in(dir.path(), &s.session_id).unwrap());
        lock_file.unlock().unwrap();
        assert_eq!(list_in(dir.path()).unwrap().len(), 1);
    }

    #[test]
    fn corrupt_registry_salvages_complete_rows_and_preserves_each_backup() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join(DATA_FILENAME);
        let first = make_session("alive", std::process::id());
        let row = serde_json::to_string(&first).unwrap();
        let corrupt = format!("[{row}, {{\"pid\": \"bad\"}}, {{\"session_id\":");
        fs::write(&path, &corrupt).unwrap();

        assert_eq!(list_in(dir.path()).unwrap(), vec![first.clone()]);
        let backup =
            register_reporting_in(dir.path(), make_session("new", std::process::id())).unwrap();
        assert_eq!(
            backup.as_deref(),
            Some(dir.path().join("active_sessions.json.corrupt").as_path())
        );
        assert_eq!(
            fs::read_to_string(dir.path().join("active_sessions.json.corrupt")).unwrap(),
            corrupt
        );
        let sessions = list_in(dir.path()).unwrap();
        assert_eq!(sessions.len(), 2);
        assert!(sessions.contains(&first));
        assert!(
            register_reporting_in(dir.path(), make_session("new", std::process::id()))
                .unwrap()
                .is_none()
        );

        let next_corrupt = b"[invalid";
        fs::write(&path, next_corrupt).unwrap();
        register_in(dir.path(), make_session("last", std::process::id())).unwrap();
        assert_eq!(
            fs::read(dir.path().join("active_sessions.json.corrupt.1")).unwrap(),
            next_corrupt
        );
        assert_eq!(
            fs::read_to_string(dir.path().join("active_sessions.json.corrupt")).unwrap(),
            corrupt
        );
    }

    #[test]
    fn empty_existing_registry_is_backed_up_before_registration() {
        let dir = TempDir::new().unwrap();
        fs::write(dir.path().join(DATA_FILENAME), b"").unwrap();
        register_in(dir.path(), make_session("new", std::process::id())).unwrap();
        assert_eq!(
            fs::read(dir.path().join("active_sessions.json.corrupt")).unwrap(),
            b""
        );
        assert_eq!(list_in(dir.path()).unwrap().len(), 1);
    }

    #[test]
    fn corrupt_file_recovers() {
        let dir = TempDir::new().unwrap();
        fs::write(dir.path().join(DATA_FILENAME), "garbage{{{").unwrap();
        assert!(list_in(dir.path()).unwrap().is_empty());
        register_in(dir.path(), make_session("s1", std::process::id())).unwrap();
        assert_eq!(list_in(dir.path()).unwrap().len(), 1);
    }
}

//! Atomic gzip storage for large, settled thread histories.
//!
//! The canonical paths remain stable. All readers detect gzip by magic. Active
//! appends restore plain JSONL while holding the same lock as snapshot writers.
use std::fs::{File, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};

use fs2::FileExt;

pub(super) const MIN_GZIP_BYTES: usize = 64 * 1024;

pub(super) fn is_history(path: &Path) -> bool {
    matches!(
        path.file_name().and_then(|name| name.to_str()),
        Some(super::CHAT_HISTORY_FILE | super::UPDATES_FILE)
    )
}

pub(super) fn lock_history(path: &Path) -> io::Result<Option<File>> {
    if !is_history(path) {
        return Ok(None);
    }
    let lock = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(path.with_extension("jsonl.lock"))?;
    lock.lock_exclusive()?;
    Ok(Some(lock))
}

pub(super) fn encode_snapshot(path: &Path, bytes: Vec<u8>) -> io::Result<Vec<u8>> {
    if !is_history(path) || bytes.len() < MIN_GZIP_BYTES {
        return Ok(bytes);
    }
    let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::fast());
    encoder.write_all(&bytes)?;
    let encoded = encoder.finish()?;
    Ok(if encoded.len() < bytes.len() {
        encoded
    } else {
        bytes
    })
}

struct Staging(PathBuf);
impl Drop for Staging {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

/// Caller holds the append/snapshot lock. Failure before rename leaves the
/// original representation intact, including corrupt gzip evidence.
pub(super) fn ensure_plain_locked(path: &Path) -> io::Result<()> {
    if !is_history(path) {
        return Ok(());
    }
    let reader = match xai_file_utils::session_file::open(path) {
        Ok(reader) => reader,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
    };
    if !reader.is_compressed() {
        return Ok(());
    }
    drop(reader);
    transform_locked(path, false)
}

pub(super) fn compress_history(path: &Path) -> io::Result<()> {
    let _lock = lock_history(path)?;
    let metadata = match std::fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
    };
    if !metadata.file_type().is_file() || metadata.len() < MIN_GZIP_BYTES as u64 {
        return Ok(());
    }
    if xai_file_utils::session_file::open(path)?.is_compressed() {
        return Ok(());
    }
    transform_locked(path, true)
}

fn transform_locked(path: &Path, compress: bool) -> io::Result<()> {
    let metadata = std::fs::symlink_metadata(path)?;
    if !metadata.file_type().is_file() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "history must be a regular file",
        ));
    }
    let temp = Staging(super::temp_sibling(path));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut target = options.open(&temp.0)?;
    if compress {
        let mut source = File::open(path)?;
        let mut encoder = flate2::write::GzEncoder::new(&mut target, flate2::Compression::fast());
        io::copy(&mut source, &mut encoder)?;
        encoder.finish()?;
        if target.metadata()?.len() >= metadata.len() {
            return Ok(());
        }
    } else {
        let mut source = xai_file_utils::session_file::open(path)?;
        io::copy(&mut source, &mut target)?;
    }
    target.set_permissions(metadata.permissions())?;
    super::sync_file_durable(&target)?;
    drop(target);
    std::fs::rename(&temp.0, path)?;
    super::sync_parent_dir_durable(path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Read;

    #[test]
    fn gzip_storage_roundtrip_preserves_bytes_permissions_and_repeated_compression() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("updates.jsonl");
        let content = "{\"text\":\"hello repeated world\"}\n".repeat(10_000);
        std::fs::write(&path, &content).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600)).unwrap();
        }
        compress_history(&path).unwrap();
        let first = std::fs::read(&path).unwrap();
        assert_eq!(&first[..2], &[0x1f, 0x8b]);
        assert!(first.len() < content.len() / 10);
        compress_history(&path).unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), first);
        assert_eq!(
            xai_file_utils::session_file::read_to_string(&path).unwrap(),
            content
        );
        let _lock = lock_history(&path).unwrap();
        ensure_plain_locked(&path).unwrap();
        assert_eq!(std::fs::read_to_string(&path).unwrap(), content);
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
    }

    #[test]
    fn gzip_storage_corruption_does_not_replace_original() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("chat_history.jsonl");
        let mut bytes = encode_snapshot(&path, vec![b'a'; MIN_GZIP_BYTES]).unwrap();
        bytes.truncate(bytes.len() - 1);
        std::fs::write(&path, &bytes).unwrap();
        let _lock = lock_history(&path).unwrap();
        assert!(ensure_plain_locked(&path).is_err());
        assert_eq!(std::fs::read(&path).unwrap(), bytes);
        assert!(
            !std::fs::read_dir(dir.path()).unwrap().any(|e| e
                .unwrap()
                .path()
                .extension()
                .is_some_and(|x| x == "tmp"))
        );
    }

    #[test]
    fn gzip_storage_pinned_reader_survives_representation_change() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("updates.jsonl");
        let bytes = vec![b'a'; MIN_GZIP_BYTES];
        std::fs::write(&path, &bytes).unwrap();
        let mut reader = xai_file_utils::session_file::open(&path).unwrap();
        compress_history(&path).unwrap();
        let mut actual = Vec::new();
        reader.read_to_end(&mut actual).unwrap();
        assert_eq!(actual, bytes);
        assert_eq!(
            encode_snapshot(&path, b"small\n".to_vec()).unwrap(),
            b"small\n"
        );
        let summary = dir.path().join("summary.json");
        assert_eq!(encode_snapshot(&summary, bytes.clone()).unwrap(), bytes);
    }
}

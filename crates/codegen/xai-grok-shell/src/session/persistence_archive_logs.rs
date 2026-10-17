//! Log files in the per-turn session archive.
//!
//! The archive copy is rebuilt in memory at every turn end, and logs grow without bound, so they are bounded twice:
//! each log keeps at most [`MAX_ARCHIVED_LOG_BYTES`] (both ends around a marker, so "full output at" pointers still
//! lead somewhere after a restore), and `terminal/` logs are admitted newest first until
//! [`MAX_ARCHIVED_TERMINAL_BYTES`] is spent.

use std::fs::File;
use std::io::{self, Read, Seek, SeekFrom};
use std::path::Path;

use super::CopiedSessionFile;

/// Session subdirectory holding each command's complete output.
pub(super) const TERMINAL_DIR: &str = "terminal";

/// Largest log kept whole in the per-turn session archive; a larger log keeps its first and last half of this.
pub(super) const MAX_ARCHIVED_LOG_BYTES: u64 = 1024 * 1024;

/// Total `terminal/` log bytes in one archive copy.
pub(super) const MAX_ARCHIVED_TERMINAL_BYTES: u64 = 16 * 1024 * 1024;

pub(super) const TRIM_MARKER: &[u8] = b"\n[... trimmed for the session archive ...]\n";

/// Export core histories as plain JSONL even when their on-disk snapshots are gzip.
/// Decode the already vetted file handle, preserving the archive's artifact and
/// symlink checks. Other files (including binary attachments) retain their bytes.
pub(super) fn read_session_file_for_archive(mut file: File, path: &Path) -> io::Result<Vec<u8>> {
    let mut data = Vec::new();
    if path
        .file_name()
        .is_some_and(|name| name == "chat_history.jsonl" || name == "updates.jsonl")
    {
        xai_file_utils::session_file::from_file(file)?.read_to_end(&mut data)?;
    } else {
        file.read_to_end(&mut data)?;
    }
    Ok(data)
}

/// Reads a log whole when it fits in [`MAX_ARCHIVED_LOG_BYTES`], otherwise its first and last halves around [`TRIM_MARKER`].
pub(super) fn read_log_for_archive(mut file: File) -> io::Result<Vec<u8>> {
    let len = file.metadata()?.len();
    let mut data = Vec::with_capacity(len.min(MAX_ARCHIVED_LOG_BYTES) as usize + TRIM_MARKER.len());
    if len <= MAX_ARCHIVED_LOG_BYTES {
        // A log that is still being written can grow past `len` before this read.
        file.take(MAX_ARCHIVED_LOG_BYTES).read_to_end(&mut data)?;
        return Ok(data);
    }
    let half = MAX_ARCHIVED_LOG_BYTES / 2;
    (&mut file).take(half).read_to_end(&mut data)?;
    data.extend_from_slice(TRIM_MARKER);
    file.seek(SeekFrom::End(-(half as i64)))?;
    file.take(half).read_to_end(&mut data)?;
    Ok(data)
}

/// Adds `terminal/` logs, newest first, until [`MAX_ARCHIVED_TERMINAL_BYTES`] is spent: a long session keeps thousands.
pub(super) fn collect_terminal_logs(
    base: &Path,
    files: &mut Vec<CopiedSessionFile>,
    artifacts: &xai_grok_feedback::FeedbackDraftArtifactSet,
) {
    let dir = base.join(TERMINAL_DIR);
    // `read_dir` follows a symlinked `terminal/`, which would upload its target's files; the session walker skips symlinks too.
    if !std::fs::symlink_metadata(&dir).is_ok_and(|m| m.is_dir()) {
        return;
    }
    let Ok(entries) = std::fs::read_dir(&dir) else {
        return;
    };
    let mut logs: Vec<_> = entries
        .flatten()
        .filter_map(|entry| {
            if !entry.file_type().ok()?.is_file() {
                return None;
            }
            let meta = entry.metadata().ok()?;
            Some((meta.modified().ok(), meta.len(), entry.path()))
        })
        .collect();
    logs.sort_unstable_by_key(|(modified, _, _)| std::cmp::Reverse(*modified));

    let mut budget = MAX_ARCHIVED_TERMINAL_BYTES;
    let mut left_out = 0_usize;
    for (_, len, path) in logs {
        let Some(left) = budget.checked_sub(len.min(MAX_ARCHIVED_LOG_BYTES)) else {
            left_out += 1;
            continue;
        };
        let Some(name) = path.strip_prefix(base).ok().and_then(Path::to_str) else {
            continue;
        };
        let Ok(Some(file)) = artifacts.open_non_artifact(&path) else {
            continue;
        };
        match read_log_for_archive(file) {
            Ok(data) => {
                budget = left;
                files.push(CopiedSessionFile {
                    name: name.to_owned(),
                    data,
                });
            }
            Err(e) => tracing::warn!(?e, "Failed to read terminal log during session copy"),
        }
    }
    if left_out > 0 {
        tracing::debug!(
            left_out,
            "session archive: left out older terminal logs over the per-copy budget"
        );
    }
}

#[cfg(test)]
mod gzip_history_tests {
    use super::*;
    use std::io::Write;

    fn gzip(bytes: &[u8]) -> Vec<u8> {
        let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
        encoder.write_all(bytes).unwrap();
        encoder.finish().unwrap()
    }

    #[test]
    fn archives_decode_histories_but_preserve_binary_attachments() {
        let dir = tempfile::tempdir().unwrap();
        let history = b"{\"type\":\"user\",\"content\":\"hello\"}\n";
        let compressed = gzip(history);
        for name in ["chat_history.jsonl", "updates.jsonl", "attachment.gz"] {
            std::fs::write(dir.path().join(name), &compressed).unwrap();
        }
        let child = dir.path().join("subagents/child");
        std::fs::create_dir_all(&child).unwrap();
        std::fs::write(child.join("chat_history.jsonl"), &compressed).unwrap();

        let mut files = Vec::new();
        super::super::collect_session_files_recursive(dir.path(), dir.path(), &mut files).unwrap();
        assert_eq!(files.len(), 4);
        for file in files {
            let expected: &[u8] = if file.name == "attachment.gz" {
                &compressed
            } else {
                history
            };
            assert_eq!(file.data, expected, "{}", file.name);
        }
    }

    #[test]
    fn archive_history_decode_rejects_truncated_gzip() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("chat_history.jsonl");
        let compressed = gzip(b"{}\n");
        std::fs::write(&path, &compressed[..compressed.len() - 4]).unwrap();
        let mut files = Vec::new();
        assert!(
            super::super::collect_session_files_recursive(dir.path(), dir.path(), &mut files)
                .is_err()
        );
        assert!(read_session_file_for_archive(File::open(&path).unwrap(), &path).is_err());
    }

    #[test]
    fn archive_history_keeps_legacy_plain_jsonl() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("updates.jsonl");
        std::fs::write(&path, b"{}\n").unwrap();
        assert_eq!(
            read_session_file_for_archive(File::open(&path).unwrap(), &path).unwrap(),
            b"{}\n"
        );
    }
}

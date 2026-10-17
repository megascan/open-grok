//! Session fork/copy for the JSONL adapter.
//!
//! The `updates.jsonl` transcript is unbounded, so the copy streams it line by
//! line: peak memory tracks a single capped line, plus one small per-line
//! record when a prompt cut is requested. Chat history stays materialized: its
//! transforms need random access and the compacted history is bounded by the
//! context window.

use std::collections::BTreeSet;
use std::io::{self, BufRead, BufReader, BufWriter, Read, Seek, Write};
use std::ops::ControlFlow;
use std::path::Path;

use agent_client_protocol as acp;

use crate::sampling::{
    ConversationItem, conversation_truncate_for_prompt, fork_filter_chat,
    transform_conversation_cwd,
};
use crate::session::info::Info;
use crate::session::persistence::{CHAT_FORMAT_VERSION, Summary};
use crate::session::storage::jsonl::{
    JsonlStorageAdapter, SessionDirMode, transform_session_id_in_update,
};
use crate::session::storage::{
    CopySessionOptions, CopySessionResult, RewindStep, SessionUpdate, SessionUpdateEnvelope,
    filter_rewind_by, rewind_step_for_line, truncate_for_prompt_by,
};

#[cfg(test)]
#[path = "copy_tests.rs"]
mod tests;

fn is_orchestration_projection_update(update: &SessionUpdate) -> bool {
    matches!(
        update,
        SessionUpdate::Xai(notification)
            if matches!(
                &notification.update,
                crate::extensions::notification::SessionUpdate::WorkflowUpdated { .. }
                    | crate::extensions::notification::SessionUpdate::GoalUpdated { .. }
            )
    )
}

/// Updates written plus the `compaction_checkpoints/{uuid}.json` files the
/// surviving records reference, collected in the same pass.
#[derive(Default)]
struct CopiedUpdates {
    count: usize,
    checkpoint_files: BTreeSet<String>,
}

/// Longest `updates.jsonl` line the copy will buffer; anything past it is
/// corruption (e.g. a tail that lost its newlines) and is discarded without
/// being buffered. Discarded lines consume no index in either pass, unlike
/// torn lines, which classify as [`RewindStep::Other`] and end a user run.
const MAX_UPDATE_LINE_BYTES: usize = 64 * 1024 * 1024;

/// [`for_each_jsonl_line_capped`] with the production cap.
fn for_each_jsonl_line<R: BufRead>(
    reader: R,
    f: impl FnMut(usize, &[u8]) -> io::Result<ControlFlow<()>>,
) -> io::Result<()> {
    for_each_jsonl_line_capped(reader, MAX_UPDATE_LINE_BYTES, f)
}

/// Invoke `f` with the index and bytes of each non-empty line, reusing one
/// capped line buffer. Lines over `cap` content bytes are discarded without
/// being buffered whole and consume no index. `f` returns `Break` to stop
/// early. Raw bytes rather than the typed `UpdatesIterator`: classification
/// must tolerate non-UTF-8 lines, and both copy passes need identical line
/// indexes.
fn for_each_jsonl_line_capped<R: BufRead>(
    mut reader: R,
    cap: usize,
    mut f: impl FnMut(usize, &[u8]) -> io::Result<ControlFlow<()>>,
) -> io::Result<()> {
    let mut buf = Vec::new();
    let mut index = 0;
    let mut discarded = 0usize;
    let result = loop {
        buf.clear();
        let n = reader
            .by_ref()
            .take(cap as u64 + 1)
            .read_until(b'\n', &mut buf)?;
        if n == 0 {
            break Ok(());
        }
        if buf.len() > cap && buf.last() != Some(&b'\n') {
            discarded += 1;
            if discarded == 1 {
                tracing::warn!(
                    max_bytes = cap,
                    "discarding over-long updates.jsonl line during fork copy"
                );
            }
            // Drain the remainder of the line without retaining it.
            loop {
                buf.clear();
                let n = reader
                    .by_ref()
                    .take(cap as u64)
                    .read_until(b'\n', &mut buf)?;
                if n == 0 || buf.last() == Some(&b'\n') {
                    break;
                }
            }
            continue;
        }
        let line = buf.trim_ascii();
        if line.is_empty() {
            continue;
        }
        if f(index, line)?.is_break() {
            break Ok(());
        }
        index += 1;
    };
    if discarded > 1 {
        tracing::warn!(
            discarded,
            max_bytes = cap,
            "discarded over-long updates.jsonl lines during fork copy"
        );
    }
    result
}

/// Indexes (in non-empty-line order) of the source lines that survive rewind
/// filtering and the `target_prompt_index` cut, holding one classification per
/// line instead of the lines. As in replay, an unparseable line classifies as
/// [`RewindStep::Other`] (ending a user run) and is skipped later at parse.
fn surviving_line_indexes<R: BufRead>(
    reader: R,
    target_prompt_index: usize,
) -> io::Result<Vec<usize>> {
    struct LineRecord {
        index: usize,
        step: RewindStep,
    }
    let mut records = Vec::new();
    for_each_jsonl_line(reader, |index, line| {
        let step = std::str::from_utf8(line).map_or(RewindStep::Other, rewind_step_for_line);
        records.push(LineRecord { index, step });
        Ok(ControlFlow::Continue(()))
    })?;
    let mut records = filter_rewind_by(records, |record| record.step);
    let keep = truncate_for_prompt_by(&records, target_prompt_index, |record| record.step);
    records.truncate(keep);
    Ok(records.into_iter().map(|record| record.index).collect())
}

/// Streaming writer for the fork target's `updates.jsonl`. Corruption-tolerant
/// like the load path: a torn or undecodable line is skipped with a warning
/// instead of failing the fork.
struct UpdateLineWriter<'a> {
    writer: BufWriter<std::fs::File>,
    source: &'a Path,
    target_session_id: &'a acp::SessionId,
    copied: CopiedUpdates,
    skipped_lines: usize,
}

impl<'a> UpdateLineWriter<'a> {
    fn try_new(
        target: &Path,
        source: &'a Path,
        target_session_id: &'a acp::SessionId,
    ) -> io::Result<Self> {
        Ok(Self {
            writer: BufWriter::new(std::fs::File::create(target)?),
            source,
            target_session_id,
            copied: CopiedUpdates::default(),
            skipped_lines: 0,
        })
    }

    fn copy_line(&mut self, line: &[u8]) -> io::Result<()> {
        let update = match std::str::from_utf8(line).map(SessionUpdateEnvelope::from_str) {
            Ok(Ok(update)) => update,
            Ok(Err(error)) => {
                self.skip_torn_line(&error);
                return Ok(());
            }
            Err(error) => {
                self.skip_torn_line(&error);
                return Ok(());
            }
        };
        if is_orchestration_projection_update(&update) {
            return Ok(());
        }
        if let SessionUpdate::Xai(notification) = &update
            && let crate::extensions::notification::SessionUpdate::CompactionCheckpoint(info) =
                &notification.update
        {
            self.copied
                .checkpoint_files
                .insert(info.checkpoint_file.clone());
        }
        let update = transform_session_id_in_update(update, self.target_session_id);
        let envelope = SessionUpdateEnvelope::from_update(&update).map_err(invalid_data)?;
        serde_json::to_writer(&mut self.writer, &envelope).map_err(invalid_data)?;
        self.writer.write_all(b"\n")?;
        self.copied.count += 1;
        Ok(())
    }

    fn skip_torn_line(&mut self, error: &dyn std::fmt::Display) {
        self.skipped_lines += 1;
        if self.skipped_lines == 1 {
            tracing::warn!(
                error = %error,
                path = %self.source.display(),
                "skipping unparseable updates.jsonl line during fork copy (torn append?)"
            );
        }
    }

    fn finish(mut self) -> io::Result<CopiedUpdates> {
        // The first skipped line already warned with its parse error.
        if self.skipped_lines > 1 {
            tracing::warn!(
                skipped = self.skipped_lines,
                copied = self.copied.count,
                path = %self.source.display(),
                "skipped unparseable session update lines during fork copy"
            );
        }
        self.writer.flush()?;
        Ok(self.copied)
    }
}

/// Copy `source` (an `updates.jsonl`) to `target` without materializing it.
/// With a `target_prompt_index`, pass one computes the surviving line set and
/// pass two writes exactly those lines; without one, every line streams
/// through, preserving rewind markers and dead branches. Both passes read one
/// pinned, rewound file handle, so their line indexes cannot skew under a
/// concurrent rename; `updates.jsonl` is append-only by contract, so lines
/// appended after pass one land past every survivor index.
fn copy_updates_streaming(
    source: &Path,
    target: &Path,
    target_session_id: &acp::SessionId,
    target_prompt_index: Option<usize>,
) -> io::Result<CopiedUpdates> {
    let mut writer = UpdateLineWriter::try_new(target, source, target_session_id)?;
    let mut file = match xai_file_utils::session_file::open(source) {
        Ok(file) => file,
        // A missing source is an empty transcript; still write the target.
        Err(error) if error.kind() == io::ErrorKind::NotFound => return writer.finish(),
        Err(error) => return Err(error),
    };
    match target_prompt_index {
        None => {
            for_each_jsonl_line(BufReader::new(file), |_, line| {
                writer.copy_line(line)?;
                Ok(ControlFlow::Continue(()))
            })?;
        }
        Some(target_idx) => {
            let survivors = surviving_line_indexes(BufReader::new(&mut file), target_idx)?;
            file.seek(io::SeekFrom::Start(0))?;
            let mut survivors = survivors.into_iter().peekable();
            for_each_jsonl_line(BufReader::new(file), |index, line| {
                if survivors.next_if_eq(&index).is_some() {
                    writer.copy_line(line)?;
                }
                Ok(if survivors.peek().is_none() {
                    ControlFlow::Break(())
                } else {
                    ControlFlow::Continue(())
                })
            })?;
        }
    }
    writer.finish()
}

/// A point fork must know whether *any* compaction occurred. Ignoring even
/// one corrupt line could hide a checkpoint and expose a future summary.
fn has_compaction_checkpoint(updates: &Path) -> io::Result<bool> {
    let file = xai_file_utils::session_file::open(updates)?;
    let mut reader = BufReader::new(file);
    let mut found = false;
    let mut any = false;
    loop {
        let mut line = Vec::new();
        let n = reader
            .by_ref()
            .take(MAX_UPDATE_LINE_BYTES as u64 + 1)
            .read_until(b'\n', &mut line)?;
        if n == 0 {
            break;
        }
        if line.len() > MAX_UPDATE_LINE_BYTES && line.last() != Some(&b'\n') {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "overlong session update during point fork",
            ));
        }
        let line = line.trim_ascii();
        if line.is_empty() {
            continue;
        }
        any = true;
        let text = std::str::from_utf8(line)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        let update = SessionUpdateEnvelope::from_str(text).map_err(invalid_data)?;
        if matches!(update, SessionUpdate::Xai(notification)
            if matches!(notification.update, crate::extensions::notification::SessionUpdate::CompactionCheckpoint(_)))
        {
            found = true;
        }
    }
    if !any {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "empty session updates during point fork",
        ));
    }
    Ok(found)
}

/// Locate a surviving checkpoint, using the same filtered timeline as the copy.
fn point_fork_checkpoint(
    updates: &Path,
    target: usize,
) -> io::Result<Option<crate::extensions::notification::CompactionCheckpointInfo>> {
    let mut file = xai_file_utils::session_file::open(updates)?;
    let survivors = surviving_line_indexes(BufReader::new(&mut file), target)?;
    file.seek(io::SeekFrom::Start(0))?;
    let mut survivors = survivors.into_iter().peekable();
    let mut checkpoint = None;
    let mut tracker = crate::session::storage::UserRunTurnTracker::new();
    let mut prompt_count = 0usize;
    for_each_jsonl_line(BufReader::new(file), |index, line| {
        if survivors.next_if_eq(&index).is_some() {
            let text = std::str::from_utf8(line)
                .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
            match rewind_step_for_line(text) {
                RewindStep::UserChunk { prompt_index } => {
                    if tracker.on_user_chunk(prompt_index) {
                        prompt_count += 1;
                    }
                }
                RewindStep::Other | RewindStep::Rewind { .. } => tracker.on_non_user(),
            }
            if let SessionUpdate::Xai(notification) =
                SessionUpdateEnvelope::from_str(text).map_err(invalid_data)?
                && let crate::extensions::notification::SessionUpdate::CompactionCheckpoint(info) =
                    notification.update
            {
                checkpoint = Some(*info);
            }
        }
        Ok(if survivors.peek().is_none() {
            ControlFlow::Break(())
        } else {
            ControlFlow::Continue(())
        })
    })?;
    if prompt_count <= target {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "fork target prompt does not exist",
        ));
    }
    Ok(checkpoint)
}

fn contains_string(value: &serde_json::Value, needle: &str) -> bool {
    if needle.is_empty() {
        return false;
    }
    match value {
        serde_json::Value::String(s) => s.contains(needle),
        serde_json::Value::Array(values) => values.iter().any(|v| contains_string(v, needle)),
        serde_json::Value::Object(values) => values
            .iter()
            .any(|(key, value)| key.contains(needle) || contains_string(value, needle)),
        _ => false,
    }
}

fn validate_point_fork_json(
    value: &serde_json::Value,
    source_dir: &Path,
    old_cwd: Option<&str>,
) -> io::Result<()> {
    if contains_string(value, &source_dir.to_string_lossy())
        || old_cwd.is_some_and(|cwd| contains_string(value, cwd))
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "point fork history references the source session or old working directory",
        ));
    }
    Ok(())
}

fn read_regular_checkpoint(source_dir: &Path, relative: &Path) -> io::Result<Vec<u8>> {
    if relative.parent() != Some(Path::new("compaction_checkpoints"))
        || relative.extension() != Some("json".as_ref())
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid checkpoint path",
        ));
    }
    if !std::fs::symlink_metadata(source_dir.join("compaction_checkpoints"))?
        .file_type()
        .is_dir()
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "checkpoint directory is not a real directory",
        ));
    }
    let path = source_dir.join(relative);
    if !std::fs::symlink_metadata(&path)?.file_type().is_file() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "checkpoint is not a regular file",
        ));
    }
    std::fs::read(path)
}

/// When the selected checkpoint is still the exact prefix of current chat,
/// retain the original rich post-checkpoint items instead of replaying them.
fn truncate_matching_checkpoint_chat(
    chat: &mut Vec<ConversationItem>,
    source_dir: &Path,
    checkpoint: &crate::extensions::notification::CompactionCheckpointInfo,
    target: usize,
) -> bool {
    let relative = Path::new(&checkpoint.checkpoint_file);
    let Ok(bytes) = read_regular_checkpoint(source_dir, relative) else {
        return false;
    };
    let Ok(file) =
        serde_json::from_slice::<crate::extensions::notification::CompactionCheckpointFile>(&bytes)
    else {
        return false;
    };
    if file.schema_version > 1
        || file.prompt_index_at_compaction != checkpoint.prompt_index_at_compaction
    {
        return false;
    }
    let prefix_len = file.compacted_history.len();
    if prefix_len > chat.len() {
        return false;
    }
    let (Ok(current_prefix), Ok(checkpoint_prefix)) = (
        serde_json::to_vec(&chat[..prefix_len]),
        serde_json::to_vec(&file.compacted_history),
    ) else {
        return false;
    };
    if current_prefix != checkpoint_prefix {
        return false;
    }
    let mut cut = chat.len();
    let mut seen_marker = false;
    let mut found_target = checkpoint.prompt_index_at_compaction == target.saturating_add(1);
    for (index, item) in chat.iter().enumerate().skip(prefix_len) {
        if let ConversationItem::User(user) = item {
            match user.prompt_index {
                Some(prompt_index) => {
                    seen_marker = true;
                    if prompt_index == target {
                        found_target = true;
                    }
                    if prompt_index > target {
                        cut = index;
                        break;
                    }
                }
                None if !seen_marker => return false,
                None => {} // Mid-turn phantom after a marked prompt.
            }
        }
    }
    if !found_target {
        return false;
    }
    chat.truncate(cut);
    true
}

/// Restore only when the current model-facing history still contains the
/// retained checkpoint as an exact prefix. ACP updates omit model-facing
/// prompt expansion, tool data, and authority metadata, so they cannot be
/// used to reconstruct missing turns.
pub(crate) fn rewind_exact_checkpoint_chat(
    updates: &Path,
    session_dir: &Path,
    conversation: &mut Vec<ConversationItem>,
    exclusive_target: usize,
) -> io::Result<Option<usize>> {
    if exclusive_target == 0 || !has_compaction_checkpoint(updates)? {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "no exact checkpoint for rewind target",
        ));
    }
    let target = exclusive_target - 1;
    let checkpoint = point_fork_checkpoint(updates, target)?.ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            "no checkpoint survives rewind target",
        )
    })?;
    if checkpoint.prompt_index_at_compaction > exclusive_target
        || !truncate_matching_checkpoint_chat(conversation, session_dir, &checkpoint, target)
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "model-facing history is unavailable at rewind target",
        ));
    }
    Ok(Some(checkpoint.prompt_index_at_compaction))
}

impl JsonlStorageAdapter {
    /// Reserve a new root session before copying. Subagent bootstrap uses the
    /// separate copy path because its destination may already be initialized.
    pub(crate) fn copy_new_session_data_sync(
        &self,
        source_info: &Info,
        target_info: &Info,
        options: CopySessionOptions,
    ) -> io::Result<CopySessionResult> {
        let SessionDirMode::FromRoot(root) = &self.dir_mode else {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "fork requires root storage",
            ));
        };
        let id = target_info.id.to_string();
        if source_info.id == target_info.id {
            return Err(io::Error::new(
                io::ErrorKind::AlreadyExists,
                "fork target must have a different session ID",
            ));
        }
        let mut components = Path::new(&id).components();
        if !matches!(components.next(), Some(std::path::Component::Normal(_)))
            || components.next().is_some()
        {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "invalid fork session ID",
            ));
        }

        let cwd_dir = crate::util::grok_home::sessions_cwd_dir_in(root, &target_info.cwd);
        super::super::create_dir_all_durable(&cwd_dir, |_| {
            crate::util::grok_home::ensure_sessions_cwd_dir_in(root, &target_info.cwd)?;
            super::super::sync_cwd_marker_if_present(&cwd_dir)
        })?;
        let target_dir = cwd_dir.join(&id);
        let mut builder = std::fs::DirBuilder::new();
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        // Non-recursive mkdir is the reservation: only its winner owns the
        // target and is allowed to clean it up after an unsuccessful copy.
        builder.create(&target_dir)?;
        struct ReservedTarget(Option<std::path::PathBuf>);
        impl Drop for ReservedTarget {
            fn drop(&mut self) {
                if let Some(path) = self.0.take() {
                    // A published summary can already have been opened by
                    // another process. Never remove a published session even
                    // when its final directory sync reported an error.
                    if path.join("summary.json").exists() {
                        return;
                    }
                    if let Err(error) = std::fs::remove_dir_all(&path) {
                        tracing::warn!(path = %path.display(), %error, "failed to remove incomplete fork");
                    }
                }
            }
        }
        let mut reservation = ReservedTarget(Some(target_dir));
        super::super::sync_dir_durable(&cwd_dir)?;
        let result = self.copy_session_data_sync(source_info, target_info, options)?;
        reservation.0 = None;
        Ok(result)
    }

    /// Fully synchronous implementation of `copy_session_data`, for use on a
    /// blocking thread; every caller reaches it through `spawn_blocking`.
    pub(crate) fn copy_session_data_sync(
        &self,
        source_info: &Info,
        target_info: &Info,
        options: CopySessionOptions,
    ) -> io::Result<CopySessionResult> {
        // Canonical creator: the fork target chain is born owner-only.
        let target_dir = self.create_session_dir_owner_only(target_info)?;

        let source_summary = self.read_summary_sync(source_info)?;
        let chat_format_version = source_summary.chat_format_version;

        let mut chat_to_copy: Vec<ConversationItem> =
            self.read_chat_history_sync(self.chat_file(source_info), chat_format_version)?;

        if let Some(target_idx) = options.target_prompt_index {
            // Fork keeps the target prompt inclusive, whereas replay restores
            // the state before its target. A compacted chat contains a summary
            // of later prompts, so truncation alone cannot reconstruct it.
            let exclusive_target = target_idx.checked_add(1).ok_or_else(|| {
                io::Error::new(io::ErrorKind::InvalidInput, "fork prompt index overflow")
            })?;
            let updates = self.updates_file(source_info);
            let has_checkpoint = has_compaction_checkpoint(&updates)?;
            let source_dir = self.session_dir(source_info);
            if !has_checkpoint {
                let checkpoint_dir = source_dir.join("compaction_checkpoints");
                match std::fs::read_dir(checkpoint_dir) {
                    Ok(mut entries) => {
                        if entries.next().transpose()?.is_some() {
                            return Err(io::Error::new(
                                io::ErrorKind::InvalidData,
                                "checkpoint files exist without a checkpoint update",
                            ));
                        }
                    }
                    Err(error) if error.kind() == io::ErrorKind::NotFound => {}
                    Err(error) => return Err(error),
                }
            }
            let checkpoint = point_fork_checkpoint(&updates, target_idx)?;
            if has_checkpoint {
                let exact_chat = checkpoint.as_ref().is_some_and(|checkpoint| {
                    checkpoint.prompt_index_at_compaction <= exclusive_target
                        && truncate_matching_checkpoint_chat(
                            &mut chat_to_copy,
                            &source_dir,
                            checkpoint,
                            target_idx,
                        )
                });
                if !exact_chat {
                    return Err(io::Error::new(
                        io::ErrorKind::InvalidData,
                        "fork target requires history that cannot be reconstructed from session updates",
                    ));
                }
            } else {
                let keep = conversation_truncate_for_prompt(&chat_to_copy, exclusive_target);
                chat_to_copy.truncate(keep);
            }
            // A compacted summary may direct the model to a source archive with
            // future content. Checkpoint bytes are copied verbatim, so an old
            // cwd in them is also unsafe when this fork rewrites paths.
            let old_cwd = (!options.skip_cwd_transform && source_info.cwd != target_info.cwd)
                .then_some(source_info.cwd.as_str());
            // Ordinary conversation can be cwd-transformed below; only the
            // session directory reference is unsafe in the resulting history.
            validate_point_fork_json(
                &serde_json::to_value(&chat_to_copy).map_err(invalid_data)?,
                &source_dir,
                None,
            )?;
            if has_checkpoint {
                let mut file = xai_file_utils::session_file::open(&updates)?;
                let survivors = surviving_line_indexes(BufReader::new(&mut file), target_idx)?;
                file.seek(io::SeekFrom::Start(0))?;
                let mut survivors = survivors.into_iter().peekable();
                for_each_jsonl_line(BufReader::new(file), |index, line| {
                    if survivors.next_if_eq(&index).is_some() {
                        let text = std::str::from_utf8(line)
                            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
                        if let SessionUpdate::Xai(notification) = SessionUpdateEnvelope::from_str(text).map_err(invalid_data)?
                            && let crate::extensions::notification::SessionUpdate::CompactionCheckpoint(info) = notification.update
                        {
                            let relative = Path::new(&info.checkpoint_file);
                            if relative.parent() != Some(Path::new("compaction_checkpoints")) || relative.file_name().is_none() {
                                return Err(io::Error::new(io::ErrorKind::InvalidData, "invalid checkpoint path"));
                            }
                            let bytes = std::fs::read(source_dir.join(relative))?;
                            let value: serde_json::Value = serde_json::from_slice(&bytes).map_err(invalid_data)?;
                            validate_point_fork_json(&value, &source_dir, old_cwd)?;
                        }
                    }
                    Ok(if survivors.peek().is_none() {
                        ControlFlow::Break(())
                    } else {
                        ControlFlow::Continue(())
                    })
                })?;
            }
        }

        if options.fork_filter {
            fork_filter_chat(&mut chat_to_copy);
        }

        for target in [
            self.workflows_dir(target_info),
            self.goal_mode_state_file(target_info)
                .parent()
                .expect("goal state has a parent")
                .to_path_buf(),
        ] {
            match std::fs::remove_dir_all(&target) {
                Ok(()) => {}
                Err(error) if error.kind() == io::ErrorKind::NotFound => {}
                Err(error) => return Err(error),
            }
        }

        // The child inherits everything below this boundary; compaction
        // preserves it.
        let inherited_prefix_len = if options.fork_filter {
            Some(chat_to_copy.len())
        } else {
            options.inherited_prefix_len
        };

        // Worktree forks skip the cwd rewrite: their display_cwd already
        // shows the model the original project path, and rewritten
        // conversation paths would contradict it.
        if !options.skip_cwd_transform && source_info.cwd != target_info.cwd {
            transform_conversation_cwd(&mut chat_to_copy, &source_info.cwd, &target_info.cwd);
        }

        if options.strip_reasoning {
            chat_to_copy = xai_chat_state::compaction_utils::strip_reasoning_blocks(chat_to_copy);
        }

        let num_chat_messages = chat_to_copy.len();
        let cwd_switch_bookkeeping_generation = chat_to_copy
            .iter()
            .filter_map(ConversationItem::working_directory_switch_generation)
            .max()
            .unwrap_or(0);

        // Release chat history before the (typically much larger) updates copy.
        {
            let mut writer = BufWriter::new(std::fs::File::create(self.chat_file(target_info))?);
            for item in &chat_to_copy {
                serde_json::to_writer(&mut writer, item).map_err(invalid_data)?;
                writer.write_all(b"\n")?;
            }
            writer.flush()?;
        }
        drop(chat_to_copy);

        // A fork_filter copy (subagent context bootstrap) starts the child with
        // an empty replay transcript, so the source updates are never read.
        let copied_updates = if options.fork_filter {
            std::fs::write(self.updates_file(target_info), b"")?;
            CopiedUpdates::default()
        } else {
            copy_updates_streaming(
                &self.updates_file(source_info),
                &self.updates_file(target_info),
                &target_info.id,
                options.target_prompt_index,
            )?
        };
        let checkpoint_files = copied_updates.checkpoint_files;
        let num_messages = copied_updates.count;

        let target_summary = fork_summary(
            source_summary,
            target_info,
            &options,
            ForkCounters {
                num_messages,
                num_chat_messages,
                cwd_switch_bookkeeping_generation,
                inherited_prefix_len,
            },
        );
        let summary_bytes = serde_json::to_vec_pretty(&target_summary).map_err(invalid_data)?;

        let plan_copied = copy_sidecar_file(
            options.copy_plan_state && options.target_prompt_index.is_none(),
            &self.plan_file(source_info),
            &self.plan_file(target_info),
        )?;
        let signals_copied = copy_sidecar_file(
            options.copy_signals && options.target_prompt_index.is_none(),
            &self.signals_file(source_info),
            &self.signals_file(target_info),
        )?;
        let usage_copied = copy_sidecar_file(
            options.copy_usage,
            &self.usage_file(source_info),
            &self.usage_file(target_info),
        )?;
        if let Some(max_turn) = options.target_prompt_index.map(|item| (item + 1) as u32) {
            if usage_copied {
                restamp_copied_usage(
                    &self.usage_file(target_info),
                    &target_info.id,
                    Some(max_turn),
                )?;
            }
        } else if usage_copied {
            restamp_copied_usage(&self.usage_file(target_info), &target_info.id, None)?;
        }
        if usage_copied && !signals_copied {
            seed_signals_turn_from_usage(
                &self.usage_file(target_info),
                &self.signals_file(target_info),
            )?;
        }
        let plan_mode_state_copied =
            if options.copy_plan_mode_state && options.target_prompt_index.is_some() {
                copy_point_fork_plan_restriction(
                    &self.plan_mode_state_file(source_info),
                    &self.plan_mode_state_file(target_info),
                )?
            } else {
                copy_sidecar_file(
                    options.copy_plan_mode_state,
                    &self.plan_mode_state_file(source_info),
                    &self.plan_mode_state_file(target_info),
                )?
            };
        let tool_state_copied = copy_sidecar_file(
            options.copy_tool_state && options.target_prompt_index.is_none(),
            &self.session_dir(source_info).join("tool_state.json"),
            &self.session_dir(target_info).join("tool_state.json"),
        )?;
        let announcement_state_copied = copy_sidecar_file(
            options.copy_announcement_state && options.target_prompt_index.is_none(),
            &self.announcement_state_file(source_info),
            &self.announcement_state_file(target_info),
        )?;
        if announcement_state_copied
            && (options.target_prompt_index.is_some() || options.fork_filter)
        {
            clear_announced_failure_episodes(&self.announcement_state_file(target_info))?;
        }

        if let Some(parent_checkpoint) =
            crate::session::helpers::session_summary::load_title_refresh_watermark(
                &self.session_dir(source_info),
            )
        {
            let child_checkpoint = if options.target_prompt_index.is_none() {
                parent_checkpoint
            } else {
                0
            };
            crate::session::helpers::session_summary::save_title_refresh_watermark(
                &self.session_dir(target_info),
                child_checkpoint,
            );
        }

        // An archive has no per-prompt cut and may describe future turns.
        // Only whole-history forks can safely inherit it verbatim.
        let compaction_segments_copied =
            if options.copy_compaction_segments && options.target_prompt_index.is_none() {
                let src_dir = self
                    .session_dir(source_info)
                    .join(xai_chat_state::compaction_transcript::COMPACTION_DIR);
                let mut copied = 0usize;
                if src_dir.is_dir() {
                    let dst_dir = self
                        .session_dir(target_info)
                        .join(xai_chat_state::compaction_transcript::COMPACTION_DIR);
                    std::fs::create_dir_all(&dst_dir)?;
                    for entry in std::fs::read_dir(&src_dir)? {
                        let entry = entry?;
                        if entry.file_type()?.is_file() {
                            std::fs::copy(entry.path(), dst_dir.join(entry.file_name()))?;
                            copied += 1;
                        }
                    }
                }
                copied
            } else {
                0
            };

        let compaction_checkpoints_copied = copy_referenced_checkpoints(
            &checkpoint_files,
            &self.session_dir(source_info),
            &target_dir,
            &source_info.id,
        )?;

        // A summary makes a directory discoverable as resumable; publish it
        // only after every part of the fork has been copied successfully.
        super::super::write_bytes_atomic(&self.summary_file(target_info), &summary_bytes)?;

        Ok(CopySessionResult {
            chat_messages_copied: num_chat_messages,
            updates_copied: num_messages,
            plan_state_copied: plan_copied,
            plan_mode_state_copied,
            signals_copied,
            tool_state_copied,
            announcement_state_copied,
            compaction_segments_copied,
            compaction_checkpoints_copied,
        })
    }
}

/// Counters produced by this copy that feed the fork target's summary, named
/// so the same-typed counts cannot transpose.
struct ForkCounters {
    num_messages: usize,
    num_chat_messages: usize,
    cwd_switch_bookkeeping_generation: u64,
    inherited_prefix_len: Option<usize>,
}

/// Build the fork target's summary: counters from this copy, fork identity
/// from `options`, and per field either inheritance from the source or a
/// fresh-session reset.
fn fork_summary(
    source: Summary,
    target_info: &Info,
    options: &CopySessionOptions,
    counters: ForkCounters,
) -> Summary {
    let source_model_id = source.current_model_id.clone();
    let target_model_id = options
        .new_model_id
        .clone()
        .map(acp::ModelId::new)
        .unwrap_or_else(|| source_model_id.clone());
    // Provider transport is model-route state. A model-changing fork must
    // resolve it afresh instead of inheriting an incompatible provider pin.
    let resolved_tool_policy = (target_model_id == source_model_id)
        .then_some(source.resolved_tool_policy)
        .flatten();
    let context_window_selection = (target_model_id == source_model_id)
        .then_some(source.context_window_selection)
        .flatten();
    // Compute before moving `source` fields that do not implement Copy.
    let cache_affinity_id = Some(source.prompt_cache_affinity_id());
    let target_worktree_identity =
        crate::session::worktree::worktree_identity_for_cwd(&target_info.cwd);
    let mut summary = Summary {
        info: target_info.clone(),
        cwd_generation: source.cwd_generation,
        previous_cwd: source.previous_cwd,
        pending_cwd_switch_reminder: None,
        cwd_switch_bookkeeping_generation: counters.cwd_switch_bookkeeping_generation,
        session_summary: if options.target_prompt_index.is_some() {
            String::new()
        } else {
            source.session_summary
        },
        created_at: chrono::Utc::now(),
        updated_at: chrono::Utc::now(),
        num_messages: counters.num_messages,
        num_chat_messages: counters.num_chat_messages,
        current_model_id: target_model_id,
        context_window_selection,
        resolved_tool_policy,
        previous_turn_model: source.previous_turn_model,
        ever_used_codex: source.ever_used_codex,
        parent_session_id: options.parent_session_id.clone(),
        forked_at: Some(chrono::Utc::now()),
        collection_id: None,
        next_trace_turn: 0,
        chat_format_version: CHAT_FORMAT_VERSION,
        prompt_display_cwd: options.prompt_display_cwd.clone(),
        session_kind: Some(
            options
                .session_kind
                .clone()
                .unwrap_or_else(|| "fork".to_string()),
        ),
        fork_context_source: options.fork_context_source.clone(),
        fork_parent_prompt_id: options.fork_parent_prompt_id.clone(),
        inherited_prefix_len: counters.inherited_prefix_len,
        hidden: None,
        source_workspace_dir: options.source_workspace_dir.clone(),
        git_root_dir: None,
        git_remotes: Vec::new(),
        head_commit: source.head_commit,
        head_branch: source.head_branch,
        request_id: None,
        // Fresh local grok_home, not inherited from source: the fork lives on this machine.
        grok_home: crate::session::persistence::grok_home_string(),
        last_active_at: source.last_active_at,
        generated_title: if options.target_prompt_index.is_some() {
            None
        } else {
            source.generated_title
        },
        title_is_manual: options.target_prompt_index.is_none() && source.title_is_manual,
        worktree_label: target_worktree_identity
            .as_ref()
            .map(|identity| identity.label.clone()),
        agent: source.agent.clone(),
        sandbox_profile: source.sandbox_profile,
        reasoning_effort: source.reasoning_effort,
        last_turn_summary: if options.target_prompt_index.is_some() {
            None
        } else {
            source.last_turn_summary
        },
        last_turn_summary_prompt_id: if options.target_prompt_index.is_some() {
            None
        } else {
            source.last_turn_summary_prompt_id
        },
        // Resume and forks keep the source's cache identity so a later
        // session/load still *tries* the warmed prompt cache. Server TTL
        // may have expired; a new key would guarantee a miss.
        cache_affinity_id,
        last_recap: if options.target_prompt_index.is_some() {
            None
        } else {
            source.last_recap
        },
    };
    if options.session_kind.is_none()
        && let Some(identity) = &target_worktree_identity
    {
        summary.stamp_worktree_identity(identity);
        if let Some(source_workspace_dir) = &options.source_workspace_dir {
            summary.source_workspace_dir = Some(source_workspace_dir.clone());
        }
    }
    summary
}

/// Copy one optional sidecar file (plan, signals, tool state, ...) when
/// enabled and present; reports whether a copy happened. A sidecar that
/// exists but is not a regular file is skipped with a warning rather than
/// failing the fork.
fn clear_announced_failure_episodes(path: &Path) -> io::Result<()> {
    let bytes = std::fs::read(path)?;
    let Ok(mut state) = serde_json::from_slice::<serde_json::Value>(&bytes) else {
        return Ok(());
    };
    let Some(fields) = state.as_object_mut() else {
        return Ok(());
    };
    if fields.remove("announced_failed_servers").is_none() {
        return Ok(());
    }
    crate::session::storage::write_bytes_atomic(
        path,
        &serde_json::to_vec(&state).map_err(invalid_data)?,
    )
}

fn seed_signals_turn_from_usage(usage_path: &Path, signals_path: &Path) -> io::Result<()> {
    let data = match std::fs::read(usage_path) {
        Ok(data) => data,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
    };
    let Ok(file) = serde_json::from_slice::<crate::session::usage_file::SessionUsageFile>(&data)
    else {
        return Ok(());
    };
    let Some(max_turn) = file.turns.iter().map(|turn| turn.turn_number).max() else {
        return Ok(());
    };
    let signals = crate::session::signals::SessionSignals {
        turn_count: max_turn,
        user_message_count: max_turn,
        ..Default::default()
    };
    crate::session::storage::write_bytes_atomic(
        signals_path,
        &serde_json::to_vec(&signals).map_err(invalid_data)?,
    )
}

fn restamp_copied_usage(
    path: &Path,
    session_id: &acp::SessionId,
    max_turn: Option<u32>,
) -> io::Result<()> {
    let data = match std::fs::read(path) {
        Ok(data) => data,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
    };
    let mut file =
        match serde_json::from_slice::<crate::session::usage_file::SessionUsageFile>(&data) {
            Ok(file) => file,
            Err(error) if max_turn.is_some() => return Err(invalid_data(error)),
            Err(_) => return Ok(()),
        };
    file.session_id = session_id.to_string();
    if let Some(max_turn) = max_turn {
        file.retain_turns_through(max_turn);
    }
    crate::session::storage::write_bytes_atomic(
        path,
        &serde_json::to_vec_pretty(&file).map_err(invalid_data)?,
    )
}

/// Preserve an active restriction without carrying future plan content or
/// approval/reminder bookkeeping from an unversioned snapshot.
fn copy_point_fork_plan_restriction(source: &Path, target: &Path) -> io::Result<bool> {
    use crate::session::plan_mode::{PlanModeSnapshot, PlanModeState};
    let bytes = match std::fs::read(source) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
        Err(error) => return Err(error),
    };
    let source: PlanModeSnapshot = serde_json::from_slice(&bytes).map_err(invalid_data)?;
    if source.state == PlanModeState::Inactive {
        return Ok(false);
    }
    let restricted = PlanModeSnapshot {
        state: PlanModeState::Active,
        was_previously_active: false,
        reminder_count: 0,
        pending_exit_reminder: false,
        awaiting_plan_approval: false,
    };
    crate::session::storage::write_bytes_atomic(
        target,
        &serde_json::to_vec(&restricted).map_err(invalid_data)?,
    )?;
    Ok(true)
}

fn copy_sidecar_file(enabled: bool, src: &Path, dst: &Path) -> io::Result<bool> {
    if !enabled {
        return Ok(false);
    }
    if !src.is_file() {
        if src.exists() {
            tracing::warn!(
                path = %src.display(),
                "sidecar is not a regular file; skipping copy",
            );
        }
        return Ok(false);
    }
    std::fs::copy(src, dst)?;
    Ok(true)
}

/// Copy the `compaction_checkpoints/{uuid}.json` files referenced by the
/// retained records; returns how many copied. Records are user-editable data,
/// so only the exact path shape this feature writes may resolve, symlinks are
/// never followed, and dangling references are skipped rather than failing
/// the fork (otherwise every /rewind in the target session would fail).
fn copy_referenced_checkpoints(
    checkpoint_files: &BTreeSet<String>,
    source_session_dir: &Path,
    target_dir: &Path,
    source_id: &acp::SessionId,
) -> io::Result<usize> {
    if checkpoint_files.is_empty() {
        return Ok(0);
    }
    // The per-file `symlink_metadata` below only vets the final path
    // component, so the intermediate `compaction_checkpoints` dir must itself
    // be a real directory; a symlinked dir would resolve every matching name
    // outside the session.
    match std::fs::symlink_metadata(source_session_dir.join("compaction_checkpoints")) {
        Ok(meta) if meta.file_type().is_dir() => {}
        Ok(meta) => {
            tracing::warn!(
                file_type = ?meta.file_type(),
                session_id = %source_id,
                "compaction_checkpoints is not a real directory; skipping checkpoint copy",
            );
            return Ok(0);
        }
        // Dir gone means every record is dangling; same policy as a missing
        // checkpoint file.
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            tracing::warn!(
                session_id = %source_id,
                "compaction_checkpoints directory missing; skipping checkpoint copy",
            );
            return Ok(0);
        }
        Err(error) => return Err(error),
    }
    let mut copied = 0usize;
    for checkpoint_file in checkpoint_files {
        let relative = Path::new(checkpoint_file);
        // A doctored record path must not address other session files (e.g.
        // the fork's rewritten updates.jsonl).
        let well_formed = relative.parent() == Some(Path::new("compaction_checkpoints"))
            && relative.extension() == Some("json".as_ref());
        if !well_formed {
            tracing::warn!(
                checkpoint_file = %checkpoint_file,
                session_id = %source_id,
                "skipping compaction checkpoint with unexpected path during copy",
            );
            continue;
        }
        let src = source_session_dir.join(relative);
        match std::fs::symlink_metadata(&src) {
            Ok(meta) if meta.file_type().is_file() => {}
            Ok(meta) => {
                // This feature only ever writes regular files, so don't
                // follow symlinks planted in the source session.
                tracing::warn!(
                    path = %src.display(),
                    file_type = ?meta.file_type(),
                    session_id = %source_id,
                    "compaction checkpoint source is not a regular file; skipping copy",
                );
                continue;
            }
            // Already-dangling record (e.g. a chained fork of a broken
            // session): the copy can't invent the file, so don't fail.
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                tracing::warn!(
                    path = %src.display(),
                    session_id = %source_id,
                    "compaction checkpoint file missing from source; skipping copy",
                );
                continue;
            }
            Err(error) => return Err(error),
        }
        let dst = target_dir.join(relative);
        if let Some(parent) = dst.parent() {
            std::fs::create_dir_all(parent)?;
        }
        std::fs::copy(&src, &dst)?;
        copied += 1;
    }
    Ok(copied)
}

fn invalid_data(error: serde_json::Error) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, error)
}

#[cfg(test)]
mod gzip_tests {
    use super::*;
    use flate2::{Compression, write::GzEncoder};

    fn write_compressed(path: &Path, contents: &[u8]) {
        let mut encoder = GzEncoder::new(Vec::new(), Compression::default());
        encoder.write_all(contents).unwrap();
        std::fs::write(path, encoder.finish().unwrap()).unwrap();
    }

    fn updates() -> String {
        let mut contents = String::new();
        for (kind, text, prompt) in [
            ("user_message_chunk", "first", 0),
            ("agent_message_chunk", "reply", 0),
            ("user_message_chunk", "second", 1),
            ("agent_message_chunk", "later", 1),
        ] {
            contents.push_str(
                &serde_json::json!({
                    "timestamp": 1,
                    "method": "session/update",
                    "params": {"sessionId": "source", "update": {
                        "sessionUpdate": kind,
                        "content": {"type": "text", "text": text},
                        "_meta": {"promptIndex": prompt}
                    }}
                })
                .to_string(),
            );
            contents.push('\n');
        }
        contents
    }

    #[test]
    fn gzip_fork_stream_and_point_copy_match_plain() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("updates.jsonl");
        let plain = dir.path().join("plain.jsonl");
        let compressed = dir.path().join("compressed.jsonl");
        let contents = updates();
        let target_id = acp::SessionId::new("target");
        for target in [None, Some(0), Some(1)] {
            std::fs::write(&source, &contents).unwrap();
            let expected = copy_updates_streaming(&source, &plain, &target_id, target).unwrap();
            assert_eq!(expected.count, if target == Some(0) { 2 } else { 4 });
            write_compressed(&source, contents.as_bytes());
            let actual = copy_updates_streaming(&source, &compressed, &target_id, target).unwrap();
            assert_eq!(actual.count, expected.count);
            let records = |path: &Path| {
                std::fs::read_to_string(path)
                    .unwrap()
                    .lines()
                    .map(|line| {
                        let mut value: serde_json::Value = serde_json::from_str(line).unwrap();
                        value.as_object_mut().unwrap().remove("timestamp");
                        value
                    })
                    .collect::<Vec<_>>()
            };
            assert_eq!(records(&compressed), records(&plain));
        }
    }

    #[test]
    fn gzip_fork_rejects_corrupt_trailer_even_for_point_copy() {
        let dir = tempfile::tempdir().unwrap();
        let source = dir.path().join("updates.jsonl");
        let target = dir.path().join("target.jsonl");
        write_compressed(&source, updates().as_bytes());
        let mut bytes = std::fs::read(&source).unwrap();
        let crc = bytes.len() - 8;
        bytes[crc] ^= 1;
        std::fs::write(&source, bytes).unwrap();
        for prompt in [None, Some(0)] {
            assert!(
                copy_updates_streaming(&source, &target, &acp::SessionId::new("target"), prompt)
                    .is_err()
            );
        }
    }
}

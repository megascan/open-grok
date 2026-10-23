//! Extracting searchable text from session update files and turning it
//! into index documents.

use std::io::{self, BufRead, Read};
use std::path::Path;

use super::search_fts::{SessionDoc, SessionSearchIndex};
use super::{
    ContentPeek, PromptAccumulator, PromptExtractEvent, RawLinePeek, RawParamsPeek,
    XAI_SESSION_UPDATE_METHOD,
};
use crate::session::persistence::Summary;
use crate::session::wire_tags::{REWIND_MARKER, USER_MESSAGE_CHUNK};

const SEARCH_CONTENT_CHAR_LIMIT: usize = 200_000;

pub(super) fn should_skip_session(updates_path: &Path, max_size: u64) -> bool {
    // Cheap optimistic guard. The blocking collector also limits decoded
    // bytes, because a gzip file can be much smaller than its transcript.
    match std::fs::metadata(updates_path) {
        Ok(meta) => meta.len() > max_size,
        Err(_) => false,
    }
}

#[derive(Debug)]
pub(super) enum UpsertOutcome {
    Indexed {
        bytes_read: u64,
    },
    /// Content hash matched the existing index entry.
    Unchanged {
        bytes_read: u64,
    },
    /// Decoded content exceeded the bootstrap budget; retained a title-only row.
    TooLarge,
    /// Storage backend doesn't expose an updates file path.
    NoContent,
}

/// Upsert `doc` unless the stored content hash already matches.
pub(super) fn upsert_unless_unchanged(
    index: &SessionSearchIndex,
    doc: &SessionDoc,
    bytes_read: u64,
) -> Result<UpsertOutcome, rusqlite::Error> {
    if let Ok(Some(existing_hash)) = index.get_content_hash(&doc.session_id)
        && existing_hash == doc.content_hash
    {
        return Ok(UpsertOutcome::Unchanged { bytes_read });
    }
    index.upsert_doc(doc)?;
    Ok(UpsertOutcome::Indexed { bytes_read })
}

// Zero-copy peek structs. Text-bearing fields are `Cow`, not `&str`: serde
// cannot borrow `&str` from JSON strings containing escapes, so borrowing
// would error and silently drop the message from the index.

/// Peek for assistant text (agent_message_chunk content.text).
#[derive(serde::Deserialize)]
struct AgentContentPeek<'a> {
    #[serde(borrow)]
    update: AgentUpdatePeek<'a>,
}

#[derive(serde::Deserialize)]
struct AgentUpdatePeek<'a> {
    #[serde(borrow, default)]
    content: Option<AgentTextPeek<'a>>,
}

#[derive(serde::Deserialize)]
struct AgentTextPeek<'a> {
    #[serde(rename = "type", default)]
    content_type: Option<&'a str>,
    #[serde(borrow, default)]
    text: Option<std::borrow::Cow<'a, str>>,
}

/// Peek for user message content (user_message_chunk content.text). Reuses
/// [`ContentPeek`] so the peeked fields stay single-sourced.
#[derive(serde::Deserialize)]
struct UserContentPeek<'a> {
    #[serde(borrow)]
    update: UserUpdatePeek<'a>,
}

#[derive(serde::Deserialize)]
struct UserUpdatePeek<'a> {
    #[serde(borrow, default)]
    content: Option<ContentPeek<'a>>,
    #[serde(default, rename = "_meta")]
    meta: Option<super::RawChunkMetaPeek>,
}

/// Peek for tool call metadata (tool_call title + locations[].path).
#[derive(serde::Deserialize)]
struct ToolCallPeek<'a> {
    #[serde(borrow)]
    update: ToolUpdatePeek<'a>,
}

#[derive(serde::Deserialize)]
struct ToolUpdatePeek<'a> {
    #[serde(borrow, default)]
    title: Option<std::borrow::Cow<'a, str>>,
    #[serde(borrow, default)]
    locations: Option<Vec<ToolLocationPeek<'a>>>,
}

#[derive(serde::Deserialize)]
struct ToolLocationPeek<'a> {
    #[serde(borrow, default)]
    path: Option<std::borrow::Cow<'a, str>>,
}

/// Collect all indexable content from a session's `updates.jsonl` in one
/// pass, without materializing full `acp::SessionNotification` objects.
pub(super) fn collect_all_indexable_content_single_pass(
    updates_path: &Path,
) -> io::Result<(String, u64)> {
    Ok(collect_all_indexable_content_bounded(updates_path, u64::MAX)?.unwrap_or_default())
}

/// Return `None` when decoded input exceeds `max_bytes`. The bound is applied
/// during streaming, before allocating an unbounded line from a gzip file.
/// Callers perform this work on a blocking thread.
pub(super) fn collect_all_indexable_content_bounded(
    updates_path: &Path,
    max_bytes: u64,
) -> io::Result<Option<(String, u64)>> {
    let file = match xai_file_utils::session_file::open(updates_path) {
        Ok(f) => f,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(Some((String::new(), 0))),
        Err(e) => return Err(e),
    };
    let mut reader = io::BufReader::new(file.take(max_bytes.saturating_add(1)));
    let mut bytes_read = 0u64;

    let mut prompts = PromptAccumulator::default();
    let mut assistant_texts: Vec<String> = Vec::new();
    let mut current_assistant: String = String::new();
    let mut tool_meta: Vec<String> = Vec::new();
    let mut assistant_chars = 0usize;
    let mut tool_call_count = 0usize;
    let mut tool_chars_emitted = 0usize;

    const ASSISTANT_MAX_CHARS: usize = 100_000;
    const TOOL_MAX_CALLS: usize = 200;
    const TOOL_MAX_CHARS: usize = 100_000;

    // Flush the assistant buffer on turn boundary; called in every
    // non-agent_message_chunk branch to match `collect_assistant_text`.
    let flush_assistant = |current: &mut String, texts: &mut Vec<String>| {
        if !current.is_empty() {
            let t = current.trim().to_string();
            if !t.is_empty() {
                texts.push(t);
            }
            current.clear();
        }
    };

    let mut line = Vec::new();
    loop {
        line.clear();
        // Propagate gzip corruption, while retaining the existing behavior
        // of skipping individual malformed UTF-8 records.
        let count = reader.read_until(b'\n', &mut line)?;
        if count == 0 {
            break;
        }
        bytes_read += count as u64;
        if bytes_read > max_bytes {
            return Ok(None);
        }
        let Ok(line) = std::str::from_utf8(&line) else {
            flush_assistant(&mut current_assistant, &mut assistant_texts);
            prompts.push(PromptExtractEvent::NotUserMessage);
            continue;
        };
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        let (raw_params, is_xai) = if let Ok(env) = serde_json::from_str::<RawLinePeek<'_>>(trimmed)
        {
            let raw = env.params.map(|p| p.get()).unwrap_or(trimmed);
            let xai = env.method == Some(XAI_SESSION_UPDATE_METHOD);
            (raw, xai)
        } else {
            (trimmed, false)
        };

        let update_peek = serde_json::from_str::<RawParamsPeek<'_>>(raw_params)
            .ok()
            .and_then(|p| p.update);
        let tag = update_peek.as_ref().map(|u| u.session_update);

        // Content events arrive on ACP "session/update"; control events
        // (rewind markers) on the xAI "_x.ai/session/update" extension.
        if !is_xai {
            match tag {
                Some(t) if t == *USER_MESSAGE_CHUNK => {
                    flush_assistant(&mut current_assistant, &mut assistant_texts);
                    if let Ok(peek) = serde_json::from_str::<UserContentPeek<'_>>(raw_params)
                        && let Some(content) = peek.update.content
                        && content.content_type == Some("text")
                        && let Some(text) = content.text
                    {
                        if content
                            .meta
                            .as_ref()
                            .is_some_and(|m| m.bash_command.is_some())
                            || peek
                                .update
                                .meta
                                .as_ref()
                                .is_some_and(|m| m.host_turn == Some(true))
                        {
                            prompts.push(PromptExtractEvent::NotUserMessage);
                        } else {
                            let prompt_index = peek
                                .update
                                .meta
                                .as_ref()
                                .and_then(|m| m.prompt_index.map(|v| v as usize));
                            prompts.push(PromptExtractEvent::UserTextChunk {
                                text: text.into_owned(),
                                prompt_index,
                            });
                        }
                    } else {
                        prompts.push(PromptExtractEvent::NotUserMessage);
                    }
                }
                Some("agent_message_chunk") => {
                    // Same assistant turn: no flush.
                    if assistant_chars < ASSISTANT_MAX_CHARS
                        && let Ok(peek) = serde_json::from_str::<AgentContentPeek<'_>>(raw_params)
                        && let Some(content) = peek.update.content
                        && content.content_type == Some("text")
                        && let Some(text) = content.text
                        && !text.is_empty()
                    {
                        let sep_cost = usize::from(!current_assistant.is_empty());
                        let budget = ASSISTANT_MAX_CHARS
                            .saturating_sub(assistant_chars)
                            .saturating_sub(sep_cost);
                        if budget > 0 {
                            if sep_cost > 0 {
                                current_assistant.push(' ');
                                assistant_chars += 1;
                            }
                            let mut take = text.len().min(budget);
                            while take > 0 && !text.is_char_boundary(take) {
                                take -= 1;
                            }
                            current_assistant.push_str(&text[..take]);
                            assistant_chars += take;
                        }
                    }
                    prompts.push(PromptExtractEvent::NotUserMessage);
                }
                Some("agent_thought_chunk") => {
                    // Same assistant turn: not indexed, but must not flush.
                    prompts.push(PromptExtractEvent::NotUserMessage);
                }
                Some("tool_call") => {
                    flush_assistant(&mut current_assistant, &mut assistant_texts);
                    if tool_call_count < TOOL_MAX_CALLS {
                        tool_call_count += 1;
                        if let Ok(peek) = serde_json::from_str::<ToolCallPeek<'_>>(raw_params) {
                            if let Some(title) = peek.update.title
                                && !title.is_empty()
                            {
                                let budget = TOOL_MAX_CHARS.saturating_sub(tool_chars_emitted);
                                if budget > 0 {
                                    let mut take = title.len().min(budget);
                                    while take > 0 && !title.is_char_boundary(take) {
                                        take -= 1;
                                    }
                                    tool_meta.push(title[..take].to_string());
                                    tool_chars_emitted += take;
                                }
                            }
                            if let Some(locs) = peek.update.locations {
                                for loc in locs {
                                    if let Some(p) = loc.path
                                        && !p.is_empty()
                                    {
                                        let budget =
                                            TOOL_MAX_CHARS.saturating_sub(tool_chars_emitted);
                                        if budget > 0 {
                                            let mut take = p.len().min(budget);
                                            while take > 0 && !p.is_char_boundary(take) {
                                                take -= 1;
                                            }
                                            tool_meta.push(p[..take].to_string());
                                            tool_chars_emitted += take;
                                        }
                                    }
                                }
                            }
                        }
                    }
                    prompts.push(PromptExtractEvent::NotUserMessage);
                }
                _ => {
                    flush_assistant(&mut current_assistant, &mut assistant_texts);
                    prompts.push(PromptExtractEvent::NotUserMessage);
                }
            }
        } else {
            match tag {
                Some(t) if t == *REWIND_MARKER => {
                    flush_assistant(&mut current_assistant, &mut assistant_texts);
                    if let Some(ref u) = update_peek
                        && let Some(idx) = u.target_prompt_index
                    {
                        prompts.push(PromptExtractEvent::RewindTo(idx));
                    } else {
                        prompts.push(PromptExtractEvent::NotUserMessage);
                    }
                }
                _ => {
                    flush_assistant(&mut current_assistant, &mut assistant_texts);
                    prompts.push(PromptExtractEvent::NotUserMessage);
                }
            }
        }
    }

    if !current_assistant.is_empty() {
        let t = current_assistant.trim().to_string();
        if !t.is_empty() {
            assistant_texts.push(t);
        }
    }

    let prompts = prompts.finish();

    let parts = [
        prompts.join("\n\n"),
        assistant_texts.join("\n"),
        tool_meta.join("\n"),
    ];
    let mut joined = parts.join("\n\n");

    if joined.len() > SEARCH_CONTENT_CHAR_LIMIT {
        // Keep the tail: the most recent content is the most relevant.
        let mut start = joined.len().saturating_sub(SEARCH_CONTENT_CHAR_LIMIT);
        while start < joined.len() && !joined.is_char_boundary(start) {
            start += 1;
        }
        joined = joined[start..].to_string();
    }

    Ok(Some((joined, bytes_read)))
}

pub(super) fn build_session_doc(summary: &Summary, content: String) -> SessionDoc {
    let title = summary.display_title().to_owned();

    let mut hasher = blake3::Hasher::new();
    hasher.update(title.as_bytes());
    hasher.update(b"\0");
    hasher.update(content.as_bytes());
    let content_hash = hasher.finalize().to_hex().to_string();

    SessionDoc {
        session_id: summary.info.id.to_string(),
        cwd: summary.info.cwd.clone(),
        updated_at_unix: summary.updated_at.timestamp(),
        title,
        content,
        content_hash,
    }
}

/// Summary fixture shared by this module's tests and `search.rs`.
#[cfg(test)]
pub(super) fn test_summary(session_id: &str, cwd: &str, title: &str) -> Summary {
    use crate::session::info::Info;
    use agent_client_protocol as acp;

    Summary {
        info: Info {
            id: acp::SessionId::new(session_id),
            cwd: cwd.to_string(),
        },
        cwd_generation: 0,
        previous_cwd: None,
        pending_cwd_switch_reminder: None,
        cwd_switch_bookkeeping_generation: 0,
        session_summary: title.to_string(),
        created_at: chrono::Utc::now(),
        updated_at: chrono::Utc::now(),
        num_messages: 0,
        num_chat_messages: 0,
        current_model_id: acp::ModelId::new("test"),
        resolved_tool_policy: None,
        previous_turn_model: None,
        ever_used_codex: false,
        parent_session_id: None,
        forked_at: None,
        collection_id: None,
        next_trace_turn: 0,
        chat_format_version: 1,
        prompt_display_cwd: None,
        session_kind: None,
        fork_context_source: None,
        fork_parent_prompt_id: None,
        inherited_prefix_len: None,
        hidden: None,
        source_workspace_dir: None,
        git_root_dir: None,
        git_remotes: Vec::new(),
        head_commit: None,
        head_branch: None,
        request_id: None,
        grok_home: None,
        last_active_at: None,
        generated_title: None,
        title_is_manual: false,
        worktree_label: None,
        agent: Default::default(),
        sandbox_profile: None,
        reasoning_effort: None,
        context_window_selection: None,
        last_turn_summary: None,
        last_turn_summary_prompt_id: None,
        last_recap: None,
        cache_affinity_id: None,
    }
}

#[cfg(test)]
#[path = "search_content_tests.rs"]
mod tests;

#[cfg(test)]
mod gzip_tests {
    use super::*;
    use flate2::{Compression, write::GzEncoder};
    use std::io::Write;

    #[test]
    fn gzip_search_preserves_content_decoded_size_and_corruption_errors() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("updates.jsonl");
        let contents = format!(
            "{}\n",
            serde_json::json!({
                "timestamp": 1, "method": "session/update",
                "params": {"sessionId": "s", "update": {
                    "sessionUpdate": "user_message_chunk",
                    "content": {"type": "text", "text": "searchable prompt ".repeat(1000)}
                }}
            })
        );
        std::fs::write(&path, &contents).unwrap();
        let expected = collect_all_indexable_content_single_pass(&path).unwrap();
        let mut encoder = GzEncoder::new(Vec::new(), Compression::default());
        encoder.write_all(contents.as_bytes()).unwrap();
        let mut compressed = encoder.finish().unwrap();
        assert!(compressed.len() < contents.len() / 2);
        std::fs::write(&path, &compressed).unwrap();
        assert_eq!(
            collect_all_indexable_content_single_pass(&path).unwrap(),
            expected
        );
        assert!(
            collect_all_indexable_content_bounded(&path, contents.len() as u64 - 1)
                .unwrap()
                .is_none()
        );
        assert_eq!(
            collect_all_indexable_content_bounded(&path, contents.len() as u64).unwrap(),
            Some(expected)
        );
        compressed.truncate(compressed.len() - 4);
        std::fs::write(&path, compressed).unwrap();
        assert!(collect_all_indexable_content_single_pass(&path).is_err());
    }
}

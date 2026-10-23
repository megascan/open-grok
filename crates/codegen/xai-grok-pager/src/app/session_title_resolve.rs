//! Resume-by-title selection shared by startup paths.
//!
//! The composition root pins an explicit non-id resume target to its canonical session id BEFORE the irreversible OS sandbox
//! ([`super::cli::PagerArgs::pin_local_resume_target`]).
//! That way the saved-profile peek and materialization act on one immutable target instead of racing a concurrent rename between two title lookups.
//! Materialization keeps [`select_by_title`] as the authoritative error source (ambiguity / no-match).
//! It is also the fallback for callers that bypass pinning.

use xai_grok_shell::session::persistence::{RecentSessionSelection, Summary};

/// UUID-shaped resume args always take the id path, even when no such id exists and a session is titled with that exact UUID.
pub(crate) fn is_uuid_shaped(arg: &str) -> bool {
    uuid::Uuid::try_parse(arg).is_ok()
}

/// IDs are a single session-directory component; other text may be a title.
pub(crate) fn is_local_id_component(arg: &str) -> bool {
    !arg.is_empty() && !matches!(arg, "." | "..") && !arg.contains(['/', '\\'])
}

/// Treat punctuation and whitespace as word separators, preserving word order.
/// Case matching uses lowercase, not full Unicode case folding.
pub(crate) fn title_key(s: &str) -> String {
    s.to_lowercase()
        .split(|c: char| !c.is_alphanumeric())
        .filter(|word| !word.is_empty())
        .collect::<Vec<_>>()
        .join(" ")
}

/// Hint appended to a local title miss.
/// Debug formatting: the arg is arbitrary user text.
pub(crate) fn title_miss_hint(arg: &str) -> String {
    format!(
        "no session id or title matched {arg:?} for this directory; \
         try `open-grok sessions search {arg:?}`"
    )
}

/// Suggest locally available titles sharing a word or a near-matching word.
/// Keep output bounded and order it by the session's most recent update.
pub(crate) fn title_miss_with_suggestions(arg: &str, summaries: &[Summary]) -> String {
    let needle = title_key(arg);
    let words: Vec<_> = needle.split_whitespace().collect();
    let mut similar: Vec<_> = summaries
        .iter()
        .filter(|summary| {
            let candidate = title_key(summary.display_title());
            let candidate_words: Vec<_> = candidate.split_whitespace().collect();
            words.iter().any(|word| {
                candidate_words.iter().any(|other| {
                    word.contains(other) || other.contains(word) || one_edit_apart(word, other)
                })
            })
        })
        .collect();
    similar.sort_by(|a, b| {
        b.updated_at
            .cmp(&a.updated_at)
            .then_with(|| a.info.id.to_string().cmp(&b.info.id.to_string()))
    });
    let mut message = title_miss_hint(arg);
    if !similar.is_empty() {
        message.push_str("\nSimilar local sessions (newest first):");
        for summary in similar.into_iter().take(5) {
            message.push_str(&format!(
                "\n  {}  {:?}  updated {}",
                summary.info.id,
                summary.display_title(),
                summary.updated_at.to_rfc3339()
            ));
        }
    }
    message
}

fn one_edit_apart(a: &str, b: &str) -> bool {
    let a: Vec<_> = a.chars().take(65).collect();
    let b: Vec<_> = b.chars().take(65).collect();
    if a.len() < 4 || b.len() < 4 || a.len() > 64 || b.len() > 64 || a.len().abs_diff(b.len()) > 1 {
        return false;
    }
    let (mut i, mut j, mut edits) = (0, 0, 0);
    while i < a.len() && j < b.len() {
        if a[i] == b[j] {
            i += 1;
            j += 1;
        } else {
            edits += 1;
            if edits > 1 {
                return false;
            }
            if a.len() >= b.len() {
                i += 1;
            }
            if b.len() >= a.len() {
                j += 1;
            }
        }
    }
    edits + (a.len() - i) + (b.len() - j) <= 1
}

/// Select the local session a resume arg names by title.
///
/// - `Ok(None)`: UUID-shaped or blank arg, or no title matched; the caller keeps id-miss behavior.
/// - `Ok(Some)`: exactly one match, or a sole manual `/rename` among duplicates (explicit user intent beats colliding auto titles).
/// - `Err`: ambiguous; never silently pick one, headless scripts need determinism.
///   Candidate titles are Debug-escaped: `/rename` accepts arbitrary text, and raw control characters would corrupt the listing.
pub(crate) fn select_by_title<'a>(
    arg: &str,
    summaries: &'a [Summary],
) -> anyhow::Result<Option<&'a Summary>> {
    if is_uuid_shaped(arg) {
        return Ok(None);
    }
    let needle = title_key(arg);
    if needle.is_empty() {
        return Ok(None);
    }
    let mut matches: Vec<&Summary> = summaries
        .iter()
        .filter(|s| title_key(s.display_title()) == needle)
        .collect();
    matches.sort_by(|a, b| {
        b.updated_at
            .cmp(&a.updated_at)
            .then_with(|| a.info.id.to_string().cmp(&b.info.id.to_string()))
    });
    match matches.as_slice() {
        [] => Ok(None),
        [only] => Ok(Some(*only)),
        _ => {
            let manual: Vec<&&Summary> = matches
                .iter()
                .filter(|s| {
                    s.manual_title_opt()
                        .is_some_and(|t| title_key(&t) == needle)
                })
                .collect();
            if let [only] = manual.as_slice() {
                return Ok(Some(**only));
            }
            let listing = matches
                .iter()
                .map(|s| {
                    format!(
                        "  {}  {:?}  updated {}",
                        s.info.id,
                        s.display_title(),
                        s.updated_at.to_rfc3339()
                    )
                })
                .collect::<Vec<_>>()
                .join("\n");
            anyhow::bail!(
                "Multiple sessions match title {:?}:\n{listing}\n\
                 Resume by session id instead: open-grok --resume <session-id>",
                arg.trim()
            );
        }
    }
}

/// Outcome of the pre-sandbox resolution of an explicit resume arg.
#[derive(Debug, PartialEq, Eq)]
pub(crate) enum PinnedResumeTarget {
    /// Nothing local resolved (UUID-shaped, no cwd, junk, or ambiguous title): leave the raw arg alone.
    /// Materialization owns the authoritative error / remote path.
    Unresolved,
    /// Resolved as a local id (possibly the restored child of a remote id).
    Id(String),
    /// Resolved by title to this session. The selected summary's persisted sandbox profile rides along.
    /// Re-deriving the profile from the id is ambiguous when a legacy id is duplicated across cwd dirs.
    Title {
        id: String,
        sandbox_profile: Option<String>,
    },
}

impl PinnedResumeTarget {
    pub(crate) fn id(self) -> Option<String> {
        match self {
            Self::Unresolved => None,
            Self::Id(id) | Self::Title { id, .. } => Some(id),
        }
    }
}

/// Resolve an explicit resume arg to a pinned local session id before the (irreversible) OS sandbox.
/// The saved-profile peek and materialization must consume one immutable target, not re-run title selection against mutable summaries.
/// Id lookups stay authoritative (same order as `resolve_existing_session`).
/// That preserves the restored-child id so the peek cannot drift to a same-id session in another cwd.
/// Errs on a listing failure (fail closed instead of guessing) and on ambiguity, which must be reported before the sandbox rather than after it.
pub(crate) fn presandbox_resume_target(
    arg: &str,
    cwd: Option<&str>,
    selection: RecentSessionSelection,
) -> anyhow::Result<PinnedResumeTarget> {
    if is_uuid_shaped(arg) {
        return Ok(PinnedResumeTarget::Unresolved);
    }
    let Some(cwd) = cwd else {
        return Ok(PinnedResumeTarget::Unresolved);
    };
    if is_local_id_component(arg) {
        if let Some(local_id) = xai_grok_shell::session::resolve_local_session(arg, cwd) {
            return Ok(PinnedResumeTarget::Id(local_id));
        }
        if xai_grok_shell::session::persistence::resolve_local_session_any_cwd_result(arg)?
            .is_some()
        {
            return Ok(PinnedResumeTarget::Id(arg.to_string()));
        }
    }
    let summaries =
        xai_grok_shell::session::persistence::local_summaries_for_cwd_sync(cwd, selection)
            .map_err(|e| {
                anyhow::anyhow!(
                    "failed to list local sessions while resolving --resume {arg:?}: {e}"
                )
            })?;
    Ok(select_by_title(arg, &summaries)?
        .map(|s| PinnedResumeTarget::Title {
            id: s.info.id.to_string(),
            sandbox_profile: s.sandbox_profile.clone(),
        })
        .unwrap_or(PinnedResumeTarget::Unresolved))
}

/// Failure message for a worktree resume.
/// `local_miss_target` is `Some(arg)` only when materialization deferred exactly this target after missing local id/title resolution.
/// Provenance is threaded, never inferred from id shape, so a resolved legacy non-UUID id gets no false no-match hint.
/// `detail` must already be user-sanitized: sanitizing the composed message would collapse disk-full chains whole and erase the appended hint.
pub(crate) fn worktree_resume_failure_message(
    local_miss_target: Option<&str>,
    detail: &str,
) -> String {
    let msg = format!("couldn't resume worktree session: {detail}");
    match local_miss_target {
        Some(target) => format!("{msg}; {}", title_miss_hint(target)),
        None => msg,
    }
}

#[cfg(test)]
#[path = "session_title_resolve_tests.rs"]
mod tests;

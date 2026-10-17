//! Per-turn dashboard summary helpers.
//!
//! After each turn the shell generates an ultra-short one-line summary of the
//! agent's reply for that turn (not a meta activity log), shown as the
//! dashboard row's secondary line. Like recap, it is display-only and never
//! mutates the conversation. Requests include only bounded visible text from the latest turn.

use crate::sampling::ConversationItem;
use crate::session::helpers::chat::floor_char_boundary;

/// Hard cap on the summary (characters). The instruction targets 5–12 words;
/// this only guards against runaway output. Rows truncate to width on render.
pub(crate) const TURN_SUMMARY_MAX_CHARS: usize = 200;

/// The latest prompt opener and visible assistant text following it. Synthetic
/// wake events open turns; mid-turn reminders do not. The retained text uses
/// UTF-8 byte budgets including truncation markers.
pub(crate) fn last_turn(
    conversation: &[ConversationItem],
    user_max_chars: usize,
    reply_max_chars: usize,
) -> Option<(String, String)> {
    let (start, opener) = conversation.iter().enumerate().rev().find(|(_, item)| {
        matches!(item, ConversationItem::User(u) if u.prompt_index.is_some() || u.synthetic_reason.as_ref().is_none_or(|r| r.starts_prompt_turn()))
    })?;
    let user_text = opener.text_content();
    let user_text = match user_text.trim() {
        "" => "(no text; attachments only)",
        text => text,
    };
    // Work backwards and cap as we build, rather than allocating an unbounded reply.
    let mut reply = String::new();
    for item in conversation[start + 1..].iter().rev() {
        let ConversationItem::Assistant(assistant) = item else {
            continue;
        };
        let text = assistant.content.trim();
        if text.is_empty() {
            continue;
        }
        let piece = super::session_recap::bounded_tail(text, reply_max_chars);
        let joined = if reply.is_empty() {
            piece
        } else {
            format!("{piece}\n\n{reply}")
        };
        reply = super::session_recap::bounded_tail(&joined, reply_max_chars);
        if text.len() > reply_max_chars || joined.len() > reply_max_chars {
            break;
        }
    }
    if reply.is_empty() {
        return None;
    }
    Some((
        super::session_recap::bounded_head(user_text, user_max_chars),
        reply,
    ))
}

pub(crate) const TURN_SUMMARY_SYSTEM: &str = "Write an ultra-short dashboard line that captures the AGENT'S REPLY \
     to the user's message. Focus on what the agent concluded, answered, recommended, or delivered, \
     not a meta description of the turn (avoid \"Explained…\", \"Answered…\", \"Greeted…\", \
     \"Reviewed…\").\n\n\
     Output ONLY the fragment: 5-12 words, plain text, glanceable on a status row. \
     Prefer the payload: answer, finding, change, or decision needed.\n\n\
     Synthetic examples (style only, do not copy):\n\
     `queue_worker` shutdown race fixed; suite green\n\
     Payment retries: exp backoff in `billing/retry.rs`, 5× on 429\n\
     Retry backoff wired into `billing/retry.rs`; tests pending\n\
     Need decision: keep or drop `sqlx` cache before refactor\n\
     Black — matches the terminal aesthetic\n\n\
     Bad (never):\n\
     - Lead with Explained / Answered / Greeted / Reviewed / Confirmed / Flagged / Summarized\n\
     - Labels, quotes, bullets, markdown, code fences, multi-sentence dumps\n\
     - Filler like \"no code changes\" or \"awaiting task\" unless that is the whole point\n\
     - Invent content not in the agent's reply";

pub(crate) fn turn_summary_user_message(user_text: &str, reply: &str) -> String {
    format!(
        "<user_message>\n{user_text}\n</user_message>\n\n<agent_reply>\n{reply}\n</agent_reply>"
    )
}

/// Clean the model's raw output into a one-line fragment: recap normalization
/// (whitespace collapse, stray label/quote stripping) plus the tighter
/// [`TURN_SUMMARY_MAX_CHARS`] cap.
pub(crate) fn clean_turn_summary_text(raw: &str) -> String {
    let mut out = super::session_recap::clean_recap_text(raw);
    if out.len() > TURN_SUMMARY_MAX_CHARS {
        let cut = floor_char_boundary(&out, TURN_SUMMARY_MAX_CHARS);
        out.truncate(cut);
        out = out.trim_end().to_string();
        out.push('\u{2026}');
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn user(text: &str) -> ConversationItem {
        ConversationItem::user(text.to_string())
    }

    #[test]
    fn last_turn_handles_wakes_and_excludes_mid_turn_injections() {
        use xai_grok_sampling_types::{ContentPart, SyntheticReason, UserItem};
        let wake = ConversationItem::User(UserItem {
            content: vec![ContentPart::Text {
                text: "agent task finished".into(),
            }],
            synthetic_reason: Some(SyntheticReason::AgentMessage),
            ..Default::default()
        });
        let conv = vec![
            user("older"),
            ConversationItem::assistant("older answer"),
            wake,
            ConversationItem::assistant("working"),
            ConversationItem::system_reminder("secret reminder"),
            ConversationItem::tool_result("call", "secret output"),
            ConversationItem::assistant("fixed the issue"),
        ];
        let (opener, reply) = last_turn(&conv, 100, 100).unwrap();
        assert_eq!(opener, "agent task finished");
        assert_eq!(reply, "working\n\nfixed the issue");
        assert!(!reply.contains("secret"));
        assert!(last_turn(&[user("question")], 100, 100).is_none());
        let (opener, reply) = last_turn(&conv, 7, 10).unwrap();
        assert!(opener.len() <= 7 && reply.len() <= 10);
        assert!(reply.ends_with("issue"));
    }

    #[test]
    fn explicit_prompt_index_opens_a_synthetic_turn() {
        use xai_grok_sampling_types::{ContentPart, SyntheticReason, UserItem};
        let opener = ConversationItem::User(UserItem {
            content: vec![ContentPart::Text {
                text: "goal wake".into(),
            }],
            synthetic_reason: Some(SyntheticReason::GoalSummary),
            prompt_index: Some(1),
            ..Default::default()
        });
        let (text, reply) =
            last_turn(&[opener, ConversationItem::assistant("done")], 100, 100).unwrap();
        assert_eq!(text, "goal wake");
        assert_eq!(reply, "done");
    }

    #[test]
    fn clean_normalizes_and_caps() {
        assert_eq!(
            clean_turn_summary_text("Summary: \"Fixed the\n\n  parser\""),
            "Fixed the parser"
        );
        let capped = clean_turn_summary_text(&"word ".repeat(100));
        assert!(capped.len() <= TURN_SUMMARY_MAX_CHARS + '\u{2026}'.len_utf8());
        assert!(capped.ends_with('\u{2026}'));
    }
}

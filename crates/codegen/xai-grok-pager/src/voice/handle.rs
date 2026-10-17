//! Map pipeline [`VoiceEvent`]s onto prompt-box dictation state.

use xai_grok_voice::{TaggedVoiceEvent, VoiceEvent};

use crate::app::app_view::{AppView, VoiceTarget};
use crate::views::prompt_widget::PromptWidget;

pub(crate) const RECORDING_DISCARDED_TOAST: &str = "Voice: recording discarded";
pub(crate) const TRANSCRIPTION_TIMED_OUT_TOAST: &str =
    "Voice: transcription timed out; recording discarded";
pub(crate) const TRANSCRIPTION_TIMED_OUT_KEPT_TOAST: &str =
    "Voice: transcription timed out; the words shown so far were kept";

/// Join committed prompt text with a voice fragment. Space-separated unless the
/// prompt is empty or already ends in whitespace (keeps trailing newlines).
pub(crate) fn combine_prompt_with_voice_text(existing: &str, text: &str) -> String {
    if existing.trim().is_empty() {
        text.to_string()
    } else if existing.ends_with(char::is_whitespace) {
        format!("{existing}{text}")
    } else {
        format!("{existing} {text}")
    }
}

/// Append `text` to the prompt bound at capture start (agent or dashboard).
///
/// Finals always append at end (or replace a blank draft). The caret follows
/// when it was at end; mid-text edits keep their place.
fn append_voice_text_to_prompt(app: &mut AppView, text: &str) {
    let target = app.voice_recording_target();
    append_voice_text_to_target(app, target, text);
}

fn append_voice_text_to_target(app: &mut AppView, target: Option<VoiceTarget>, text: &str) {
    let append = |prompt: &mut PromptWidget| {
        let existing = prompt.text();
        let cursor = prompt.cursor();
        let blank = existing.trim().is_empty();
        // Blank draft is a full replace — park the caret at the new end.
        // Otherwise append at end; only follow the caret if it was already there.
        let follow_end = blank || cursor >= existing.len();
        let combined = combine_prompt_with_voice_text(existing, text);
        prompt.set_text(&combined);
        prompt.set_cursor(if follow_end { combined.len() } else { cursor });
    };
    match target {
        Some(VoiceTarget::Agent(id)) => {
            let Some(agent) = app.agents.get_mut(&id) else {
                return;
            };
            append(&mut agent.prompt);
        }
        Some(target @ (VoiceTarget::DashboardDispatch | VoiceTarget::DashboardPeekReply(_))) => {
            let Some(dashboard) = app.dashboard.as_mut() else {
                return;
            };
            // Peek reply is shared across rows: only land if still on the bound row.
            let prompt = match target {
                VoiceTarget::DashboardPeekReply(rec) => {
                    let peeked = match dashboard.peek.as_ref().map(|p| &p.row) {
                        Some(crate::views::dashboard::DashboardRowId::TopLevel(id)) => Some(*id),
                        _ => None,
                    };
                    if peeked != Some(rec) {
                        return;
                    }
                    &mut dashboard.peek_reply
                }
                _ => &mut dashboard.dispatch,
            };
            append(prompt);
        }
        None => {}
    }
}

/// Move non-empty interim into the bound prompt and clear the overlay.
/// Does not stop the mic. Returns the promoted fragment.
pub(crate) fn commit_interim_into_prompt(app: &mut AppView) -> Option<String> {
    let interim = app
        .voice_interim()
        .map(str::trim)
        .filter(|t| !t.is_empty())
        .map(str::to_owned)?;
    append_voice_text_to_prompt(app, &interim);
    app.voice_commit_interim();
    Some(interim)
}

/// Reject late events from a superseded capture while allowing its one trailing
/// final to reach the same prompt after a rapid stop and restart.
pub fn handle_tagged_voice_event(app: &mut AppView, tagged: TaggedVoiceEvent) -> bool {
    let TaggedVoiceEvent { session, event } = tagged;
    if let Some((stopped, target)) = app.voice_trailing_final
        && session == stopped
        && let VoiceEvent::UtteranceFinal { text } = &event
    {
        app.voice_trailing_final = None;
        if app
            .voice_recording_target()
            .is_none_or(|current| current == target)
            && !text.trim().is_empty()
        {
            append_voice_text_to_target(app, Some(target), text.trim());
        }
        return true;
    }
    if session != app.voice_session && !matches!(event, VoiceEvent::Notice { .. }) {
        let kind: &'static str = (&event).into();
        tracing::trace!(?session, current = ?app.voice_session, kind, "stale voice event dropped");
        return false;
    }
    handle_voice_event(app, event)
}

/// Apply a voice event to app state. Returns whether the frame should redraw.
pub fn handle_voice_event(app: &mut AppView, event: VoiceEvent) -> bool {
    match event {
        VoiceEvent::CaptureStarted { route } => {
            app.voice_set_route(route);
            false
        }
        VoiceEvent::CaptureCancelled => {
            app.voice_capture_cancelled();
            true
        }
        VoiceEvent::Transcribing => {
            app.voice_mark_transcribing();
            true
        }
        VoiceEvent::Notice { message } => {
            app.show_toast(&format!("Voice: {message}"));
            true
        }
        VoiceEvent::InterimTranscript { text } => {
            // No-op unless recording, so a late interim after a stop can't
            // repopulate the overlay.
            app.voice_set_interim(text)
        }
        VoiceEvent::UtteranceFinal { text } => {
            app.voice_commit_interim();
            // Keep the mic open across pauses; user stops explicitly, then Enter to send.
            // The bound target survives a stop (`Stopping`), so a trailing final
            // after an explicit stop still lands.
            if !text.trim().is_empty() {
                append_voice_text_to_prompt(app, text.trim());
            }
            app.voice_finish_transcribing();
            true
        }
        VoiceEvent::Error { message, hint } => {
            let target = app.voice_recording_target();
            if app.voice_state.is_on_clip_route() {
                let _ = commit_interim_into_prompt(app);
            }
            app.voice_reset();
            app.show_toast(&format!("Voice: {message}"));
            // Long fix steps: agent/peek scrollback only (toast is one line;
            // dashboard dispatch has no scrollback).
            if let Some(hint) = hint
                && let Some(VoiceTarget::Agent(id) | VoiceTarget::DashboardPeekReply(id)) = target
                && let Some(agent) = app.agents.get_mut(&id)
            {
                agent
                    .scrollback
                    .push_block(crate::scrollback::block::RenderBlock::system(format!(
                        "Voice: {message}. {hint}"
                    )));
            }
            true
        }
    }
}

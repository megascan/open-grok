//! `/model` selects a model, optional context window and reasoning effort.

use agent_client_protocol as acp;
use xai_grok_shell::sampling::types::{ReasoningEffortOption, supports_reasoning_effort_meta};

use crate::acp::model_state::ModelState;
use crate::app::actions::Action;
use crate::slash::command::{AppCtx, ArgItem, CommandExecCtx, CommandResult, SlashCommand};
use crate::slash::commands::context_window::{
    format_window, parse_window_token, supported_window, unknown_window_error, window_arg_items,
};
use crate::slash::commands::effort_levels::build_effort_arg_items;

/// Switch the active model (and optionally its reasoning effort).
pub struct ModelCommand;

impl SlashCommand for ModelCommand {
    fn name(&self) -> &str {
        "model"
    }

    fn aliases(&self) -> &[&str] {
        &["m"]
    }

    fn description(&self) -> &str {
        "Switch the active model"
    }

    fn session_scoped(&self) -> bool {
        true
    }

    fn offered_when_session_less(&self) -> bool {
        // The dashboard offers `/model` to pick the model for the next
        // spawned agent (intercepted in `dispatch_dashboard_dispatch_slash`).
        true
    }

    fn usage(&self) -> &str {
        "/model <name> [window] [effort]"
    }

    fn takes_args(&self) -> bool {
        true
    }

    fn args_required(&self) -> bool {
        true
    }

    fn arg_placeholder(&self) -> Option<&str> {
        Some("<model> [window] [effort]")
    }

    fn suggest_args(&self, ctx: &AppCtx, args_query: &str) -> Option<Vec<ArgItem>> {
        if ctx.models.is_empty() {
            return None;
        }

        match chained_phase(ctx.models, args_query) {
            Some(ChainedPhase::Window { model_id, prefix }) => {
                Some(build_window_items(ctx.models, &model_id, &prefix))
            }
            Some(ChainedPhase::Effort { model_id, prefix }) => {
                Some(build_effort_items(ctx.models, &model_id, &prefix))
            }
            Some(ChainedPhase::Done) => None,
            None => Some(build_model_items(ctx.models)),
        }
    }

    fn preselected_arg(&self, ctx: &AppCtx, args_query: &str) -> Option<String> {
        let fresh = |prefix: &str| args_query.trim_end().eq_ignore_ascii_case(prefix);
        match chained_phase(ctx.models, args_query)? {
            ChainedPhase::Window { model_id, prefix } if fresh(&prefix) => {
                let window = ctx.models.window_after_switch_to(&model_id)?;
                Some(window_insert_text(ctx.models, &model_id, &prefix, window))
            }
            ChainedPhase::Effort { model_id, prefix } if fresh(&prefix) => {
                let option = ctx.models.preselected_effort_option_for(&model_id)?;
                Some(effort_insert_text(&prefix, &option))
            }
            _ => None,
        }
    }

    fn run(&self, ctx: &mut CommandExecCtx, args: &str) -> CommandResult {
        let trimmed = args.trim();
        if trimmed.is_empty() {
            return CommandResult::Error("Usage: /model <name> [window] [effort]".into());
        }

        // Prefer an exact full-string catalog match first. Model display names
        // often contain spaces ("Grok 4.5"); if we split on the last token
        // first, a shorter catalog entry ("Grok") would steal the prefix and
        // treat "4.5" as an effort level.
        if let Some(id) = resolve_model(ctx.models, trimmed) {
            return CommandResult::Action(Action::SetDefaultModel(id));
        }

        if let Some((id, rest)) = split_model_effort(ctx.models, trimmed) {
            let windows = ctx.models.context_window_options_for(&id);
            let (window, effort_token) = match split_window_effort(rest, &windows) {
                Ok(split) => split,
                Err(message) => return CommandResult::Error(message),
            };
            let effort = if effort_token.is_empty() && window.is_some() {
                ctx.models
                    .reasoning_effort
                    .filter(|_| ctx.models.current.as_ref() == Some(&id))
            } else {
                match ctx.models.resolve_effort_for_model(&id, effort_token) {
                    Ok(effort) => Some(effort),
                    Err(err) => return CommandResult::Error(err.message()),
                }
            };
            if let Some(window) = window {
                if ctx.session_id.is_none() {
                    return CommandResult::Error(
                        "No active session yet; retry once it starts".into(),
                    );
                }
                return CommandResult::Action(Action::SwitchModelWithWindow {
                    model_id: id,
                    effort,
                    window,
                });
            }
            return CommandResult::Action(Action::SwitchModel {
                model_id: id,
                effort,
                service_tier: None,
            });
        }

        CommandResult::Error(format!("Unknown model: {trimmed}"))
    }
}

/// Resolve provider-qualified selectors first, then catalog ids, then
/// unqualified display names/slugs when they identify exactly one model.
fn resolve_model(models: &ModelState, name: &str) -> Option<acp::ModelId> {
    let query = name.trim();
    unique_model_match(models, |id, info| {
        model_selector(id, info).eq_ignore_ascii_case(query)
    })
    .or_else(|| unique_model_match(models, |id, _| id.0.as_ref().eq_ignore_ascii_case(query)))
    .or_else(|| {
        unique_model_match(models, |id, info| {
            info.name.eq_ignore_ascii_case(query)
                || model_catalog_slug(id, info).eq_ignore_ascii_case(query)
        })
    })
}

fn unique_model_match(
    models: &ModelState,
    mut predicate: impl FnMut(&acp::ModelId, &acp::ModelInfo) -> bool,
) -> Option<acp::ModelId> {
    let mut matches = models
        .available
        .iter()
        .filter_map(|(id, info)| predicate(id, info).then_some(id.clone()));
    let matched = matches.next()?;
    matches.next().is_none().then_some(matched)
}

fn supports_reasoning_effort(info: &acp::ModelInfo) -> bool {
    supports_reasoning_effort_meta(info.meta.as_ref())
}

fn model_provider_id(info: &acp::ModelInfo) -> Option<&str> {
    info.meta
        .as_ref()
        .and_then(|meta| meta.get("provider"))
        .and_then(serde_json::Value::as_str)
}

fn model_provider_label(info: &acp::ModelInfo) -> Option<&str> {
    match model_provider_id(info) {
        Some("codex" | "openai" | "openai_codex") => Some("OpenAI Codex"),
        Some("xai") => Some("xAI"),
        Some("kimi" | "moonshot" | "moonshot_ai") => Some("Kimi"),
        Some("fireworks" | "fireworks_ai") => Some("Fireworks AI"),
        Some("deepseek" | "deep_seek" | "deepseek_api") => Some("DeepSeek"),
        Some("opencode_go" | "opencode-go") => Some("OpenCode Go"),
        Some("openrouter" | "open_router" | "open-router") => Some("OpenRouter"),
        Some("wafer" | "wafer_ai") => Some("Wafer AI"),
        Some("zai" | "z_ai" | "z-ai") => Some("Z AI"),
        Some("runinfra" | "run_infra" | "run-infra") => Some("RunInfra"),
        Some(
            "gemini" | "google" | "google_gemini" | "google-gemini" | "ai_studio" | "ai-studio"
            | "aistudio" | "gemini_api",
        ) => Some("Google Gemini"),
        Some(provider) => Some(provider),
        None => None,
    }
}

fn model_catalog_slug<'a>(id: &'a acp::ModelId, info: &acp::ModelInfo) -> &'a str {
    let id = id.0.as_ref();
    let Some(provider) = model_provider_id(info) else {
        return id;
    };
    id.strip_prefix(provider)
        .and_then(|suffix| suffix.strip_prefix(':'))
        .unwrap_or(id)
}

fn model_selector(id: &acp::ModelId, info: &acp::ModelInfo) -> String {
    match model_provider_id(info) {
        Some(provider) => format!("{provider}:{}", model_catalog_slug(id, info)),
        None => info.name.clone(),
    }
}

fn model_context_window(info: &acp::ModelInfo) -> Option<u64> {
    info.meta
        .as_ref()
        .and_then(|meta| meta.get("totalContextTokens"))
        .and_then(serde_json::Value::as_u64)
}

fn format_context_window(tokens: u64) -> String {
    fn compact(tokens: u64, divisor: u64, suffix: &str) -> String {
        let tenths = ((tokens as u128 * 10 + u128::from(divisor / 2)) / u128::from(divisor)) as u64;
        if tenths.is_multiple_of(10) {
            format!("{}{suffix} context", tenths / 10)
        } else {
            format!("{}.{:01}{suffix} context", tenths / 10, tenths % 10)
        }
    }

    if tokens >= 1_000_000 {
        compact(tokens, 1_000_000, "M")
    } else if tokens >= 1_000 {
        compact(tokens, 1_000, "K")
    } else {
        format!("{tokens} context")
    }
}

fn model_picker_description(id: &acp::ModelId, info: &acp::ModelInfo) -> String {
    let mut parts = Vec::with_capacity(3);
    parts.push(model_catalog_slug(id, info).to_string());
    if let Some(tokens) = model_context_window(info) {
        parts.push(format_context_window(tokens));
    }
    if let Some(description) = info
        .description
        .as_deref()
        .filter(|description| !description.trim().is_empty())
    {
        parts.push(description.trim().to_string());
    }
    parts.join(" · ")
}

/// The arg picker title for the `/model` sub-menu that `args_query` has reached.
pub(crate) fn picker_title(models: &ModelState, args_query: &str) -> &'static str {
    match chained_phase(models, args_query) {
        Some(ChainedPhase::Window { .. }) => "Pick context window",
        Some(ChainedPhase::Effort { .. }) => "Pick reasoning effort",
        Some(ChainedPhase::Done) | None => "Pick model",
    }
}

/// Which chained sub-menu the typed args have reached.
enum ChainedPhase {
    Window {
        model_id: acp::ModelId,
        prefix: String,
    },
    Effort {
        model_id: acp::ModelId,
        prefix: String,
    },
    Done,
}

/// The sub-menu for `args_query`, or `None` while the user is still picking a model.
fn chained_phase(models: &ModelState, args_query: &str) -> Option<ChainedPhase> {
    let (id, key, rest) = longest_reasoning_prefix(models, args_query)?;
    let model_id = id;
    let windows = models.context_window_options_for(&model_id);
    let reasoning = models
        .available
        .get(&model_id)
        .is_some_and(supports_reasoning_effort);

    let phase = match committed_first_token(rest) {
        // A committed window advances the chain to the effort phase
        Some(first)
            if parse_window_token(first).is_some_and(|window| windows.contains(&window)) =>
        {
            if reasoning {
                ChainedPhase::Effort {
                    model_id,
                    prefix: format!("{key} {first}"),
                }
            } else {
                ChainedPhase::Done
            }
        }
        // Any other committed token is a typed effort
        Some(_) if reasoning => ChainedPhase::Effort {
            model_id,
            prefix: key.clone(),
        },
        // A longer model name may still match ("Foo Bar" after "Foo")
        Some(_) => return None,
        None if windows.len() > 1 => {
            // A partial token that can't start any window row ("Grok 4.7 hi") is a typed effort
            if starts_a_window(&windows, rest.trim_start()) {
                ChainedPhase::Window {
                    model_id,
                    prefix: key.clone(),
                }
            } else if reasoning {
                ChainedPhase::Effort {
                    model_id,
                    prefix: key.clone(),
                }
            } else {
                return None;
            }
        }
        None if reasoning => ChainedPhase::Effort {
            model_id,
            prefix: key.clone(),
        },
        None => ChainedPhase::Done,
    };
    Some(phase)
}

/// The first token is committed once whitespace follows it.
fn committed_first_token(rest: &str) -> Option<&str> {
    let rest_trimmed = rest.trim_start();
    match rest_trimmed.split_once(char::is_whitespace) {
        Some((first, _)) => Some(first),
        None if !rest_trimmed.is_empty() && rest.ends_with(char::is_whitespace) => {
            Some(rest_trimmed)
        }
        None => None,
    }
}

/// Whether a partial token can still grow into one of the window rows (an empty partial always can).
fn starts_a_window(windows: &[u64], partial: &str) -> bool {
    let partial = partial.to_ascii_lowercase();
    windows.iter().any(|&window| {
        format_window(window).starts_with(&partial) || window.to_string().starts_with(&partial)
    })
}

fn split_on_model_key<'a>(args: &'a str, key: &str) -> Option<&'a str> {
    let rest = args.get(key.len()..)?;
    if args
        .get(..key.len())
        .is_some_and(|prefix| prefix.eq_ignore_ascii_case(key))
        && rest.starts_with(char::is_whitespace)
    {
        Some(rest)
    } else {
        None
    }
}

/// Longest reasoning-model selector, name, or id that prefixes `args`, and the text after it.
fn longest_reasoning_prefix<'a>(
    models: &'a ModelState,
    args: &'a str,
) -> Option<(acp::ModelId, String, &'a str)> {
    let mut best: Option<(acp::ModelId, String, &str)> = None;
    let mut ambiguous = false;
    for (id, info) in models
        .available
        .iter()
        .filter(|(id, info)| has_chained_args(models, id, info))
    {
        let selector = model_selector(id, info);
        for key in [selector.as_str(), info.name.as_str(), id.0.as_ref()] {
            let Some(rest) = split_on_model_key(args, key) else {
                continue;
            };
            match best.as_ref() {
                Some((_, prev, _)) if prev.len() > key.len() => {}
                Some((prior, prev, _)) if prev.len() == key.len() => {
                    if prior != id {
                        ambiguous = true;
                    }
                }
                _ => {
                    best = Some((id.clone(), key.to_string(), rest));
                    ambiguous = false;
                }
            }
        }
    }
    if ambiguous {
        return None;
    }
    best
}

fn split_model_effort<'a>(
    models: &'a ModelState,
    args: &'a str,
) -> Option<(acp::ModelId, &'a str)> {
    let (id, _, rest) = longest_reasoning_prefix(models, args)?;
    let token = rest.trim();
    if token.is_empty() {
        None
    } else {
        Some((id, token))
    }
}

fn has_chained_args(models: &ModelState, id: &acp::ModelId, info: &acp::ModelInfo) -> bool {
    supports_reasoning_effort(info) || models.context_window_options_for(id).len() > 1
}

/// A window is always the first token, and the remaining words are the effort.
fn split_window_effort<'a>(
    rest: &'a str,
    windows: &[u64],
) -> Result<(Option<std::num::NonZeroU64>, &'a str), String> {
    let (first, tail) = match rest.split_once(char::is_whitespace) {
        Some((first, tail)) => (first, tail.trim()),
        None => (rest, ""),
    };
    if windows.is_empty() || parse_window_token(first).is_none() {
        return Ok((None, rest));
    }
    match supported_window(first, windows) {
        Some(window) => Ok((Some(window), tail)),
        None => Err(unknown_window_error(first, windows)),
    }
}

/// One row per logical model. Reasoning models get a trailing space in
/// `insert_text` so the prompt widget chains into the effort sub-menu.
fn build_model_items(models: &ModelState) -> Vec<ArgItem> {
    let current_id = models.current.as_ref();
    let mut entries: Vec<_> = models.available.iter().collect();
    entries.sort_by(|(id_a, info_a), (id_b, info_b)| {
        model_provider_label(info_a)
            .unwrap_or_default()
            .to_ascii_lowercase()
            .cmp(
                &model_provider_label(info_b)
                    .unwrap_or_default()
                    .to_ascii_lowercase(),
            )
            .then_with(|| {
                info_a
                    .name
                    .to_ascii_lowercase()
                    .cmp(&info_b.name.to_ascii_lowercase())
            })
            .then_with(|| model_catalog_slug(id_a, info_a).cmp(model_catalog_slug(id_b, info_b)))
    });
    let mut items: Vec<ArgItem> = Vec::with_capacity(models.available.len());
    for (id, info) in entries {
        let is_current = current_id == Some(id);
        let supports = has_chained_args(models, id, info);
        let selector = model_selector(id, info);
        let row_label = match model_provider_label(info) {
            Some(provider) => format!("{provider} · {}", info.name),
            None => info.name.clone(),
        };

        let display = if is_current {
            format!("{row_label} (current)")
        } else {
            row_label
        };

        // Trailing space on reasoning models: signals "more input
        // expected" to the prompt widget so Enter advances to effort
        // phase instead of submitting.
        let insert_text = if supports {
            format!("{selector} ")
        } else {
            selector
        };

        items.push(ArgItem {
            display,
            match_text: format!(
                "{} {} {}",
                info.name,
                model_catalog_slug(id, info),
                model_provider_label(info).unwrap_or_default()
            ),
            insert_text,
            description: model_picker_description(id, info),
        });
    }
    items
}

/// One row per selectable window for the `/model` chained window phase.
fn build_window_items(models: &ModelState, model_id: &acp::ModelId, prefix: &str) -> Vec<ArgItem> {
    let windows = models.context_window_options_for(model_id);
    // Only the current model has an active window
    let active = (models.current.as_ref() == Some(model_id))
        .then(|| models.get_context_window())
        .flatten();
    let default = models.model_default_window(model_id);
    window_arg_items(&windows, active, default, |_, window| {
        window_insert_text(models, model_id, prefix, window)
    })
}

fn window_insert_text(
    models: &ModelState,
    model_id: &acp::ModelId,
    prefix: &str,
    window: u64,
) -> String {
    let chains_to_effort = models
        .available
        .get(model_id)
        .is_some_and(supports_reasoning_effort);
    let label = format_window(window);
    if chains_to_effort {
        format!("{prefix} {label} ")
    } else {
        format!("{prefix} {label}")
    }
}

/// One row per effort level for the `/model` chained effort phase.
/// `prefix` is the selector, name, or catalog id the user typed. `insert_text` is `"{prefix} {effort}"`.
fn build_effort_items(models: &ModelState, model_id: &acp::ModelId, prefix: &str) -> Vec<ArgItem> {
    if !models.available.contains_key(model_id) {
        return Vec::new();
    }
    let is_current_model = models.current.as_ref() == Some(model_id);
    let options = models.reasoning_effort_options_for(model_id);
    build_effort_arg_items(
        &options,
        models.reasoning_effort,
        is_current_model,
        |option| effort_insert_text(prefix, option),
    )
}

fn effort_insert_text(prefix: &str, option: &ReasoningEffortOption) -> String {
    format!("{prefix} {}", option.id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use xai_grok_shell::sampling::types::ReasoningEffort;

    fn model_with_reasoning(id: &str, name: &str) -> (acp::ModelId, acp::ModelInfo) {
        let id = acp::ModelId::new(Arc::from(id));
        let mut meta = serde_json::Map::new();
        meta.insert(
            "supportsReasoningEffort".into(),
            serde_json::Value::Bool(true),
        );
        let info = acp::ModelInfo::new(id.clone(), name.to_string())
            .meta(serde_json::Value::Object(meta).as_object().cloned());
        (id, info)
    }

    fn plain_model(id: &str, name: &str) -> (acp::ModelId, acp::ModelInfo) {
        let id = acp::ModelId::new(Arc::from(id));
        let info = acp::ModelInfo::new(id.clone(), name.to_string());
        (id, info)
    }

    fn provider_model(
        id: &str,
        name: &str,
        provider: &str,
        context_window: u64,
    ) -> (acp::ModelId, acp::ModelInfo) {
        let id = acp::ModelId::new(Arc::from(id));
        let info = acp::ModelInfo::new(id.clone(), name.to_string())
            .description(Some("Model description".to_string()))
            .meta(Some(serde_json::Map::from_iter([
                (
                    "provider".to_string(),
                    serde_json::Value::String(provider.to_string()),
                ),
                (
                    "totalContextTokens".to_string(),
                    serde_json::Value::Number(context_window.into()),
                ),
            ])));
        (id, info)
    }

    #[test]
    fn model_picker_chains_window_before_effort_and_preserves_provider_identity() {
        let mut state = ModelState::default();
        let (id, mut info) = provider_model("example", "Example", "codex", 256_000);
        let meta = info.meta.as_mut().unwrap();
        meta.insert(
            "contextWindows".into(),
            serde_json::json!([256_000, 500_000]),
        );
        meta.insert("supportsReasoningEffort".into(), serde_json::json!(true));
        state.available.insert(id.clone(), info);
        assert_eq!(
            picker_title(&state, "codex:example "),
            "Pick context window"
        );
        assert_eq!(
            picker_title(&state, "codex:example 500k "),
            "Pick reasoning effort"
        );
        let windows = build_window_items(&state, &id, "codex:example");
        assert_eq!(windows[1].insert_text, "codex:example 500k ");
        let sid = acp::SessionId::new("session");
        let mut ctx = dummy_exec_ctx(&state);
        ctx.session_id = Some(&sid);
        assert!(matches!(
            ModelCommand.run(&mut ctx, "codex:example 500k high"),
            CommandResult::Action(Action::SwitchModelWithWindow { model_id, effort: Some(ReasoningEffort::High), window })
                if model_id == id && window.get() == 500_000
        ));
        assert!(
            matches!(ModelCommand.run(&mut ctx, "codex:example 1m high"),
            CommandResult::Error(message) if message.contains("unknown context window"))
        );
    }

    #[test]
    fn ambiguous_names_cannot_choose_a_provider_for_chained_selection() {
        let mut state = ModelState::default();
        for (provider, id) in [("codex", "codex:model"), ("xai", "xai:model")] {
            let (id, mut info) = provider_model(id, "Same model", provider, 256_000);
            info.meta.as_mut().unwrap().insert(
                "contextWindows".into(),
                serde_json::json!([256_000, 500_000]),
            );
            state.available.insert(id, info);
        }
        let sid = acp::SessionId::new("session");
        let mut ctx = dummy_exec_ctx(&state);
        ctx.session_id = Some(&sid);
        assert!(matches!(
            ModelCommand.run(&mut ctx, "Same model 500k"),
            CommandResult::Error(_)
        ));
        assert!(matches!(ModelCommand.run(&mut ctx, "codex:model 500k"),
            CommandResult::Action(Action::SwitchModelWithWindow { model_id, .. }) if model_id.0.as_ref() == "codex:model"));
    }

    static EMPTY_BUNDLE: crate::app::bundle::BundleState = crate::app::bundle::BundleState {
        has_cache: false,
        version: String::new(),
        personas: Vec::new(),
        roles: Vec::new(),
        agents: Vec::new(),
        skills: Vec::new(),
        persona_details: Vec::new(),
        role_details: Vec::new(),
    };

    fn dummy_exec_ctx(models: &ModelState) -> CommandExecCtx<'_> {
        CommandExecCtx {
            models,
            session_id: None,
            bundle_state: &EMPTY_BUNDLE,
            screen_mode: crate::app::ScreenMode::Inline,
            billing_surface_visible: true,
            pager_state: crate::settings::PagerLocalSnapshot {
                multiline_mode: false,
                yolo_mode: false,
                ..crate::settings::PagerLocalSnapshot::default()
            },
        }
    }

    #[test]
    fn split_model_effort_keeps_a_multi_word_label() {
        let mut state = ModelState::default();
        let (id, info) = model_with_reasoning("grok-4.7", "Grok 4.7");
        state.available.insert(id.clone(), info);
        assert_eq!(
            split_model_effort(&state, "Grok 4.7 Extra High")
                .map(|(model, token)| { (model.0.to_string(), token.to_string()) }),
            Some(("grok-4.7".to_string(), "Extra High".to_string()))
        );
        assert_eq!(
            split_model_effort(&state, "Grok 4.7 high").map(|(_, token)| token),
            Some("high")
        );
        assert!(split_model_effort(&state, "Grok 4.7").is_none());
        assert_eq!(
            split_model_effort(&state, "grok-4.7 Extra High")
                .map(|(model, token)| (model.0.to_string(), token.to_string())),
            Some(("grok-4.7".to_string(), "Extra High".to_string()))
        );
    }

    #[test]
    fn empty_query_returns_one_row_per_logical_model() {
        let mut state = ModelState::default();
        let (rid, rinfo) = model_with_reasoning("reasoning-x", "Reasoning X");
        let (pid, pinfo) = plain_model("grok-4.5", "Grok 4.5");
        state.available.insert(rid, rinfo);
        state.available.insert(pid, pinfo);

        let cmd = ModelCommand;
        let ctx = AppCtx {
            models: &state,
            cwd: std::path::Path::new("."),
            has_session_announcements: false,
            billing_surface_visible: true,
            workflows_available: true,
            saved_workflows: &[],
            workflow_runs: &[],
            screen_mode: crate::app::ScreenMode::Fullscreen,
            current_title: None,
        };
        let items = cmd.suggest_args(&ctx, "").unwrap();
        assert_eq!(items.len(), 2, "model phase: one row per logical model");

        // Reasoning model has trailing space in insert_text -- this is the
        // signal the prompt widget reads to keep the dropdown open after
        // Enter so the effort sub-menu can render.
        let reasoning = items
            .iter()
            .find(|i| i.insert_text.trim_end() == "Reasoning X")
            .expect("reasoning model row");
        assert_eq!(reasoning.insert_text, "Reasoning X ");

        // Plain model has no trailing space -- Enter commits immediately.
        let plain = items
            .iter()
            .find(|i| i.insert_text == "Grok 4.5")
            .expect("plain model row");
        assert_eq!(plain.insert_text, "Grok 4.5");
    }

    #[test]
    fn model_rows_show_provider_and_context_window_and_match_model_id() {
        let mut state = ModelState::default();
        let (codex_id, codex) = provider_model("gpt-5.6-sol", "GPT-5.6 Sol", "codex", 353_000);
        let (xai_id, xai) = provider_model("grok-build", "Grok Build", "xai", 500_000);
        state.available.insert(codex_id, codex);
        state.available.insert(xai_id, xai);

        let items = build_model_items(&state);
        let codex = items
            .iter()
            .find(|item| item.insert_text == "codex:gpt-5.6-sol")
            .expect("Codex model row");
        assert_eq!(codex.display, "OpenAI Codex · GPT-5.6 Sol");
        assert_eq!(
            codex.description,
            "gpt-5.6-sol · 353K context · Model description"
        );
        assert!(codex.match_text.contains("gpt-5.6-sol"));
        assert!(codex.match_text.contains("OpenAI Codex"));

        let xai = items
            .iter()
            .find(|item| item.insert_text == "xai:grok-build")
            .expect("xAI model row");
        assert_eq!(xai.display, "xAI · Grok Build");
        assert_eq!(
            xai.description,
            "grok-build · 500K context · Model description"
        );

        assert_eq!(format_context_window(353_400), "353.4K context");
    }

    #[test]
    fn duplicate_model_names_are_sorted_and_selected_by_provider() {
        let mut state = ModelState::default();
        let (codex_id, codex) = provider_model("gpt-5.6-sol", "GPT-5.6 Sol", "codex", 353_000);
        let (xai_id, xai) = provider_model("xai:gpt-5.6-sol", "GPT-5.6 Sol", "xai", 353_000);
        state.available.insert(xai_id.clone(), xai);
        state.available.insert(codex_id.clone(), codex);

        let items = build_model_items(&state);
        assert_eq!(items[0].display, "OpenAI Codex · GPT-5.6 Sol");
        assert_eq!(items[0].insert_text, "codex:gpt-5.6-sol");
        assert_eq!(items[1].display, "xAI · GPT-5.6 Sol");
        assert_eq!(items[1].insert_text, "xai:gpt-5.6-sol");

        let mut ctx = dummy_exec_ctx(&state);
        assert!(matches!(
            ModelCommand.run(&mut ctx, "codex:gpt-5.6-sol"),
            CommandResult::Action(Action::SetDefaultModel(id)) if id == codex_id
        ));
        assert!(matches!(
            ModelCommand.run(&mut ctx, "xai:gpt-5.6-sol"),
            CommandResult::Action(Action::SetDefaultModel(id)) if id == xai_id
        ));
        assert!(matches!(
            ModelCommand.run(&mut ctx, "GPT-5.6 Sol"),
            CommandResult::Error(message) if message.contains("Unknown model")
        ));
    }

    #[test]
    fn trailing_space_after_reasoning_model_enters_effort_phase() {
        let mut state = ModelState::default();
        let (id, info) = model_with_reasoning("reasoning-x", "Reasoning X");
        state.available.insert(id, info);

        let cmd = ModelCommand;
        let ctx = AppCtx {
            models: &state,
            cwd: std::path::Path::new("."),
            has_session_announcements: false,
            billing_surface_visible: true,
            workflows_available: true,
            saved_workflows: &[],
            workflow_runs: &[],
            screen_mode: crate::app::ScreenMode::Fullscreen,
            current_title: None,
        };
        // Args query has a trailing space -> effort phase. Items come out
        // ordered xhigh -> low (strongest first) per EFFORT_LEVELS.
        let items = cmd.suggest_args(&ctx, "Reasoning X ").unwrap();
        assert_eq!(items.len(), 4);
        assert_eq!(items[0].insert_text, "Reasoning X xhigh");
        assert_eq!(items[1].insert_text, "Reasoning X high");
        assert_eq!(items[2].insert_text, "Reasoning X medium");
        assert_eq!(items[3].insert_text, "Reasoning X low");
        // Display is just the level so the user sees a clean column.
        assert_eq!(items[0].display, "xhigh");
        // match_text carries the sort-key prefix that forces the matcher's
        // alphabetical tiebreak to render rows in EFFORT_LEVELS order.
        assert!(items[0].match_text.starts_with("a "));
        assert!(items[3].match_text.starts_with("d "));
    }

    #[test]
    fn preselected_arg_targets_default_row_only_for_fresh_effort_menu() {
        let mut state = ModelState::default();
        let id = acp::ModelId::new(Arc::from("reasoning-x"));
        let info = acp::ModelInfo::new(id.clone(), "Reasoning X").meta(
            serde_json::json!({ "supportsReasoningEffort": true, "reasoningEffort": "high" })
                .as_object()
                .cloned(),
        );
        state.available.insert(id, info);

        let cmd = ModelCommand;
        let ctx = AppCtx {
            models: &state,
            cwd: std::path::Path::new("."),
            has_session_announcements: false,
            billing_surface_visible: true,
            workflows_available: true,
            saved_workflows: &[],
            workflow_runs: &[],
            screen_mode: crate::app::ScreenMode::Fullscreen,
            current_title: None,
        };
        // The preselection must name a row `suggest_args` actually builds, or the consumers fall back to row 0
        let high_row = cmd
            .suggest_args(&ctx, "Reasoning X ")
            .and_then(|items| items.get(1).map(|item| item.insert_text.clone()));
        assert_eq!(Some("Reasoning X high".to_owned()), high_row);
        assert_eq!(high_row, cmd.preselected_arg(&ctx, "Reasoning X "));
        assert_eq!(None, cmd.preselected_arg(&ctx, "Reasoning X h"));
        assert_eq!(None, cmd.preselected_arg(&ctx, ""));
    }

    #[test]
    fn partial_effort_query_still_in_effort_phase() {
        let mut state = ModelState::default();
        let (id, info) = model_with_reasoning("reasoning-x", "Reasoning X");
        state.available.insert(id, info);

        let cmd = ModelCommand;
        let ctx = AppCtx {
            models: &state,
            cwd: std::path::Path::new("."),
            has_session_announcements: false,
            billing_surface_visible: true,
            workflows_available: true,
            saved_workflows: &[],
            workflow_runs: &[],
            screen_mode: crate::app::ScreenMode::Fullscreen,
            current_title: None,
        };
        // Still in effort phase; matcher upstream narrows to high / xhigh.
        let items = cmd.suggest_args(&ctx, "Reasoning X h").unwrap();
        assert_eq!(items.len(), 4);
    }

    #[test]
    fn partial_model_query_stays_in_model_phase() {
        let mut state = ModelState::default();
        let (id, info) = model_with_reasoning("reasoning-x", "Reasoning X");
        state.available.insert(id, info);

        let cmd = ModelCommand;
        let ctx = AppCtx {
            models: &state,
            cwd: std::path::Path::new("."),
            has_session_announcements: false,
            billing_surface_visible: true,
            workflows_available: true,
            saved_workflows: &[],
            workflow_runs: &[],
            screen_mode: crate::app::ScreenMode::Fullscreen,
            current_title: None,
        };
        // No trailing space, user is still typing the model name.
        let items = cmd.suggest_args(&ctx, "Reason").unwrap();
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].insert_text, "Reasoning X ");
    }

    #[test]
    fn run_parses_model_plus_effort_when_supported() {
        let mut state = ModelState::default();
        let (id, info) = model_with_reasoning("reasoning-x", "Reasoning X");
        state.available.insert(id, info);
        let mut ctx = dummy_exec_ctx(&state);
        let result = ModelCommand.run(&mut ctx, "Reasoning X xhigh");
        match result {
            CommandResult::Action(Action::SwitchModel {
                model_id, effort, ..
            }) => {
                assert_eq!(model_id.0.as_ref(), "reasoning-x");
                assert_eq!(effort, Some(ReasoningEffort::Xhigh));
            }
            other => panic!("expected SwitchModel with effort, got {other:?}"),
        }
    }

    #[test]
    fn run_rejects_unoffered_effort_with_effort_error_not_unknown_model() {
        // Regression: previously `resolve_effort_token_for` returned None and
        // the handler fell through to `Unknown model: Reasoning X none`.
        let mut state = ModelState::default();
        let (id, info) = model_with_reasoning("reasoning-x", "Reasoning X");
        state.available.insert(id, info);
        let mut ctx = dummy_exec_ctx(&state);
        let result = ModelCommand.run(&mut ctx, "Reasoning X none");
        match result {
            CommandResult::Error(msg) => {
                assert!(
                    msg.contains("unknown effort level 'none'"),
                    "expected effort error, got {msg}"
                );
                assert!(
                    msg.contains("use one of:"),
                    "expected offered levels in message, got {msg}"
                );
                assert!(
                    !msg.to_lowercase().contains("unknown model"),
                    "must not misreport as unknown model: {msg}"
                );
                let offered = msg.split_once("; ").map(|(_, r)| r).unwrap_or("");
                assert!(
                    !offered.contains("none"),
                    "must not list none as offered: {msg}"
                );
            }
            other => panic!("expected Error, got {other:?}"),
        }
    }

    #[test]
    fn run_prefers_full_multi_word_model_name_over_prefix_plus_effort() {
        // Catalog has both "Grok" (reasoning) and "Grok 4.5". `/model Grok 4.5`
        // must select the full name, not treat "4.5" as an effort on "Grok".
        let mut state = ModelState::default();
        let (short_id, short_info) = model_with_reasoning("grok", "Grok");
        let (long_id, long_info) = model_with_reasoning("grok-4.5", "Grok 4.5");
        state.available.insert(short_id, short_info);
        state.available.insert(long_id.clone(), long_info);
        let mut ctx = dummy_exec_ctx(&state);
        let result = ModelCommand.run(&mut ctx, "Grok 4.5");
        match result {
            CommandResult::Action(Action::SetDefaultModel(resolved_id)) => {
                assert_eq!(resolved_id, long_id);
            }
            other => panic!("expected SetDefaultModel(Grok 4.5), got {other:?}"),
        }
    }

    #[test]
    fn run_rejects_effort_for_non_reasoning_model() {
        let mut state = ModelState::default();
        let (id, info) = plain_model("grok-4.5", "Grok 4.5");
        state.available.insert(id, info);
        let mut ctx = dummy_exec_ctx(&state);
        let result = ModelCommand.run(&mut ctx, "Grok 4.5 high");
        // Falls through to "is the whole string a model name?" — which
        // it isn't, so we get an Unknown error.
        assert!(matches!(result, CommandResult::Error(_)));
    }

    /// The bare `/model <name>` form dispatches
    /// `Action::SetDefaultModel(<ModelId>)` instead of the legacy
    /// `Action::SwitchModel { effort: None }`. The dispatcher routes
    /// the typed setter through both `Effect::SwitchModel`
    /// (session-level mutation) AND `Effect::PersistSetting`
    /// (next-session default).
    ///
    /// The payload is the typed `acp::ModelId` (resolved at the slash
    /// boundary), not a String.
    #[test]
    fn run_bare_model_name_dispatches_set_default_model() {
        let mut state = ModelState::default();
        let (id, info) = plain_model("grok-4.5", "Grok 4.5");
        state.available.insert(id.clone(), info);
        let mut ctx = dummy_exec_ctx(&state);
        let result = ModelCommand.run(&mut ctx, "Grok 4.5");
        match result {
            CommandResult::Action(Action::SetDefaultModel(resolved_id)) => {
                assert_eq!(resolved_id, id);
            }
            other => panic!("expected Action::SetDefaultModel(<id>), got {other:?}"),
        }
    }

    /// Case-insensitive matching against the catalog: `/model grok 4.5`
    /// resolves to the same `ModelId` as `/model Grok 4.5`.
    #[test]
    fn run_set_default_model_resolves_case_insensitively() {
        let mut state = ModelState::default();
        let (id, info) = plain_model("grok-4.5", "Grok 4.5");
        state.available.insert(id.clone(), info);
        let mut ctx = dummy_exec_ctx(&state);
        let result = ModelCommand.run(&mut ctx, "grok 4.5");
        match result {
            CommandResult::Action(Action::SetDefaultModel(resolved_id)) => {
                assert_eq!(resolved_id, id);
            }
            other => panic!("expected Action::SetDefaultModel(<id>), got {other:?}"),
        }
    }
}

use toml::Value as TomlValue;
use xai_grok_sampling_types::ReasoningEffort;

/// Pasted logs or files can make the user message huge; its opening carries the ask.
const TURN_SUMMARY_USER_MESSAGE_MAX_CHARS_DEFAULT: usize = 4_000;
/// Conclusions come at the end of a reply, so a long reply keeps its tail.
const TURN_SUMMARY_AGENT_REPLY_MAX_CHARS_DEFAULT: usize = 32_000;
const TURN_SUMMARY_MESSAGE_MAX_CHARS_MIN: usize = 200;
const TURN_SUMMARY_MESSAGE_MAX_CHARS_MAX: usize = 200_000;
const TURN_SUMMARY_TIMEOUT_MS_MIN: u64 = 1_000;
const TURN_SUMMARY_TIMEOUT_MS_DEFAULT: u64 = 45_000;
const TURN_SUMMARY_TIMEOUT_MS_MAX: u64 = 120_000;

/// Sampling settings for the per-turn dashboard summary.
/// Disk config wins per field over remote `turn_summary_config`; only the user layer may select a model.
/// The on/off gate stays the `turn_summary` feature.
#[derive(Clone, Debug, Default, PartialEq, serde::Serialize, serde::Deserialize)]
#[serde(default)]
pub struct TurnSummaryConfig {
    /// A local model choice may select another provider. Remote model choices are ignored.
    pub model: Option<String>,
    /// Applied when the chosen model supports it; otherwise the auxiliary default stays.
    pub reasoning_effort: Option<ReasoningEffort>,
    pub user_message_max_chars: Option<usize>,
    pub agent_reply_max_chars: Option<usize>,
    pub timeout_ms: Option<u64>,
}

/// [`TurnSummaryConfig`] with defaults applied and limits clamped.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct TurnSummarySettings {
    pub(crate) model: Option<String>,
    pub(crate) reasoning_effort: Option<ReasoningEffort>,
    pub(crate) user_message_max_chars: usize,
    pub(crate) agent_reply_max_chars: usize,
    pub(crate) timeout: std::time::Duration,
}

impl TurnSummaryConfig {
    pub(crate) fn settings(&self) -> TurnSummarySettings {
        let chars = |value: Option<usize>, default| {
            value.unwrap_or(default).clamp(
                TURN_SUMMARY_MESSAGE_MAX_CHARS_MIN,
                TURN_SUMMARY_MESSAGE_MAX_CHARS_MAX,
            )
        };
        TurnSummarySettings {
            model: self.model.clone().filter(|m| !m.trim().is_empty()),
            reasoning_effort: self.reasoning_effort,
            user_message_max_chars: chars(
                self.user_message_max_chars,
                TURN_SUMMARY_USER_MESSAGE_MAX_CHARS_DEFAULT,
            ),
            agent_reply_max_chars: chars(
                self.agent_reply_max_chars,
                TURN_SUMMARY_AGENT_REPLY_MAX_CHARS_DEFAULT,
            ),
            timeout: std::time::Duration::from_millis(
                self.timeout_ms
                    .unwrap_or(TURN_SUMMARY_TIMEOUT_MS_DEFAULT)
                    .clamp(TURN_SUMMARY_TIMEOUT_MS_MIN, TURN_SUMMARY_TIMEOUT_MS_MAX),
            ),
        }
    }
}

/// Malformed payloads are ignored so remote settings are applied atomically.
fn coerce_turn_summary_json(value: serde_json::Value) -> Option<TurnSummaryConfig> {
    match serde_json::from_value(value) {
        Ok(cfg) => Some(cfg),
        Err(e) => {
            tracing::warn!(error = %e, "[turn_summary]: dropped malformed remote payload");
            None
        }
    }
}

static REMOTE_TURN_SUMMARY_CONFIG: std::sync::RwLock<Option<TurnSummaryConfig>> =
    std::sync::RwLock::new(None);

/// Call wherever `RemoteSettings` is applied.
pub fn cache_remote_turn_summary_config(value: Option<serde_json::Value>) {
    let coerced = value.and_then(coerce_turn_summary_json);
    if let Ok(mut guard) = REMOTE_TURN_SUMMARY_CONFIG.write() {
        *guard = coerced;
    }
}

fn turn_summary_config_from_toml(v: Option<&TomlValue>) -> Option<TurnSummaryConfig> {
    let table = v?.get("turn_summary")?.clone();
    table
        .try_into()
        .map_err(|e| tracing::warn!(error = %e, "[turn_summary]: dropped malformed local table"))
        .ok()
}

fn merge_turn_summary_config(
    config: TurnSummaryConfig,
    remote: TurnSummaryConfig,
) -> TurnSummaryConfig {
    TurnSummaryConfig {
        // Remote settings cannot opt the user into cross-provider export.
        model: config.model,
        reasoning_effort: config.reasoning_effort.or(remote.reasoning_effort),
        user_message_max_chars: config
            .user_message_max_chars
            .or(remote.user_message_max_chars),
        agent_reply_max_chars: config
            .agent_reply_max_chars
            .or(remote.agent_reply_max_chars),
        timeout_ms: config.timeout_ms.or(remote.timeout_ms),
    }
}

fn turn_summary_config_from_layers(layers: &crate::config::ConfigLayers) -> TurnSummaryConfig {
    let mut config =
        turn_summary_config_from_toml(Some(&layers.effective_config_base_without_overlay()))
            .unwrap_or_default();
    // Managed/requirements layers can come from outside the user. Only
    // the user's explicit config can opt into a cross-provider model.
    config.model = turn_summary_config_from_toml(Some(&layers.user)).and_then(|user| user.model);
    config
}

/// Overlay-free disk config merged over the cached remote config, read at call time.
pub(crate) fn resolve_turn_summary_settings_from_disk() -> TurnSummarySettings {
    let config = crate::config::ConfigLayers::load()
        .map(|layers| turn_summary_config_from_layers(&layers))
        .unwrap_or_default();
    let remote = REMOTE_TURN_SUMMARY_CONFIG
        .read()
        .ok()
        .and_then(|g| g.clone())
        .unwrap_or_default();
    merge_turn_summary_config(config, remote).settings()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_fields_win_over_remote_and_defaults_fill_the_rest() {
        let local: TurnSummaryConfig = toml::from_str::<TomlValue>(
            "[turn_summary]\nmodel = \"grok-4.20\"\ntimeout_ms = 999999",
        )
        .ok()
        .and_then(|v| turn_summary_config_from_toml(Some(&v)))
        .unwrap();
        let remote = coerce_turn_summary_json(serde_json::json!({
            "model": "remote-model",
            "reasoning_effort": "medium",
        }))
        .unwrap();
        let settings = merge_turn_summary_config(local, remote).settings();
        assert_eq!(settings.model.as_deref(), Some("grok-4.20"));
        assert_eq!(settings.reasoning_effort, Some(ReasoningEffort::Medium));
        assert_eq!(
            settings.timeout,
            std::time::Duration::from_millis(TURN_SUMMARY_TIMEOUT_MS_MAX)
        );
        assert_eq!(
            settings.agent_reply_max_chars,
            TURN_SUMMARY_AGENT_REPLY_MAX_CHARS_DEFAULT
        );
        assert_eq!(
            coerce_turn_summary_json(serde_json::json!({"timeout_ms": "x"})),
            None
        );
    }
    #[test]
    fn remote_model_never_authorizes_export_and_defaults_preserve_route() {
        let remote = TurnSummaryConfig {
            model: Some("other-provider".to_owned()),
            ..Default::default()
        };
        let settings = merge_turn_summary_config(TurnSummaryConfig::default(), remote).settings();
        assert_eq!(settings.model, None);
        assert_eq!(settings.reasoning_effort, None);
    }
    #[test]
    fn only_user_config_layer_can_select_model() {
        let mut layers = crate::config::ConfigLayers::default();
        layers.managed =
            toml::from_str("[turn_summary]\nmodel = \"managed-model\"\ntimeout_ms = 2000").unwrap();
        let config = turn_summary_config_from_layers(&layers);
        assert_eq!(config.model, None);
        assert_eq!(config.timeout_ms, Some(2000));
        layers.user = toml::from_str("[turn_summary]\nmodel = \"user-model\"").unwrap();
        let config = turn_summary_config_from_layers(&layers);
        assert_eq!(config.model.as_deref(), Some("user-model"));
    }
}

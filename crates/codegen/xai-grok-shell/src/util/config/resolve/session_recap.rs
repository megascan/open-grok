use toml::Value as TomlValue;
use xai_grok_sampling_types::ReasoningEffort;

/// Pasted logs or files can make a user message huge; its opening carries the ask.
const SESSION_RECAP_USER_MESSAGE_MAX_CHARS_DEFAULT: usize = 2_000;
/// Conclusions come at the end of a reply, so a long reply keeps its tail.
const SESSION_RECAP_AGENT_REPLY_MAX_CHARS_DEFAULT: usize = 4_000;
const SESSION_RECAP_MESSAGE_MAX_CHARS_MIN: usize = 200;
const SESSION_RECAP_MESSAGE_MAX_CHARS_MAX: usize = 200_000;
/// About 12k tokens at the bytes/4 estimate; the most recent messages win.
const SESSION_RECAP_TRANSCRIPT_MAX_CHARS_DEFAULT: usize = 48_000;
const SESSION_RECAP_TRANSCRIPT_MAX_CHARS_MIN: usize = 1_000;
const SESSION_RECAP_TRANSCRIPT_MAX_CHARS_MAX: usize = 400_000;
const SESSION_RECAP_TIMEOUT_MS_MIN: u64 = 1_000;
const SESSION_RECAP_TIMEOUT_MS_DEFAULT: u64 = 45_000;
const SESSION_RECAP_TIMEOUT_MS_MAX: u64 = 120_000;

/// Sampling settings for the session recap.
/// Disk config wins per field over remote `session_recap_config`; only the user layer may select a model.
/// The on/off gate stays the `session_recap` feature.
#[derive(Clone, Debug, Default, PartialEq, serde::Serialize, serde::Deserialize)]
#[serde(default)]
pub struct SessionRecapConfig {
    /// A local model choice may select another provider. Remote model choices are ignored.
    pub model: Option<String>,
    /// Applied when the chosen model supports it; otherwise the auxiliary default stays.
    pub reasoning_effort: Option<ReasoningEffort>,
    pub user_message_max_chars: Option<usize>,
    pub agent_reply_max_chars: Option<usize>,
    pub transcript_max_chars: Option<usize>,
    pub timeout_ms: Option<u64>,
}

/// [`SessionRecapConfig`] with defaults applied and limits clamped.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct SessionRecapSettings {
    pub(crate) model: Option<String>,
    /// Legacy user preference; it grants cross-provider use only when it is
    /// also the effective model selected by the models manager.
    pub(crate) user_legacy_model: Option<String>,
    pub(crate) reasoning_effort: Option<ReasoningEffort>,
    pub(crate) user_message_max_chars: usize,
    pub(crate) agent_reply_max_chars: usize,
    pub(crate) transcript_max_chars: usize,
    pub(crate) timeout: std::time::Duration,
}

impl SessionRecapConfig {
    pub(crate) fn settings(&self) -> SessionRecapSettings {
        let chars = |value: Option<usize>, default| {
            value.unwrap_or(default).clamp(
                SESSION_RECAP_MESSAGE_MAX_CHARS_MIN,
                SESSION_RECAP_MESSAGE_MAX_CHARS_MAX,
            )
        };
        SessionRecapSettings {
            model: self.model.clone().filter(|m| !m.trim().is_empty()),
            user_legacy_model: None,
            reasoning_effort: self.reasoning_effort,
            user_message_max_chars: chars(
                self.user_message_max_chars,
                SESSION_RECAP_USER_MESSAGE_MAX_CHARS_DEFAULT,
            ),
            agent_reply_max_chars: chars(
                self.agent_reply_max_chars,
                SESSION_RECAP_AGENT_REPLY_MAX_CHARS_DEFAULT,
            ),
            transcript_max_chars: self
                .transcript_max_chars
                .unwrap_or(SESSION_RECAP_TRANSCRIPT_MAX_CHARS_DEFAULT)
                .clamp(
                    SESSION_RECAP_TRANSCRIPT_MAX_CHARS_MIN,
                    SESSION_RECAP_TRANSCRIPT_MAX_CHARS_MAX,
                ),
            timeout: std::time::Duration::from_millis(
                self.timeout_ms
                    .unwrap_or(SESSION_RECAP_TIMEOUT_MS_DEFAULT)
                    .clamp(SESSION_RECAP_TIMEOUT_MS_MIN, SESSION_RECAP_TIMEOUT_MS_MAX),
            ),
        }
    }
}

impl SessionRecapSettings {
    /// Capture the user-authorized model itself, not just an authorization bit:
    /// route preparation awaits and the manager's configured model may change.
    pub(crate) fn trusted_model_override(
        &self,
        effective_legacy_model: Option<&str>,
    ) -> Option<&str> {
        self.model.as_deref().or_else(|| {
            self.user_legacy_model.as_deref().filter(|user| {
                effective_legacy_model.is_some_and(|effective| *user == effective.trim())
            })
        })
    }
}

/// Malformed payloads are ignored so remote settings are applied atomically.
fn coerce_session_recap_json(value: serde_json::Value) -> Option<SessionRecapConfig> {
    match serde_json::from_value(value) {
        Ok(cfg) => Some(cfg),
        Err(e) => {
            tracing::warn!(error = %e, "[session_recap]: dropped malformed remote payload");
            None
        }
    }
}

static REMOTE_SESSION_RECAP_CONFIG: std::sync::RwLock<Option<SessionRecapConfig>> =
    std::sync::RwLock::new(None);

/// Call wherever `RemoteSettings` is applied.
pub fn cache_remote_session_recap_config(value: Option<serde_json::Value>) {
    let coerced = value.and_then(coerce_session_recap_json);
    if let Ok(mut guard) = REMOTE_SESSION_RECAP_CONFIG.write() {
        *guard = coerced;
    }
}

fn session_recap_config_from_toml(v: Option<&TomlValue>) -> Option<SessionRecapConfig> {
    let table = v?.get("session_recap")?.clone();
    table
        .try_into()
        .map_err(|e| tracing::warn!(error = %e, "[session_recap]: dropped malformed local table"))
        .ok()
}

fn merge_session_recap_config(
    config: SessionRecapConfig,
    remote: SessionRecapConfig,
) -> SessionRecapConfig {
    SessionRecapConfig {
        // Remote settings cannot opt the user into cross-provider export.
        model: config.model,
        reasoning_effort: config.reasoning_effort.or(remote.reasoning_effort),
        user_message_max_chars: config
            .user_message_max_chars
            .or(remote.user_message_max_chars),
        agent_reply_max_chars: config
            .agent_reply_max_chars
            .or(remote.agent_reply_max_chars),
        transcript_max_chars: config.transcript_max_chars.or(remote.transcript_max_chars),
        timeout_ms: config.timeout_ms.or(remote.timeout_ms),
    }
}

fn session_recap_config_from_layers(layers: &crate::config::ConfigLayers) -> SessionRecapConfig {
    let mut config =
        session_recap_config_from_toml(Some(&layers.effective_config_base_without_overlay()))
            .unwrap_or_default();
    // Managed/requirements layers can come from outside the user. Only
    // the user's explicit config can opt into a cross-provider model.
    config.model = session_recap_config_from_toml(Some(&layers.user)).and_then(|user| user.model);
    config
}

fn session_recap_settings_from_layers(
    layers: Option<&crate::config::ConfigLayers>,
    remote: SessionRecapConfig,
) -> SessionRecapSettings {
    let config = layers
        .map(session_recap_config_from_layers)
        .unwrap_or_default();
    let mut settings = merge_session_recap_config(config, remote).settings();
    settings.user_legacy_model = layers.and_then(|layers| {
        layers
            .user
            .get("models")?
            .get("recap")?
            .as_str()
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .map(str::to_owned)
    });
    settings
}

/// Overlay-free disk config merged over the cached remote config, read at call time.
pub(crate) fn resolve_session_recap_settings_from_disk() -> SessionRecapSettings {
    let layers = crate::config::ConfigLayers::load().ok();
    let remote = REMOTE_SESSION_RECAP_CONFIG
        .read()
        .ok()
        .and_then(|g| g.clone())
        .unwrap_or_default();
    session_recap_settings_from_layers(layers.as_ref(), remote)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_fields_win_over_remote_and_defaults_fill_the_rest() {
        let local: SessionRecapConfig = toml::from_str::<TomlValue>(
            "[session_recap]\nmodel = \"grok-4.20\"\ntimeout_ms = 999999\ntranscript_max_chars = 10",
        )
        .ok()
        .and_then(|v| session_recap_config_from_toml(Some(&v)))
        .unwrap();
        let remote = coerce_session_recap_json(serde_json::json!({
            "model": "remote-model",
            "reasoning_effort": "medium",
            "user_message_max_chars": 3_000,
        }))
        .unwrap();
        let settings = merge_session_recap_config(local, remote).settings();
        assert_eq!(settings.model.as_deref(), Some("grok-4.20"));
        assert_eq!(settings.reasoning_effort, Some(ReasoningEffort::Medium));
        assert_eq!(settings.user_message_max_chars, 3_000);
        assert_eq!(
            settings.agent_reply_max_chars,
            SESSION_RECAP_AGENT_REPLY_MAX_CHARS_DEFAULT
        );
        assert_eq!(
            settings.transcript_max_chars,
            SESSION_RECAP_TRANSCRIPT_MAX_CHARS_MIN
        );
        assert_eq!(
            settings.timeout,
            std::time::Duration::from_millis(SESSION_RECAP_TIMEOUT_MS_MAX)
        );
        assert_eq!(
            coerce_session_recap_json(serde_json::json!({"timeout_ms": "x"})),
            None
        );
    }
    #[test]
    fn remote_model_never_authorizes_export_and_defaults_preserve_route() {
        let remote = SessionRecapConfig {
            model: Some("other-provider".to_owned()),
            ..Default::default()
        };
        let settings = merge_session_recap_config(SessionRecapConfig::default(), remote).settings();
        assert_eq!(settings.model, None);
        assert_eq!(settings.reasoning_effort, None);
    }
    #[test]
    fn only_user_config_layer_can_select_model() {
        let mut layers = crate::config::ConfigLayers::default();
        layers.managed =
            toml::from_str("[session_recap]\nmodel = \"managed-model\"\ntimeout_ms = 2000")
                .unwrap();
        let config = session_recap_config_from_layers(&layers);
        assert_eq!(config.model, None);
        assert_eq!(config.timeout_ms, Some(2000));
        layers.user = toml::from_str("[session_recap]\nmodel = \"user-model\"").unwrap();
        let config = session_recap_config_from_layers(&layers);
        assert_eq!(config.model.as_deref(), Some("user-model"));
    }

    #[test]
    fn legacy_settings_model_requires_user_provenance_and_captures_the_route() {
        let mut layers = crate::config::ConfigLayers::default();
        layers.managed = toml::from_str("[models]\nrecap = \"managed-model\"").unwrap();
        let remote = SessionRecapConfig {
            model: Some("remote-model".into()),
            ..Default::default()
        };
        let settings = session_recap_settings_from_layers(Some(&layers), remote.clone());
        assert_eq!(settings.trusted_model_override(Some("managed-model")), None);
        // A project-selected manager model without a matching user choice is not consent.
        assert_eq!(settings.trusted_model_override(Some("project-model")), None);
        assert_eq!(settings.trusted_model_override(Some("remote-model")), None);

        layers.user = toml::from_str("[models]\nrecap = \"user-model\"").unwrap();
        let settings = session_recap_settings_from_layers(Some(&layers), remote);
        assert_eq!(
            settings.trusted_model_override(Some("user-model")),
            Some("user-model")
        );
        assert_eq!(settings.trusted_model_override(Some("project-model")), None);
        assert_eq!(settings.trusted_model_override(Some("managed-model")), None);
        assert_eq!(settings.trusted_model_override(None), None);

        layers.user = toml::from_str(
            "[models]\nrecap = \"user-model\"\n[session_recap]\nmodel = \"explicit-model\"",
        )
        .unwrap();
        let settings =
            session_recap_settings_from_layers(Some(&layers), SessionRecapConfig::default());
        assert_eq!(
            settings.trusted_model_override(Some("project-model")),
            Some("explicit-model")
        );
    }
}

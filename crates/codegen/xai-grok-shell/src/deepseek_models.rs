//! Provider-isolated DeepSeek direct model discovery.
//!
//! DeepSeek serves an OpenAI-compatible Chat Completions API. The provider's
//! `/models` endpoint is authoritative for availability: every text-in/text-out
//! model it lists enters the DeepSeek partition, with its live name, context
//! window, output cap, and `effort` levels. Curated entries only supply what
//! the endpoint does not describe (the Responses backend and hosted search for
//! V4 Flash, and limits when an older payload omits them). Uncurated ids use
//! plain Chat Completions.
//!
//! See <https://api-docs.deepseek.com/api/list-models>.

use crate::agent::config::{EnvKeys, ModelEntry, ModelInfo};
use anyhow::{Context, anyhow};
use indexmap::IndexMap;
use serde::Deserialize;
use std::num::NonZeroU64;
use std::time::Duration;
use url::Url;
use xai_grok_sampling_types::{
    ApiBackend, ModelProvider, ReasoningEffort, ReasoningEffortOption, ToolMode,
};

pub const DEEPSEEK_API_BASE_URL: &str = "https://api.deepseek.com";
pub const DEEPSEEK_API_BASE_URL_ENV: &str = "OPENGROK_DEEPSEEK_API_BASE_URL";
pub const DEEPSEEK_API_KEY_ENV: &str = "DEEPSEEK_API_KEY";
const DEEPSEEK_MODELS_REQUEST_TIMEOUT: Duration = Duration::from_secs(10);
/// Context window for an uncurated id whose payload omits `context_window`.
const DEEPSEEK_FALLBACK_CONTEXT_WINDOW: u64 = 128_000;

#[derive(Clone, Copy, Debug)]
pub struct CuratedDeepSeekModel {
    pub key: &'static str,
    pub slug: &'static str,
    pub name: &'static str,
    pub description: &'static str,
    pub context_window: u64,
    pub max_output_tokens: u32,
    pub api_backend: ApiBackend,
    pub supports_backend_search: bool,
}

pub const CURATED_DEEPSEEK_MODELS: [CuratedDeepSeekModel; 2] = [
    CuratedDeepSeekModel {
        key: "deepseek:deepseek-v4-pro",
        slug: "deepseek-v4-pro",
        name: "DeepSeek V4 Pro",
        description: "DeepSeek V4 Pro through the direct DeepSeek API",
        context_window: 1_000_000,
        max_output_tokens: 384_000,
        api_backend: ApiBackend::ChatCompletions,
        supports_backend_search: false,
    },
    CuratedDeepSeekModel {
        key: "deepseek:deepseek-v4-flash",
        slug: "deepseek-v4-flash",
        name: "DeepSeek V4 Flash 0731",
        description: "DeepSeek-V4-Flash-0731 through the direct DeepSeek Responses API",
        context_window: 1_000_000,
        max_output_tokens: 384_000,
        api_backend: ApiBackend::Responses,
        supports_backend_search: true,
    },
];

pub fn is_trusted_api_base_url(base_url: &str) -> bool {
    let Ok(url) = Url::parse(base_url) else {
        return false;
    };
    url.scheme() == "https" && url.host_str() == Some("api.deepseek.com")
}

pub fn api_base_url() -> String {
    std::env::var(DEEPSEEK_API_BASE_URL_ENV)
        .ok()
        .map(|value| value.trim().trim_end_matches('/').to_owned())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| DEEPSEEK_API_BASE_URL.to_owned())
}

fn environment_api_key() -> Option<String> {
    std::env::var(DEEPSEEK_API_KEY_ENV)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

pub fn environment_api_key_is_configured() -> bool {
    environment_api_key().is_some()
}

fn stored_api_key() -> Option<String> {
    crate::auth::read_provider_api_key(
        &crate::util::grok_home::grok_home(),
        ModelProvider::DeepSeek,
    )
}

fn select_api_key(
    base_url: &str,
    environment_key: Option<String>,
    stored_key: Option<String>,
) -> Option<String> {
    environment_key.or_else(|| {
        is_trusted_api_base_url(base_url)
            .then_some(stored_key)
            .flatten()
    })
}

fn api_key_for_base_url(base_url: &str) -> Option<String> {
    select_api_key(base_url, environment_api_key(), stored_api_key())
}

fn credential_fingerprint(api_key: &str) -> String {
    blake3::hash(api_key.as_bytes()).to_hex().to_string()
}

fn curated_model_entry(curated: &CuratedDeepSeekModel, base_url: &str) -> ModelEntry {
    let mut info = ModelInfo::fallback(curated.key);
    info.id = Some(curated.key.to_owned());
    info.model = curated.slug.to_owned();
    info.base_url = base_url.trim_end_matches('/').to_owned();
    info.name = Some(curated.name.to_owned());
    info.description = Some(curated.description.to_owned());
    info.max_completion_tokens = Some(curated.max_output_tokens);
    info.api_backend = curated.api_backend;
    info.provider = ModelProvider::DeepSeek;
    info.tool_mode = Some(ToolMode::Direct);
    info.context_window =
        NonZeroU64::new(curated.context_window).expect("non-zero DeepSeek context window");
    info.supports_reasoning_effort = true;
    info.reasoning_efforts = deepseek_reasoning_efforts();
    info.reasoning_effort = Some(ReasoningEffort::High);
    info.supports_backend_search = curated.supports_backend_search;
    info.supported_in_api = true;
    ModelEntry {
        info,
        api_key: None,
        env_key: Some(EnvKeys::single(DEEPSEEK_API_KEY_ENV)),
        auth_provider: None,
        api_base_url: None,
    }
}

#[derive(Clone, Debug)]
pub(crate) struct DeepSeekModelsCatalog {
    entries: IndexMap<String, ModelEntry>,
    credential_fingerprint: String,
}

impl DeepSeekModelsCatalog {
    pub(crate) fn entries(&self) -> IndexMap<String, ModelEntry> {
        self.entries.clone()
    }

    pub(crate) fn is_authoritative(&self) -> bool {
        !self.entries.is_empty()
    }
}

#[derive(Clone, Debug)]
pub(crate) struct DeepSeekModelsClient {
    http: crate::lazy_http::LazyHttpClient,
    base_url: String,
}

impl DeepSeekModelsClient {
    pub(crate) fn new() -> Self {
        Self {
            http: crate::lazy_http::LazyHttpClient::new(),
            base_url: api_base_url(),
        }
    }

    #[cfg(test)]
    fn with_base_url(base_url: impl Into<String>) -> Self {
        Self {
            http: crate::lazy_http::LazyHttpClient::new(),
            base_url: base_url.into(),
        }
    }

    pub(crate) async fn query(&self) -> anyhow::Result<Option<DeepSeekModelsCatalog>> {
        let Some(api_key) = api_key_for_base_url(&self.base_url) else {
            return Ok(None);
        };
        self.query_with_key(&api_key).await.map(Some)
    }

    pub(crate) fn has_usable_api_key(&self) -> bool {
        api_key_for_base_url(&self.base_url).is_some()
    }

    pub(crate) fn catalog_matches_current_credential(
        &self,
        catalog: &DeepSeekModelsCatalog,
    ) -> bool {
        api_key_for_base_url(&self.base_url)
            .map(|key| credential_fingerprint(&key))
            .is_some_and(|fingerprint| fingerprint == catalog.credential_fingerprint)
    }

    async fn query_with_key(&self, api_key: &str) -> anyhow::Result<DeepSeekModelsCatalog> {
        let url = format!("{}/models", self.base_url.trim_end_matches('/'));
        let response = self
            .http
            .client()
            .get(&url)
            .timeout(DEEPSEEK_MODELS_REQUEST_TIMEOUT)
            .bearer_auth(api_key)
            .send()
            .await
            .with_context(|| format!("DeepSeek models request to {url} failed"))?;
        let status = response.status();
        if !status.is_success() {
            let body = response.text().await.unwrap_or_default();
            return Err(anyhow!(
                "DeepSeek models request returned {status}: {}",
                safe_error_excerpt(&body, api_key)
            ));
        }
        let wire: DeepSeekModelsResponse = response
            .json()
            .await
            .context("DeepSeek models response was invalid")?;
        Ok(self.catalog_from_wire(wire, api_key))
    }

    fn catalog_from_wire(
        &self,
        wire: DeepSeekModelsResponse,
        api_key: &str,
    ) -> DeepSeekModelsCatalog {
        let mut entries = IndexMap::new();
        for model in wire.data {
            let id = model.id.trim();
            if id.is_empty() {
                continue;
            }
            if let Some(reason) = skip_reason(&model) {
                tracing::debug!(model = id, reason, "skipping DeepSeek model");
                continue;
            }
            let (key, entry) = live_model_entry(id, &model, &self.base_url);
            entries.entry(key).or_insert(entry);
        }
        DeepSeekModelsCatalog {
            entries,
            credential_fingerprint: credential_fingerprint(api_key),
        }
    }
}

/// Agent turns need text in and text out. Absent modality lists come from
/// older payloads and are treated as text-only.
fn skip_reason(model: &DeepSeekWireModel) -> Option<&'static str> {
    let has_text = |modalities: &[String]| {
        modalities.is_empty()
            || modalities
                .iter()
                .any(|modality| modality.trim().eq_ignore_ascii_case("text"))
    };
    if !has_text(&model.input_modalities) {
        return Some("no text input");
    }
    if !has_text(&model.output_modalities) {
        return Some("no text output");
    }
    None
}

fn live_model_entry(id: &str, wire: &DeepSeekWireModel, base_url: &str) -> (String, ModelEntry) {
    let curated = CURATED_DEEPSEEK_MODELS
        .iter()
        .find(|curated| curated.slug == id);
    let mut entry = match curated {
        Some(curated) => curated_model_entry(curated, base_url),
        None => uncurated_model_entry(id, base_url),
    };
    let info = &mut entry.info;
    if let Some(name) = wire
        .name
        .as_deref()
        .map(str::trim)
        .filter(|name| !name.is_empty())
    {
        info.name = Some(name.to_owned());
        if curated.is_none() {
            info.description = Some(format!("{name} through the direct DeepSeek API"));
        }
    }
    if let Some(context_window) = wire.context_window.and_then(NonZeroU64::new) {
        info.context_window = context_window;
    }
    if let Some(max_output_tokens) = wire.max_output_tokens.filter(|tokens| *tokens > 0) {
        info.max_completion_tokens = Some(max_output_tokens);
    }
    if let Some(effort) = wire.effort.as_ref() {
        let options = reasoning_efforts_from_wire(effort);
        info.supports_reasoning_effort = !options.is_empty();
        info.reasoning_effort = options
            .iter()
            .find(|option| option.default)
            .map(|option| option.value);
        info.reasoning_efforts = options;
    }
    let key = info.id.clone().unwrap_or_else(|| format!("deepseek:{id}"));
    (key, entry)
}

fn uncurated_model_entry(id: &str, base_url: &str) -> ModelEntry {
    let key = format!("deepseek:{id}");
    let mut info = ModelInfo::fallback(&key);
    info.id = Some(key);
    info.model = id.to_owned();
    info.base_url = base_url.trim_end_matches('/').to_owned();
    info.name = Some(id.to_owned());
    info.description = Some(format!("{id} through the direct DeepSeek API"));
    info.api_backend = ApiBackend::ChatCompletions;
    info.provider = ModelProvider::DeepSeek;
    info.tool_mode = Some(ToolMode::Direct);
    info.context_window = NonZeroU64::new(DEEPSEEK_FALLBACK_CONTEXT_WINDOW)
        .expect("non-zero DeepSeek fallback context window");
    info.supports_backend_search = false;
    info.supported_in_api = true;
    ModelEntry {
        info,
        api_key: None,
        env_key: Some(EnvKeys::single(DEEPSEEK_API_KEY_ENV)),
        auth_provider: None,
        api_base_url: None,
    }
}

/// `effort.supported_levels` lists the values `reasoning_effort` accepts
/// (excluding `none`); `default_level` applies when thinking is on and no
/// level is sent. Unknown levels are skipped rather than guessed, and an
/// unlisted default is not marked.
fn reasoning_efforts_from_wire(effort: &DeepSeekWireEffort) -> Vec<ReasoningEffortOption> {
    let default = effort
        .default_level
        .as_deref()
        .and_then(|level| level.trim().parse::<ReasoningEffort>().ok());
    let mut seen = Vec::new();
    for level in &effort.supported_levels {
        if let Ok(value) = level.trim().parse::<ReasoningEffort>()
            && value != ReasoningEffort::None
            && !seen.contains(&value)
        {
            seen.push(value);
        }
    }
    seen.into_iter()
        .map(|value| ReasoningEffortOption {
            id: value.as_str().to_owned(),
            value,
            label: effort_label(value).to_owned(),
            description: None,
            default: Some(value) == default,
        })
        .collect()
}

fn effort_label(value: ReasoningEffort) -> &'static str {
    match value {
        ReasoningEffort::None => "None",
        ReasoningEffort::Minimal => "Minimal",
        ReasoningEffort::Low => "Low",
        ReasoningEffort::Medium => "Medium",
        ReasoningEffort::High => "High",
        ReasoningEffort::Xhigh => "Xhigh",
        ReasoningEffort::Max => "Max",
        ReasoningEffort::Ultra => "Ultra",
    }
}

fn deepseek_reasoning_efforts() -> Vec<ReasoningEffortOption> {
    [
        (
            ReasoningEffort::Low,
            "Faster responses with lighter reasoning",
            false,
        ),
        (
            ReasoningEffort::High,
            "DeepSeek's default reasoning level",
            true,
        ),
        (
            ReasoningEffort::Max,
            "Maximum reasoning depth for difficult tasks",
            false,
        ),
    ]
    .into_iter()
    .map(|(value, description, default)| ReasoningEffortOption {
        id: value.as_str().to_owned(),
        value,
        label: effort_label(value).to_owned(),
        description: Some(description.to_owned()),
        default,
    })
    .collect()
}

fn safe_error_excerpt(body: &str, api_key: &str) -> String {
    let sanitized = body
        .replace(api_key, "[REDACTED]")
        .replace(['\r', '\n'], " ");
    sanitized.chars().take(512).collect()
}

#[derive(Debug, Deserialize)]
struct DeepSeekModelsResponse {
    #[serde(default)]
    data: Vec<DeepSeekWireModel>,
}

#[derive(Debug, Default, Deserialize)]
struct DeepSeekWireModel {
    id: String,
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    context_window: Option<u64>,
    #[serde(default)]
    max_output_tokens: Option<u32>,
    #[serde(default)]
    input_modalities: Vec<String>,
    #[serde(default)]
    output_modalities: Vec<String>,
    #[serde(default)]
    effort: Option<DeepSeekWireEffort>,
}

#[derive(Debug, Default, Deserialize)]
struct DeepSeekWireEffort {
    #[serde(default)]
    supported_levels: Vec<String>,
    #[serde(default)]
    default_level: Option<String>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{Json, Router, extract::State, http::HeaderMap, routing::get};
    use std::sync::{Arc, Mutex};

    #[test]
    fn trusted_hosts_are_provider_scoped() {
        assert!(is_trusted_api_base_url(DEEPSEEK_API_BASE_URL));
        assert!(is_trusted_api_base_url("https://api.deepseek.com/v1"));
        assert!(!is_trusted_api_base_url("http://api.deepseek.com"));
        assert!(!is_trusted_api_base_url("https://api.x.ai/v1"));
        assert!(!is_trusted_api_base_url("https://deepseek.example/v1"));
    }

    #[test]
    fn stored_keys_never_leave_owned_hosts() {
        let stored = Some("deepseek-stored-secret".to_owned());
        assert_eq!(
            select_api_key(DEEPSEEK_API_BASE_URL, None, stored.clone()).as_deref(),
            Some("deepseek-stored-secret")
        );
        assert_eq!(
            select_api_key("https://proxy.example/v1", None, stored),
            None
        );
        assert_eq!(
            select_api_key(
                "https://proxy.example/v1",
                Some("explicit-environment-secret".to_owned()),
                None,
            )
            .as_deref(),
            Some("explicit-environment-secret")
        );
    }

    #[test]
    fn wire_catalog_keeps_uncurated_ids_and_curated_capabilities() {
        let client = DeepSeekModelsClient::with_base_url(DEEPSEEK_API_BASE_URL);
        let catalog = client.catalog_from_wire(
            DeepSeekModelsResponse {
                data: vec![
                    DeepSeekWireModel {
                        id: "deepseek-v4-pro".to_owned(),
                        ..Default::default()
                    },
                    DeepSeekWireModel {
                        id: "deepseek-v4-flash".to_owned(),
                        ..Default::default()
                    },
                    DeepSeekWireModel {
                        id: "future-unknown".to_owned(),
                        ..Default::default()
                    },
                ],
            },
            "catalog-key",
        );
        let entries = catalog.entries();
        assert_eq!(entries.len(), 3);

        let future = &entries["deepseek:future-unknown"];
        assert_eq!(future.info.model, "future-unknown");
        assert_eq!(future.info.provider, ModelProvider::DeepSeek);
        assert_eq!(future.info.api_backend, ApiBackend::ChatCompletions);
        assert_eq!(
            future.info.context_window.get(),
            DEEPSEEK_FALLBACK_CONTEXT_WINDOW
        );
        assert!(!future.info.supports_backend_search);
        assert!(!future.info.supports_reasoning_effort);

        let pro = &entries["deepseek:deepseek-v4-pro"];
        assert_eq!(pro.info.model, "deepseek-v4-pro");
        assert_eq!(pro.info.provider, ModelProvider::DeepSeek);
        assert_eq!(pro.info.api_backend, ApiBackend::ChatCompletions);
        assert!(!pro.info.supports_backend_search);

        let flash = &entries["deepseek:deepseek-v4-flash"];
        assert_eq!(flash.info.model, "deepseek-v4-flash");
        assert_eq!(flash.info.name.as_deref(), Some("DeepSeek V4 Flash 0731"));
        assert_eq!(flash.info.api_backend, ApiBackend::Responses);
        assert_eq!(flash.info.tool_mode, Some(ToolMode::Direct));
        assert_eq!(flash.info.context_window.get(), 1_000_000);
        assert_eq!(flash.info.max_completion_tokens, Some(384_000));
        assert!(flash.info.supports_backend_search);
        assert!(flash.info.supports_reasoning_effort);
        assert_eq!(flash.info.reasoning_effort, Some(ReasoningEffort::High));
        assert_eq!(
            flash
                .info
                .reasoning_efforts
                .iter()
                .map(|option| option.value)
                .collect::<Vec<_>>(),
            vec![
                ReasoningEffort::Low,
                ReasoningEffort::High,
                ReasoningEffort::Max,
            ]
        );
        assert_eq!(
            flash.env_key.as_ref().and_then(EnvKeys::primary),
            Some(DEEPSEEK_API_KEY_ENV)
        );
    }

    /// The documented `GET /models` payload, including an id with no curated
    /// entry, populates the catalog from live metadata.
    #[test]
    fn documented_payload_populates_live_metadata() {
        let wire: DeepSeekModelsResponse = serde_json::from_value(serde_json::json!({
            "object": "list",
            "data": [
                {
                    "id": "deepseek-flash",
                    "object": "model",
                    "owned_by": "deepseek",
                    "name": "DeepSeek-V4.1-Flash",
                    "context_window": 1_048_576,
                    "max_output_tokens": 393_216,
                    "input_modalities": ["text", "image"],
                    "output_modalities": ["text"],
                    "effort": {
                        "supported_levels": ["low", "high", "max"],
                        "default_level": "high"
                    },
                    "api_capabilities": {
                        "anthropic_messages": {"system_prompt_update": "in-history"}
                    }
                },
                {
                    "id": "deepseek-v4-pro",
                    "object": "model",
                    "owned_by": "deepseek",
                    "name": "DeepSeek-V4-Pro",
                    "context_window": 1_048_576,
                    "max_output_tokens": 393_216,
                    "input_modalities": ["text"],
                    "output_modalities": ["text"],
                    "effort": {"supported_levels": ["low", "high", "max"]}
                },
                {
                    "id": "deepseek-embed",
                    "object": "model",
                    "owned_by": "deepseek",
                    "output_modalities": ["embedding"]
                },
                {
                    "id": "deepseek-listen",
                    "object": "model",
                    "owned_by": "deepseek",
                    "input_modalities": ["audio"],
                    "output_modalities": ["text"]
                }
            ]
        }))
        .expect("documented DeepSeek /models payload deserializes");
        let client = DeepSeekModelsClient::with_base_url(DEEPSEEK_API_BASE_URL);
        let entries = client.catalog_from_wire(wire, "catalog-key").entries();
        assert_eq!(
            entries.keys().map(String::as_str).collect::<Vec<_>>(),
            vec!["deepseek:deepseek-flash", "deepseek:deepseek-v4-pro"]
        );

        let flash = &entries["deepseek:deepseek-flash"];
        assert_eq!(flash.info.model, "deepseek-flash");
        assert_eq!(flash.info.name.as_deref(), Some("DeepSeek-V4.1-Flash"));
        assert_eq!(flash.info.provider, ModelProvider::DeepSeek);
        assert_eq!(flash.info.api_backend, ApiBackend::ChatCompletions);
        assert_eq!(flash.info.context_window.get(), 1_048_576);
        assert_eq!(flash.info.max_completion_tokens, Some(393_216));
        assert!(flash.info.supports_reasoning_effort);
        assert_eq!(flash.info.reasoning_effort, Some(ReasoningEffort::High));
        assert_eq!(
            flash
                .info
                .reasoning_efforts
                .iter()
                .map(|option| (option.value, option.default))
                .collect::<Vec<_>>(),
            vec![
                (ReasoningEffort::Low, false),
                (ReasoningEffort::High, true),
                (ReasoningEffort::Max, false),
            ]
        );
        assert_eq!(
            flash.env_key.as_ref().and_then(EnvKeys::primary),
            Some(DEEPSEEK_API_KEY_ENV)
        );

        // Curated ids keep their key and backend; live limits win, and a menu
        // without `default_level` leaves the effort to DeepSeek.
        let pro = &entries["deepseek:deepseek-v4-pro"];
        assert_eq!(pro.info.name.as_deref(), Some("DeepSeek-V4-Pro"));
        assert_eq!(pro.info.api_backend, ApiBackend::ChatCompletions);
        assert_eq!(pro.info.context_window.get(), 1_048_576);
        assert_eq!(pro.info.max_completion_tokens, Some(393_216));
        assert!(pro.info.supports_reasoning_effort);
        assert_eq!(pro.info.reasoning_effort, None);
    }

    #[test]
    fn wire_effort_levels_skip_none_unknown_and_duplicates() {
        let options = reasoning_efforts_from_wire(&DeepSeekWireEffort {
            supported_levels: vec![
                "none".to_owned(),
                "LOW".to_owned(),
                "turbo".to_owned(),
                "low".to_owned(),
                "max".to_owned(),
            ],
            default_level: Some("medium".to_owned()),
        });
        assert_eq!(
            options
                .iter()
                .map(|option| (option.value, option.default))
                .collect::<Vec<_>>(),
            vec![(ReasoningEffort::Low, false), (ReasoningEffort::Max, false)]
        );
    }

    #[test]
    fn error_excerpt_redacts_a_reflected_credential() {
        let excerpt = safe_error_excerpt(
            "request rejected for model-query-canary\ntry again",
            "model-query-canary",
        );
        assert_eq!(excerpt, "request rejected for [REDACTED] try again");
    }

    #[tokio::test]
    async fn model_query_uses_bearer_auth() {
        #[derive(Clone, Default)]
        struct RequestCapture(Arc<Mutex<Option<String>>>);

        async fn models(
            State(capture): State<RequestCapture>,
            headers: HeaderMap,
        ) -> Json<serde_json::Value> {
            *capture.0.lock().expect("capture lock") = headers
                .get(reqwest::header::AUTHORIZATION)
                .and_then(|value| value.to_str().ok())
                .map(str::to_owned);
            Json(serde_json::json!({
                "object": "list",
                "data": [{"id": "deepseek-v4-flash"}, {"id": "deepseek-flash"}]
            }))
        }

        let capture = RequestCapture::default();
        let app = Router::new()
            .route("/models", get(models))
            .with_state(capture.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

        let client = DeepSeekModelsClient::with_base_url(format!("http://{address}"));
        let catalog = client.query_with_key("model-query-canary").await.unwrap();
        assert!(catalog.entries().contains_key("deepseek:deepseek-v4-flash"));
        assert!(catalog.entries().contains_key("deepseek:deepseek-flash"));
        assert_eq!(
            capture.0.lock().unwrap().as_deref(),
            Some("Bearer model-query-canary")
        );
    }
}

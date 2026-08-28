//! Provider-isolated NVIDIA Build model discovery.
//!
//! NVIDIA exposes an OpenAI-compatible Chat Completions API. Its `/models`
//! response is authoritative, so the catalog contains only chat-capable ids
//! returned by NVIDIA and never invents a static model list.

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

pub const NVIDIA_API_BASE_URL: &str = "https://integrate.api.nvidia.com/v1";
pub const NVIDIA_API_BASE_URL_ENV: &str = "OPENGROK_NVIDIA_API_BASE_URL";
pub const NVIDIA_API_KEY_ENV: &str = "NVIDIA_API_KEY";
pub const NGC_API_KEY_ENV: &str = "NGC_API_KEY";
const NVIDIA_MODELS_REQUEST_TIMEOUT: Duration = Duration::from_secs(10);

const NON_CHAT_ID_TOKENS: &[&str] = &[
    "embed",
    "clip",
    "reward",
    "translate",
    "diffusion",
    "retriever",
    "detector",
    "ising",
];

const REASONING_FAMILY_TOKENS: &[&str] = &[
    "kimi-k3",
    "kimi-k2.6",
    "muse-glimmer",
    "gpt-oss",
    "deepseek-v4",
    "nemotron",
    "cosmos-reason",
    "minimax-m3",
];

pub fn is_trusted_api_base_url(base_url: &str) -> bool {
    let Ok(url) = Url::parse(base_url) else {
        return false;
    };
    url.scheme() == "https" && url.host_str() == Some("integrate.api.nvidia.com")
}

pub fn api_base_url() -> String {
    std::env::var(NVIDIA_API_BASE_URL_ENV)
        .ok()
        .map(|value| value.trim().trim_end_matches('/').to_owned())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| NVIDIA_API_BASE_URL.to_owned())
}

fn environment_api_key() -> Option<String> {
    for name in [NVIDIA_API_KEY_ENV, NGC_API_KEY_ENV] {
        if let Some(value) = std::env::var(name)
            .ok()
            .map(|value| value.trim().to_owned())
            .filter(|value| !value.is_empty())
        {
            return Some(value);
        }
    }
    None
}

pub fn environment_api_key_is_configured() -> bool {
    environment_api_key().is_some()
}

pub fn env_keys() -> EnvKeys {
    EnvKeys::new([NVIDIA_API_KEY_ENV, NGC_API_KEY_ENV])
}

fn stored_api_key() -> Option<String> {
    crate::auth::read_provider_api_key(&crate::util::grok_home::grok_home(), ModelProvider::Nvidia)
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

fn normalized_id(model_id: &str) -> String {
    model_id
        .chars()
        .filter(|ch| ch.is_ascii_alphanumeric())
        .flat_map(|ch| ch.to_lowercase())
        .collect()
}

fn is_non_chat_catalog_id(model_id: &str) -> bool {
    let lower = model_id.to_ascii_lowercase();
    NON_CHAT_ID_TOKENS
        .iter()
        .any(|token| lower.contains(token))
}

fn is_known_reasoning_family(model_id: &str) -> bool {
    let lower = model_id.to_ascii_lowercase();
    REASONING_FAMILY_TOKENS
        .iter()
        .any(|token| lower.contains(token))
}

fn curated_context_window(model_id: &str) -> Option<u64> {
    let id = normalized_id(model_id);
    if id.contains("kimik3")
        || id.contains("deepseekv4")
        || id.contains("glm52")
        || id.contains("museglimmer")
        || id.contains("nemotron3ultra")
        || id.contains("nemotron3super")
    {
        return Some(1_000_000);
    }
    if id.contains("kimik26") {
        return Some(262_144);
    }
    if id.contains("gptoss") {
        return Some(131_072);
    }
    None
}

fn effort_option(
    value: ReasoningEffort,
    description: &str,
    default: bool,
) -> ReasoningEffortOption {
    ReasoningEffortOption {
        id: value.as_str().to_owned(),
        value,
        label: match value {
            ReasoningEffort::None => "None",
            ReasoningEffort::Minimal => "Minimal",
            ReasoningEffort::Low => "Low",
            ReasoningEffort::Medium => "Medium",
            ReasoningEffort::High => "High",
            ReasoningEffort::Max => "Max",
            _ => unreachable!("NVIDIA menus expose only none/minimal/low/medium/high/max"),
        }
        .to_owned(),
        description: Some(description.to_owned()),
        default,
    }
}

fn nvidia_reasoning_efforts() -> Vec<ReasoningEffortOption> {
    let default = ReasoningEffort::High;
    vec![
        effort_option(ReasoningEffort::None, "Answer only; skip reasoning", false),
        effort_option(
            ReasoningEffort::Minimal,
            "Use as few reasoning tokens as possible",
            false,
        ),
        effort_option(ReasoningEffort::Low, "Minimize latency and cost", false),
        effort_option(
            ReasoningEffort::Medium,
            "Balanced reasoning for most tasks",
            false,
        ),
        effort_option(
            ReasoningEffort::High,
            "Default NVIDIA reasoning depth",
            default == ReasoningEffort::High,
        ),
        effort_option(ReasoningEffort::Max, "Maximum thinking depth", false),
    ]
}

fn model_entry(model_id: &str, base_url: &str) -> ModelEntry {
    let key = format!("nvidia:{model_id}");
    let mut info = ModelInfo::fallback(&key);
    info.id = Some(key);
    info.model = model_id.to_owned();
    info.base_url = base_url.trim_end_matches('/').to_owned();
    info.name = Some(model_id.to_owned());
    info.api_backend = ApiBackend::ChatCompletions;
    info.provider = ModelProvider::Nvidia;
    info.tool_mode = Some(ToolMode::Direct);
    info.supports_backend_search = false;
    info.supports_standalone_web_search = Some(false);
    info.supported_in_api = true;
    if is_known_reasoning_family(model_id) {
        let efforts = nvidia_reasoning_efforts();
        info.reasoning_effort = efforts
            .iter()
            .find(|option| option.default)
            .map(|option| option.value);
        info.reasoning_efforts = efforts;
        info.supports_reasoning_effort = true;
    } else {
        info.supports_reasoning_effort = false;
        info.reasoning_effort = None;
        info.reasoning_efforts.clear();
    }
    if let Some(window) = curated_context_window(model_id) {
        info.context_window = NonZeroU64::new(window).expect("non-zero NVIDIA context window");
    }
    ModelEntry {
        info,
        api_key: None,
        env_key: Some(env_keys()),
        auth_provider: None,
        api_base_url: None,
    }
}

#[derive(Clone, Debug)]
pub(crate) struct NvidiaModelsCatalog {
    entries: IndexMap<String, ModelEntry>,
    credential_fingerprint: String,
}

impl NvidiaModelsCatalog {
    pub(crate) fn entries(&self) -> IndexMap<String, ModelEntry> {
        self.entries.clone()
    }

    pub(crate) fn is_authoritative(&self) -> bool {
        true
    }
}

#[derive(Clone, Debug)]
pub(crate) struct NvidiaModelsClient {
    http: reqwest::Client,
    base_url: String,
}

impl NvidiaModelsClient {
    pub(crate) fn new() -> Self {
        Self {
            http: reqwest::Client::new(),
            base_url: api_base_url(),
        }
    }

    #[cfg(test)]
    fn with_base_url(base_url: impl Into<String>) -> Self {
        Self {
            http: reqwest::Client::new(),
            base_url: base_url.into(),
        }
    }

    pub(crate) async fn query(&self) -> anyhow::Result<Option<NvidiaModelsCatalog>> {
        let Some(api_key) = api_key_for_base_url(&self.base_url) else {
            return Ok(None);
        };
        self.query_with_key(&api_key).await.map(Some)
    }

    pub(crate) fn has_usable_api_key(&self) -> bool {
        api_key_for_base_url(&self.base_url).is_some()
    }

    pub(crate) fn catalog_matches_current_credential(&self, catalog: &NvidiaModelsCatalog) -> bool {
        api_key_for_base_url(&self.base_url)
            .map(|key| credential_fingerprint(&key))
            .is_some_and(|fingerprint| fingerprint == catalog.credential_fingerprint)
    }

    async fn query_with_key(&self, api_key: &str) -> anyhow::Result<NvidiaModelsCatalog> {
        let url = format!("{}/models", self.base_url.trim_end_matches('/'));
        let response = self
            .http
            .get(&url)
            .timeout(NVIDIA_MODELS_REQUEST_TIMEOUT)
            .bearer_auth(api_key)
            .send()
            .await
            .with_context(|| format!("NVIDIA models request to {url} failed"))?;
        let status = response.status();
        if !status.is_success() {
            let body = response.text().await.unwrap_or_default();
            return Err(anyhow!(
                "NVIDIA models request returned {status}: {}",
                safe_error_excerpt(&body, api_key)
            ));
        }
        let wire: NvidiaModelsResponse = response
            .json()
            .await
            .context("NVIDIA models response was invalid")?;
        Ok(self.catalog_from_wire(wire, api_key))
    }

    fn catalog_from_wire(&self, wire: NvidiaModelsResponse, api_key: &str) -> NvidiaModelsCatalog {
        let entries = wire
            .data
            .into_iter()
            .map(|model| model.id.trim().to_owned())
            .filter(|id| !id.is_empty() && !is_non_chat_catalog_id(id))
            .map(|id| {
                let key = format!("nvidia:{id}");
                (key, model_entry(&id, &self.base_url))
            })
            .collect();
        NvidiaModelsCatalog {
            entries,
            credential_fingerprint: credential_fingerprint(api_key),
        }
    }
}

fn safe_error_excerpt(body: &str, api_key: &str) -> String {
    let sanitized = body
        .replace(api_key, "[REDACTED]")
        .replace(['\r', '\n'], " ");
    sanitized.chars().take(512).collect()
}

#[derive(Debug, Deserialize)]
struct NvidiaModelsResponse {
    #[serde(default)]
    data: Vec<NvidiaWireModel>,
}

#[derive(Debug, Deserialize)]
struct NvidiaWireModel {
    id: String,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::{read_provider_api_key, store_provider_api_key};
    use axum::{Json, Router, extract::State, http::HeaderMap, routing::get};
    use std::sync::{Arc, Mutex};

    #[test]
    fn trusted_hosts_are_provider_scoped() {
        assert!(is_trusted_api_base_url(NVIDIA_API_BASE_URL));
        assert!(is_trusted_api_base_url(
            "https://integrate.api.nvidia.com/v1/models"
        ));
        assert!(!is_trusted_api_base_url("http://integrate.api.nvidia.com/v1"));
        assert!(!is_trusted_api_base_url(
            "https://integrate.api.nvidia.com.example/v1"
        ));
        assert!(!is_trusted_api_base_url("https://api.x.ai/v1"));
        assert!(!is_trusted_api_base_url("https://pass.wafer.ai/v1"));
    }

    #[test]
    fn stored_keys_never_leave_owned_hosts() {
        let stored = Some("nvidia-stored-secret".to_owned());
        assert_eq!(
            select_api_key(NVIDIA_API_BASE_URL, None, stored.clone()).as_deref(),
            Some("nvidia-stored-secret")
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
    fn env_key_may_follow_override_url() {
        assert_eq!(
            select_api_key(
                "https://proxy.example/v1",
                Some("env-nvidia-key".to_owned()),
                Some("stored-nvidia-key".to_owned()),
            )
            .as_deref(),
            Some("env-nvidia-key")
        );
    }

    #[test]
    fn wire_catalog_uses_only_returned_chat_ids() {
        let client = NvidiaModelsClient::with_base_url(NVIDIA_API_BASE_URL);
        let catalog = client.catalog_from_wire(
            NvidiaModelsResponse {
                data: vec![
                    NvidiaWireModel {
                        id: " moonshotai/kimi-k3 ".to_owned(),
                    },
                    NvidiaWireModel { id: "".to_owned() },
                    NvidiaWireModel {
                        id: "nvidia/nv-embedqa-e5-v5".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/embed-qa".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/nvclip".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/llama-3.1-nemotron-70b-reward".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/translate-1b".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "stabilityai/stable-diffusion-xl".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/nv-retriever-qa".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/security-detector".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "nvidia/ising-model".to_owned(),
                    },
                    NvidiaWireModel {
                        id: "meta/muse-glimmer-30b".to_owned(),
                    },
                ],
            },
            "catalog-key",
        );
        let entries = catalog.entries();
        assert_eq!(entries.len(), 2);
        assert!(entries.contains_key("nvidia:moonshotai/kimi-k3"));
        assert!(entries.contains_key("nvidia:meta/muse-glimmer-30b"));
        assert!(!entries.keys().any(|key| key.contains("embed")));
        for (key, entry) in &entries {
            assert!(key.starts_with("nvidia:"));
            assert_eq!(entry.info.provider, ModelProvider::Nvidia);
            assert_eq!(entry.info.api_backend, ApiBackend::ChatCompletions);
            assert_eq!(entry.info.tool_mode, Some(ToolMode::Direct));
            assert!(!entry.info.supports_backend_search);
            assert_eq!(entry.info.supports_standalone_web_search, Some(false));
            assert_eq!(
                entry.env_key.as_ref().and_then(EnvKeys::primary),
                Some(NVIDIA_API_KEY_ENV)
            );
        }
    }

    #[test]
    fn kimi_k3_gets_reasoning_menu_unknown_chat_ids_fail_closed() {
        let kimi = model_entry("moonshotai/kimi-k3", NVIDIA_API_BASE_URL);
        assert!(kimi.info.supports_reasoning_effort);
        assert_eq!(kimi.info.reasoning_effort, Some(ReasoningEffort::High));
        let values: Vec<_> = kimi
            .info
            .reasoning_efforts
            .iter()
            .map(|option| option.value)
            .collect();
        assert_eq!(
            values,
            vec![
                ReasoningEffort::None,
                ReasoningEffort::Minimal,
                ReasoningEffort::Low,
                ReasoningEffort::Medium,
                ReasoningEffort::High,
                ReasoningEffort::Max,
            ]
        );

        let unknown = model_entry("some-vendor/future-chat", NVIDIA_API_BASE_URL);
        assert!(!unknown.info.supports_reasoning_effort);
        assert!(unknown.info.reasoning_effort.is_none());
        assert!(unknown.info.reasoning_efforts.is_empty());
        assert_eq!(unknown.info.context_window.get(), 200_000);
    }

    #[test]
    fn curated_windows_match_published_nvidia_lineup() {
        let cases = [
            ("moonshotai/kimi-k3", 1_000_000),
            ("deepseek-ai/deepseek-v4", 1_000_000),
            ("z-ai/glm-5.2", 1_000_000),
            ("meta/muse-glimmer-30b", 1_000_000),
            ("nvidia/nemotron-3-ultra", 1_000_000),
            ("nvidia/nemotron-3-super", 1_000_000),
            ("moonshotai/kimi-k2.6", 262_144),
            ("openai/gpt-oss-120b", 131_072),
            ("meta/llama-3.3-70b-instruct", 200_000),
        ];
        for (id, window) in cases {
            assert_eq!(
                model_entry(id, NVIDIA_API_BASE_URL)
                    .info
                    .context_window
                    .get(),
                window,
                "{id}"
            );
        }
        assert_eq!(curated_context_window("kimi-k2.6"), Some(262_144));
        assert_ne!(curated_context_window("kimi-k2.6"), Some(1_000_000));
    }

    #[test]
    fn catalog_key_is_nvidia_prefixed_live_id() {
        let entry = model_entry("moonshotai/kimi-k3", NVIDIA_API_BASE_URL);
        assert_eq!(entry.info.id.as_deref(), Some("nvidia:moonshotai/kimi-k3"));
        assert_eq!(entry.info.model, "moonshotai/kimi-k3");
        assert_eq!(entry.info.provider, ModelProvider::Nvidia);
        assert_eq!(entry.info.api_backend, ApiBackend::ChatCompletions);
    }

    #[test]
    fn storing_nvidia_key_does_not_touch_other_providers() {
        let home = tempfile::tempdir().expect("temp OPENGROK_HOME");
        store_provider_api_key(home.path(), ModelProvider::Wafer, "wafer-stored-secret")
            .expect("store wafer");
        store_provider_api_key(
            home.path(),
            ModelProvider::OpenRouter,
            "openrouter-stored-secret",
        )
        .expect("store openrouter");
        store_provider_api_key(home.path(), ModelProvider::Xai, "xai-stored-secret")
            .expect("store xai");
        store_provider_api_key(home.path(), ModelProvider::Nvidia, "nvidia-stored-secret")
            .expect("store nvidia");

        assert_eq!(
            read_provider_api_key(home.path(), ModelProvider::Nvidia).as_deref(),
            Some("nvidia-stored-secret")
        );
        assert_eq!(
            read_provider_api_key(home.path(), ModelProvider::Wafer).as_deref(),
            Some("wafer-stored-secret")
        );
        assert_eq!(
            read_provider_api_key(home.path(), ModelProvider::OpenRouter).as_deref(),
            Some("openrouter-stored-secret")
        );
        assert_eq!(
            read_provider_api_key(home.path(), ModelProvider::Xai).as_deref(),
            Some("xai-stored-secret")
        );
    }

    #[test]
    fn errors_redact_credentials() {
        assert_eq!(
            safe_error_excerpt("invalid nvidia-secret\nretry", "nvidia-secret"),
            "invalid [REDACTED] retry"
        );
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
                "data": [{"id": "moonshotai/kimi-k3", "object": "model", "created": 0, "owned_by": "nvidia"}]
            }))
        }

        let capture = RequestCapture::default();
        let app = Router::new()
            .route("/v1/models", get(models))
            .with_state(capture.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

        let client = NvidiaModelsClient::with_base_url(format!("http://{address}/v1"));
        let catalog = client
            .query_with_key("test-nvidia-key")
            .await
            .expect("model query");
        assert_eq!(
            capture.0.lock().expect("capture lock").as_deref(),
            Some("Bearer test-nvidia-key")
        );
        assert!(catalog.entries().contains_key("nvidia:moonshotai/kimi-k3"));
    }
}

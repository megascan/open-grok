use super::model::TEAM_PRINCIPAL_TYPE;
use crate::env::{PROD_RELAY_WS_URL, PROD_WS_ORIGIN};
use serde::{Deserialize, Serialize};
fn default_oidc_scopes() -> Vec<String> {
    vec![
        "openid".into(),
        "profile".into(),
        "email".into(),
        "offline_access".into(),
        "api:access".into(),
    ]
}
/// Default scopes for the xAI OAuth2 provider. Includes `grok-cli:access`
/// which authorizes the token for API proxy requests.
fn default_oauth2_scopes() -> Vec<String> {
    vec![
        "openid".into(),
        "profile".into(),
        "email".into(),
        "offline_access".into(),
        "grok-cli:access".into(),
        "api:access".into(),
        "conversations:read".into(),
        "conversations:write".into(),
        "workspaces:read".into(),
        "workspaces:write".into(),
    ]
}
fn default_team_oauth2_scopes() -> Vec<String> {
    vec![
        "profile".into(),
        "offline_access".into(),
        "grok-cli:access".into(),
        "api:access".into(),
        "team:read".into(),
        "conversations:read".into(),
        "conversations:write".into(),
        "workspaces:read".into(),
        "workspaces:write".into(),
    ]
}
/// Pin automatic auth to one method (`[auth] preferred_method` in config.toml).
///
/// When set, only that method is used for automatic selection; if it is
/// unavailable, auth fails (no silent fallthrough to the other method).
/// Unset keeps today's multi-method fallthrough (session preferred when both
/// exist). Config-toml only — not remote settings, settings UI, or env.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum PreferredAuthMethod {
    /// `XAI_API_KEY` / auth.json `xai::api_key` / per-model BYOK (`xai.api_key`).
    ApiKey,
    /// OIDC / OAuth2 session (`cached_token`, interactive `grok.com` / `oidc`,
    /// including devbox-minted OIDC).
    Oidc,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct GrokComConfig {
    pub grok_ws_origin: String,
    pub grok_ws_url: String,
    pub token_header: String,
    /// OIDC config for customer-provided IdPs. See [`OidcAuthConfig`].
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub oidc: Option<OidcAuthConfig>,
    /// OAuth2 provider config. When set, preferred over the legacy relay flow.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub oauth2: Option<OAuth2ProviderConfig>,
    /// External auth provider command (stdout = token, stderr = user UX, exit 0 = success).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub auth_provider_command: Option<String>,
    /// Login button label (env: `GROK_AUTH_PROVIDER_LABEL`).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub auth_provider_label: Option<String>,
    /// Token TTL in seconds for external auth providers that output bare
    /// tokens without `expires_in`. Synthesizes `expires_at` so proactive
    /// refresh works. Env: `GROK_AUTH_TOKEN_TTL`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub auth_token_ttl: Option<u64>,
    /// Admin kill switch: when `Some(true)`, the `xai.api_key` auth method is
    /// neither advertised nor accepted, so `XAI_API_KEY`/per-model credentials
    /// can't bypass the deployment's IdP login. Env: `GROK_DISABLE_API_KEY_AUTH`.
    /// Parity with common force-login-method admin knobs.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub disable_api_key_auth: Option<bool>,
    /// Restrict login to a specific team — the login token's team principal must
    /// equal this. Put in `requirements.toml` to enforce as non-overridable policy.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub force_login_team_uuid: Option<ForceLoginTeam>,
    /// Pin automatic auth to `api_key` or `oidc`. When set and the chosen
    /// method is unavailable, auth fails (no fallthrough). Unset keeps
    /// multi-method fallthrough. Config.toml only (`[auth] preferred_method`).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub preferred_method: Option<PreferredAuthMethod>,
}
/// Team login restriction. TOML string or array; an empty array fails closed.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ForceLoginTeam {
    /// The only allowed team.
    Single(String),
    /// Allowed teams; empty = fail closed.
    AnyOf(Vec<String>),
}
/// Customer OIDC Identity Provider configuration (`[grok_com_config.oidc]`).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OidcAuthConfig {
    pub issuer: String,
    pub client_id: String,
    #[serde(default = "default_oidc_scopes")]
    pub scopes: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub audience: Option<String>,
}
/// OAuth2 provider configuration (`GROK_OAUTH2_ISSUER` / `GROK_OAUTH2_CLIENT_ID`).
///
/// Uses the standard OAuth 2.1 Auth Code + PKCE flow via [`OidcAuthConfig`].
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OAuth2ProviderConfig {
    pub issuer: String,
    pub client_id: String,
    #[serde(default = "default_oauth2_scopes")]
    pub scopes: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub principal_type: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub principal_id: Option<String>,
    /// Client-supplied referrer for OAuth usage-attribution analytics.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub referrer: Option<String>,
}
pub const XAI_OAUTH2_ISSUER: &str = "https://auth.x.ai";
/// Production accounts-app origin allowlist — the only origins builds without
/// non-production builds accept. Lives in its own const, referenced by both
/// profiles below, so the frozen-contract test (monorepo CI compiles with
/// that feature enabled) still pins this production-origin const.
const PROD_ACCOUNTS_APP_ORIGINS: &[&str] = &["https://accounts.x.ai"];
/// See the opt-in non-production feature variant above — builds without
/// the feature accept only the production accounts app.
pub(crate) fn allowed_accounts_app_origins() -> Vec<String> {
    PROD_ACCOUNTS_APP_ORIGINS
        .iter()
        .map(|o| o.to_string())
        .collect()
}
/// Build a CORS layer that accepts requests from the accounts-app deployments
/// listed in [`allowed_accounts_app_origins`] for the given HTTP method.
///
/// Callers can chain additional configuration (e.g. `.allow_headers(...)` or
/// `.allow_private_network(true)`) onto the returned layer.
pub(crate) fn accounts_app_cors_layer(method: axum::http::Method) -> tower_http::cors::CorsLayer {
    tower_http::cors::CorsLayer::new()
        .allow_origin(tower_http::cors::AllowOrigin::list(
            allowed_accounts_app_origins()
                .iter()
                .filter_map(|origin| match origin.parse() {
                    Ok(value) => Some(value),
                    Err(_) => {
                        tracing::warn!(origin, "skipping malformed accounts-app CORS origin");
                        None
                    }
                }),
        ))
        .allow_methods([method])
}
/// Local-dev OAuth2 issuer (accounts-app running on localhost).
const XAI_OAUTH2_LOCAL_ISSUER: &str = "http://localhost:22255";
const DEFAULT_OAUTH2_REFERRER: &str = "grok-build";
/// Returns `true` when `GROK_LOCAL_AUTH=1` is set,
/// indicating the local accounts-app should be used as the OAuth2 issuer.
pub(crate) fn use_local_auth() -> bool {
    std::env::var("GROK_LOCAL_AUTH")
        .map(|v| !v.is_empty() && v != "0")
        .unwrap_or(false)
}
/// Returns the active xAI OAuth2 issuer — the local-dev issuer when
/// `GROK_LOCAL_AUTH=1` is set, otherwise the production issuer.
pub fn xai_oauth2_issuer() -> &'static str {
    if use_local_auth() {
        XAI_OAUTH2_LOCAL_ISSUER
    } else {
        XAI_OAUTH2_ISSUER
    }
}
/// Returns `true` if `issuer` is a recognised xAI OAuth2 issuer
/// (production **or** local-dev). Use this instead of comparing against
/// [`XAI_OAUTH2_ISSUER`] directly so that local-dev sessions are still
/// treated as first-party xAI auth.
pub fn is_xai_oauth2_issuer(issuer: &str) -> bool {
    issuer == XAI_OAUTH2_ISSUER || issuer == XAI_OAUTH2_LOCAL_ISSUER
}
/// auth.json scope key used by the pre-OIDC `open-grok login --legacy` flow.
/// Matches the key format produced by the original `accounts.x.ai` relay auth.
pub(crate) const LEGACY_AUTH_SCOPE: &str = "https://accounts.x.ai/sign-in";

// Parse nested provider tables as partial overlays, so a configured field does
// not erase the remaining defaults of an already-selected provider.
#[derive(Deserialize)]
struct GrokComConfigSection {
    grok_ws_origin: Option<String>,
    grok_ws_url: Option<String>,
    token_header: Option<String>,
    oidc: Option<toml::Value>,
    oauth2: Option<toml::Value>,
    auth_provider_command: Option<String>,
    auth_provider_label: Option<String>,
    auth_token_ttl: Option<u64>,
    disable_api_key_auth: Option<bool>,
    force_login_team_uuid: Option<ForceLoginTeam>,
    preferred_method: Option<PreferredAuthMethod>,
}

#[derive(Deserialize)]
struct OidcAuthSection {
    issuer: Option<String>,
    client_id: Option<String>,
    scopes: Option<Vec<String>>,
    audience: Option<String>,
}

#[derive(Deserialize)]
struct OAuth2ProviderSection {
    issuer: Option<String>,
    client_id: Option<String>,
    scopes: Option<Vec<String>>,
    principal_type: Option<String>,
    principal_id: Option<String>,
    referrer: Option<String>,
}

impl GrokComConfigSection {
    fn merge_over(self, base: GrokComConfig) -> Result<GrokComConfig, toml::de::Error> {
        let oidc = match (self.oidc, base.oidc) {
            (None, base) => base,
            (Some(value), None) => Some(value.try_into()?),
            (Some(value), Some(base)) => {
                let section: OidcAuthSection = value.try_into()?;
                Some(OidcAuthConfig {
                    issuer: section.issuer.unwrap_or(base.issuer),
                    client_id: section.client_id.unwrap_or(base.client_id),
                    scopes: section.scopes.unwrap_or(base.scopes),
                    audience: section.audience.or(base.audience),
                })
            }
        };
        let oauth2 = match (self.oauth2, base.oauth2) {
            (None, base) => base,
            (Some(value), None) => Some(value.try_into()?),
            (Some(value), Some(base)) => {
                let section: OAuth2ProviderSection = value.try_into()?;
                Some(OAuth2ProviderConfig {
                    issuer: section.issuer.unwrap_or(base.issuer),
                    client_id: section.client_id.unwrap_or(base.client_id),
                    scopes: section.scopes.unwrap_or(base.scopes),
                    principal_type: section.principal_type.or(base.principal_type),
                    principal_id: section.principal_id.or(base.principal_id),
                    referrer: section.referrer.or(base.referrer),
                })
            }
        };
        Ok(GrokComConfig {
            grok_ws_origin: self.grok_ws_origin.unwrap_or(base.grok_ws_origin),
            grok_ws_url: self.grok_ws_url.unwrap_or(base.grok_ws_url),
            token_header: self.token_header.unwrap_or(base.token_header),
            oidc,
            oauth2,
            auth_provider_command: self.auth_provider_command.or(base.auth_provider_command),
            auth_provider_label: self.auth_provider_label.or(base.auth_provider_label),
            auth_token_ttl: self.auth_token_ttl.or(base.auth_token_ttl),
            disable_api_key_auth: self.disable_api_key_auth.or(base.disable_api_key_auth),
            force_login_team_uuid: self.force_login_team_uuid.or(base.force_login_team_uuid),
            preferred_method: self.preferred_method.or(base.preferred_method),
        })
    }
}

impl GrokComConfig {
    /// Resolve the auth section of an effective config, retaining nested defaults.
    pub fn from_effective_config(config: &toml::Value) -> Result<Self, toml::de::Error> {
        let config = expand_auth_alias(config);
        let mut resolved = match config.get("grok_com_config") {
            Some(section) => {
                GrokComConfigSection::deserialize(section.clone())?.merge_over(Self::default())?
            }
            None => Self::default(),
        };
        resolved.pin_login_team();
        Ok(resolved)
    }

    /// Apply the non-overridable requirements, environment, then config pin.
    pub fn pin_login_team(&mut self) {
        let requirements = xai_grok_config::requirements_layers();
        self.force_login_team_uuid = resolve_force_login_team(
            force_login_team_from_requirements_layers(
                requirements.iter().map(|layer| &layer.value),
            ),
            std::env::var("OPENGROK_FORCE_LOGIN_TEAM_ID")
                .ok()
                .and_then(|value| parse_force_login_team(&value)),
            self.force_login_team_uuid.take(),
        );
    }
    /// Whether `xai.api_key` auth is disabled. Pinning a team
    /// (`force_login_team_uuid`) implies this — team membership can't be verified
    /// from a bare API key, so it must go through IdP login. The
    /// `GROK_DISABLE_API_KEY_AUTH` env lockdown is sticky: because the env value
    /// seeds `default()` (the merge base), a lower-trust user `config.toml` could
    /// otherwise set `disable_api_key_auth = false` and override it — so the env
    /// is OR-ed in here and cannot be turned back off by a user layer. Trusted
    /// `requirements.toml` already wins over `config.toml` via layer precedence.
    pub(crate) fn api_key_auth_disabled(&self) -> bool {
        self.disable_api_key_auth == Some(true)
            || self.force_login_team_uuid.is_some()
            || env_lockdown_forced()
    }
    /// When `preferred_method = api_key`, automatic OIDC paths (devbox mint,
    /// interactive browser login, external auth provider) must not run — the
    /// pin is fail-closed. Explicit `open-grok login --devbox` / `--api-key` bypass
    /// this by not consulting automatic flow helpers.
    pub(crate) fn blocks_automatic_oidc(&self) -> bool {
        matches!(self.preferred_method, Some(PreferredAuthMethod::ApiKey))
    }
    /// The auth.json scope key for this config.
    pub fn auth_scope(&self) -> String {
        if let Some(ref oidc) = self.oidc {
            format!("{}::{}", oidc.issuer.trim_end_matches('/'), oidc.client_id)
        } else if let Some(ref oauth2) = self.oauth2 {
            oauth2.auth_scope()
        } else {
            unreachable!("oauth2 config is always present (xAI default or env override)")
        }
    }
}
impl OAuth2ProviderConfig {
    pub fn is_team_principal(&self) -> bool {
        self.principal_type.as_deref() == Some(TEAM_PRINCIPAL_TYPE)
    }
    pub fn from_env() -> Option<Self> {
        let issuer = std::env::var("GROK_OAUTH2_ISSUER").ok()?;
        let client_id = std::env::var("GROK_OAUTH2_CLIENT_ID").ok()?;
        let principal_type = std::env::var("GROK_OAUTH2_PRINCIPAL_TYPE").ok();
        let principal_id = std::env::var("GROK_OAUTH2_PRINCIPAL_ID").ok();
        let default_scopes = match principal_type.as_deref() {
            Some(TEAM_PRINCIPAL_TYPE) => default_team_oauth2_scopes(),
            _ => default_oauth2_scopes(),
        };
        Some(Self {
            issuer,
            client_id,
            scopes: std::env::var("GROK_OAUTH2_SCOPES")
                .map(|s| s.split(',').map(|s| s.trim().to_owned()).collect())
                .unwrap_or(default_scopes),
            principal_type,
            principal_id,
            referrer: Some(
                std::env::var("GROK_OAUTH2_REFERRER")
                    .unwrap_or_else(|_| DEFAULT_OAUTH2_REFERRER.to_owned()),
            ),
        })
    }
    /// Convert to [`OidcAuthConfig`] to reuse the OIDC login flow.
    pub(crate) fn as_oidc(&self) -> OidcAuthConfig {
        OidcAuthConfig {
            issuer: self.issuer.clone(),
            client_id: self.client_id.clone(),
            scopes: self.scopes.clone(),
            audience: None,
        }
    }
    pub(crate) fn base_auth_scope(&self) -> String {
        format!("{}::{}", self.issuer.trim_end_matches('/'), self.client_id)
    }
    pub fn auth_scope(&self) -> String {
        self.base_auth_scope()
    }
}
impl Default for GrokComConfig {
    fn default() -> Self {
        let oidc = OidcAuthConfig::from_env();
        let oauth2 = if oidc.is_some() {
            None
        } else {
            Some(
                OAuth2ProviderConfig::from_env().unwrap_or_else(|| OAuth2ProviderConfig {
                    issuer: xai_oauth2_issuer().to_owned(),
                    client_id: obfstr::obfstr!("b1a00492-073a-47ea-816f-4c329264a828").to_owned(),
                    scopes: default_oauth2_scopes(),
                    principal_type: None,
                    principal_id: None,
                    referrer: Some(DEFAULT_OAUTH2_REFERRER.to_owned()),
                }),
            )
        };
        Self {
            grok_ws_origin: std::env::var("GROK_WS_ORIGIN")
                .unwrap_or_else(|_| PROD_WS_ORIGIN.to_owned()),
            grok_ws_url: std::env::var("GROK_WS_URL")
                .unwrap_or_else(|_| PROD_RELAY_WS_URL.to_owned()),
            token_header: "xai-grok-cli".to_owned(),
            oidc,
            oauth2,
            auth_provider_command: std::env::var("GROK_AUTH_PROVIDER_COMMAND").ok(),
            auth_provider_label: std::env::var("GROK_AUTH_PROVIDER_LABEL").ok(),
            auth_token_ttl: std::env::var("GROK_AUTH_TOKEN_TTL")
                .ok()
                .and_then(|v| v.parse().ok()),
            disable_api_key_auth: std::env::var("GROK_DISABLE_API_KEY_AUTH")
                .ok()
                .map(|v| env_flag_enabled(&v)),
            force_login_team_uuid: None,
            preferred_method: None,
        }
    }
}
/// Parse a boolean env-var value for grok's on/off flags. A bare presence
/// enables the flag, but the common falsy spellings (`0`, `false`, `off`,
/// `no`, empty) count as disabled — so e.g. `GROK_DISABLE_API_KEY_AUTH=false`
/// does NOT turn the kill switch on.
fn env_flag_enabled(value: &str) -> bool {
    !matches!(
        value.trim().to_ascii_lowercase().as_str(),
        "" | "0" | "false" | "off" | "no"
    )
}
/// True when the admin has set `GROK_DISABLE_API_KEY_AUTH` to a truthy value in
/// the process environment. Read live (call-time) and OR-ed into
/// `api_key_auth_disabled()` so the env lockdown is non-overridable by a
/// user-layer `config.toml`.
fn env_lockdown_forced() -> bool {
    std::env::var("GROK_DISABLE_API_KEY_AUTH")
        .ok()
        .is_some_and(|v| env_flag_enabled(&v))
}

/// Read a team restriction from a requirements layer. A malformed configured
/// value is a deny-all restriction, never an absent policy.
pub fn force_login_team_from_requirements_value(value: &toml::Value) -> Option<ForceLoginTeam> {
    let pin = value
        .get("grok_com_config")
        .and_then(|section| section.get("force_login_team_uuid"))
        .or_else(|| {
            value
                .get("auth")
                .and_then(|section| section.get("force_login_team_uuid"))
        })?;
    Some(pin.clone().try_into().unwrap_or_else(|_| {
        tracing::warn!("malformed requirements team pin; failing closed");
        ForceLoginTeam::AnyOf(Vec::new())
    }))
}

fn force_login_team_from_requirements_layers<'a>(
    layers: impl Iterator<Item = &'a toml::Value>,
) -> Option<ForceLoginTeam> {
    layers
        .filter_map(force_login_team_from_requirements_value)
        .last()
}

/// Fold the short `[auth]` alias into `[grok_com_config]`; explicit fields win.
pub fn expand_auth_alias(config: &toml::Value) -> toml::Value {
    let mut config = config.clone();
    if let toml::Value::Table(ref mut table) = config
        && let Some(auth) = table.remove("auth")
    {
        if let Some(explicit) = table.get_mut("grok_com_config") {
            if let (toml::Value::Table(explicit), toml::Value::Table(auth)) = (explicit, &auth) {
                for (key, value) in auth {
                    explicit.entry(key.clone()).or_insert(value.clone());
                }
            }
        } else {
            table.insert("grok_com_config".to_owned(), auth);
        }
    }
    config
}

pub fn resolve_force_login_team(
    requirements: Option<ForceLoginTeam>,
    env: Option<ForceLoginTeam>,
    config: Option<ForceLoginTeam>,
) -> Option<ForceLoginTeam> {
    requirements.or(env).or(config)
}

fn parse_force_login_team(raw: &str) -> Option<ForceLoginTeam> {
    let raw = raw.trim();
    if raw.is_empty() {
        return None;
    }
    if raw.starts_with('[') {
        match serde_json::from_str::<Vec<String>>(raw) {
            Ok(teams) => Some(ForceLoginTeam::AnyOf(
                teams
                    .into_iter()
                    .map(|team| team.trim().to_owned())
                    .collect(),
            )),
            Err(_) => {
                tracing::warn!("malformed OPENGROK_FORCE_LOGIN_TEAM_ID; failing closed");
                Some(ForceLoginTeam::AnyOf(Vec::new()))
            }
        }
    } else {
        Some(ForceLoginTeam::Single(raw.to_owned()))
    }
}

impl OidcAuthConfig {
    pub fn from_env() -> Option<Self> {
        let issuer = std::env::var("GROK_OIDC_ISSUER").ok()?;
        let client_id = std::env::var("GROK_OIDC_CLIENT_ID").ok()?;
        Some(Self {
            issuer,
            client_id,
            scopes: std::env::var("GROK_OIDC_SCOPES")
                .map(|s| s.split(',').map(|s| s.trim().to_owned()).collect())
                .unwrap_or_else(|_| default_oidc_scopes()),
            audience: std::env::var("GROK_OIDC_AUDIENCE").ok(),
        })
    }
}
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn nested_provider_overlay_preserves_unspecified_defaults() {
        let mut base = GrokComConfig::default();
        base.oidc = None;
        base.oauth2 = Some(OAuth2ProviderConfig {
            issuer: "https://example.test".into(),
            client_id: "client".into(),
            scopes: vec!["openid".into()],
            principal_type: None,
            principal_id: None,
            referrer: None,
        });
        let original = base.oauth2.clone().unwrap();
        let section: GrokComConfigSection =
            toml::from_str("[oauth2]\nprincipal_type = 'Team'\nprincipal_id = 'team-a'\n").unwrap();
        let merged = section.merge_over(base).unwrap().oauth2.unwrap();
        assert_eq!(merged.issuer, original.issuer);
        assert_eq!(merged.client_id, original.client_id);
        assert_eq!(merged.scopes, original.scopes);
        assert_eq!(merged.principal_id.as_deref(), Some("team-a"));
    }

    #[test]
    fn team_pin_precedence_and_malformed_values_fail_closed() {
        let req: toml::Value = toml::from_str("[auth]\nforce_login_team_uuid = 42\n").unwrap();
        let requirements = force_login_team_from_requirements_value(&req);
        assert_eq!(requirements, Some(ForceLoginTeam::AnyOf(vec![])));
        let env = parse_force_login_team("[\"team-b\", \" team-c \" ]");
        assert_eq!(
            env,
            Some(ForceLoginTeam::AnyOf(vec![
                "team-b".into(),
                "team-c".into()
            ]))
        );
        let config = Some(ForceLoginTeam::Single("team-a".into()));
        assert_eq!(
            resolve_force_login_team(requirements.clone(), env.clone(), config.clone()),
            requirements
        );
        assert_eq!(
            resolve_force_login_team(None, env.clone(), config.clone()),
            env
        );
        assert_eq!(resolve_force_login_team(None, None, config.clone()), config);
        assert_eq!(
            parse_force_login_team("[broken"),
            Some(ForceLoginTeam::AnyOf(vec![]))
        );
        let user: toml::Value =
            toml::from_str("[grok_com_config]\nforce_login_team_uuid = 'user-team'\n").unwrap();
        let system: toml::Value =
            toml::from_str("[auth]\nforce_login_team_uuid = 'system-team'\n").unwrap();
        assert_eq!(
            force_login_team_from_requirements_layers([&user, &system].into_iter()),
            Some(ForceLoginTeam::Single("system-team".into()))
        );
    }
    #[test]
    fn team_auth_scope_is_base_scope() {
        let cfg = OAuth2ProviderConfig {
            issuer: "https://auth.x.ai".into(),
            client_id: "client-123".into(),
            scopes: default_team_oauth2_scopes(),
            principal_type: Some("Team".into()),
            principal_id: Some("team-abc".into()),
            referrer: Some("grok-build".into()),
        };
        assert_eq!(cfg.auth_scope(), "https://auth.x.ai::client-123");
    }
    #[test]
    fn env_flag_enabled_treats_falsy_spellings_as_off() {
        for off in ["", " ", "0", "false", "FALSE", "off", "No", "  false  "] {
            assert!(!env_flag_enabled(off), "{off:?} should be off");
        }
        for on in ["1", "true", "yes", "on", "enabled"] {
            assert!(env_flag_enabled(on), "{on:?} should be on");
        }
    }
    #[test]
    fn personal_auth_scope_is_base_scope() {
        let cfg = OAuth2ProviderConfig {
            issuer: "https://auth.x.ai".into(),
            client_id: "client-123".into(),
            scopes: default_oauth2_scopes(),
            principal_type: None,
            principal_id: None,
            referrer: Some("grok-build".into()),
        };
        assert_eq!(cfg.auth_scope(), "https://auth.x.ai::client-123");
    }
    /// FROZEN loopback contract: the accounts-app origins the CLI's loopback
    /// callback server accepts cross-origin requests from. The consent page
    /// (served from accounts.x.ai) delivers the code via `fetch(..., cors)`, so
    /// removing an origin breaks loopback delivery for already-installed CLIs.
    /// Keep in sync with the oauth2-provider / accounts-app deployments.
    /// Non-production / local-dev origins are opt-in only.
    #[test]
    fn allowed_accounts_app_origins_are_frozen() {
        assert_eq!(PROD_ACCOUNTS_APP_ORIGINS, &["https://accounts.x.ai"]);
        assert_eq!(allowed_accounts_app_origins(), PROD_ACCOUNTS_APP_ORIGINS);
    }
    /// FROZEN client contract: the 10 scopes the xAI OAuth2 client requests.
    /// The server must keep accepting all of them; existing tokens carry
    /// exactly this set. Frozen OAuth client scope contract.
    #[test]
    fn default_oauth2_scopes_are_frozen() {
        let scopes = default_oauth2_scopes();
        let scopes: Vec<&str> = scopes.iter().map(String::as_str).collect();
        assert_eq!(
            scopes,
            [
                "openid",
                "profile",
                "email",
                "offline_access",
                "grok-cli:access",
                "api:access",
                "conversations:read",
                "conversations:write",
                "workspaces:read",
                "workspaces:write",
            ]
        );
    }
    /// FROZEN team-client contract: the 9 scopes the xAI OAuth2 team client
    /// requests. Keep this distinct from the personal scope contract because
    /// team authentication adds `team:read` and omits `openid` and `email`.
    #[test]
    fn default_team_oauth2_scopes_are_frozen() {
        let scopes = default_team_oauth2_scopes();
        let scopes: Vec<&str> = scopes.iter().map(String::as_str).collect();
        assert_eq!(
            scopes,
            [
                "profile",
                "offline_access",
                "grok-cli:access",
                "api:access",
                "team:read",
                "conversations:read",
                "conversations:write",
                "workspaces:read",
                "workspaces:write",
            ]
        );
    }
    #[test]
    fn preferred_method_deserializes_from_toml() {
        let cfg: GrokComConfig = toml::from_str(
            r#"
            preferred_method = "api_key"
            "#,
        )
        .expect("parse");
        assert_eq!(cfg.preferred_method, Some(PreferredAuthMethod::ApiKey));
        let cfg: GrokComConfig = toml::from_str(
            r#"
            preferred_method = "oidc"
            "#,
        )
        .expect("parse");
        assert_eq!(cfg.preferred_method, Some(PreferredAuthMethod::Oidc));
        let cfg: GrokComConfig = toml::from_str("").expect("parse empty");
        assert_eq!(cfg.preferred_method, None);
    }
}

//! Transport-level operations for the eventual shell and user-facing glue.
//!
//! The shell must close its provider export boundary before calling `run_child`.
//! These operations never initiate authentication while spawning a child.

use std::path::Path;

use async_trait::async_trait;
use serde_json::{Value, json};
use tokio::process::Command;
use tokio_util::sync::CancellationToken;

use super::{Client, Error, Host, Launch, ModelCatalog, ParentAccess, Result};

const MAX_RESULT_BYTES: usize = 1024 * 1024;
static USER_AUTH_SERIAL: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());
static LIVE_CHILDREN: std::sync::LazyLock<std::sync::Mutex<LiveChildren>> =
    std::sync::LazyLock::new(|| std::sync::Mutex::new(LiveChildren::default()));
static CHILD_STOPPED: tokio::sync::Notify = tokio::sync::Notify::const_new();

#[derive(Default)]
struct LiveChildren {
    changing_auth: bool,
    next_id: u64,
    children: std::collections::HashMap<u64, CancellationToken>,
}

/// Keep a child registered until its process has been stopped and reaped.
pub(crate) struct ActiveChildGuard(u64);
impl Drop for ActiveChildGuard {
    fn drop(&mut self) {
        if let Ok(mut state) = LIVE_CHILDREN.lock() {
            state.children.remove(&self.0);
        }
        CHILD_STOPPED.notify_one();
    }
}

/// Recheck the account-local catalog while serialized with an auth change.
pub(crate) fn register_child(alias: &str, cancel: &CancellationToken) -> Option<ActiveChildGuard> {
    let mut state = LIVE_CHILDREN.lock().ok()?;
    if state.changing_auth
        || cancel.is_cancelled()
        || super::catalog::cached_catalog(&crate::util::grok_home::grok_home())
            .and_then(|catalog| catalog.resolve_child_model(alias).map(str::to_owned))
            .is_none()
    {
        return None;
    }
    state.next_id = state.next_id.wrapping_add(1);
    let id = state.next_id;
    state.children.insert(id, cancel.clone());
    Some(ActiveChildGuard(id))
}

struct AuthChangeGuard;
impl Drop for AuthChangeGuard {
    fn drop(&mut self) {
        if let Ok(mut state) = LIVE_CHILDREN.lock() {
            state.changing_auth = false;
        }
    }
}

async fn stop_live_children(cancel: &CancellationToken) -> Result<AuthChangeGuard> {
    {
        let mut state = LIVE_CHILDREN
            .lock()
            .map_err(|_| Error::UnverifiedAuthority)?;
        state.changing_auth = true;
        for child in state.children.values() {
            child.cancel();
        }
    }
    let guard = AuthChangeGuard;
    let wait = async {
        loop {
            let notified = CHILD_STOPPED.notified();
            let empty = {
                let state = LIVE_CHILDREN
                    .lock()
                    .map_err(|_| Error::UnverifiedAuthority)?;
                state.children.is_empty()
            };
            if empty {
                return Ok::<(), Error>(());
            }
            notified.await;
        }
    };
    tokio::select! {
        result = tokio::time::timeout(std::time::Duration::from_secs(15), wait) => {
            result.map_err(|_| Error::UnverifiedAuthority)??;
            Ok(guard)
        }
        _ = cancel.cancelled() => Err(Error::Cancelled),
    }
}

async fn user_auth_guard(
    cancel: &CancellationToken,
) -> Result<tokio::sync::MutexGuard<'static, ()>> {
    tokio::select! {
        guard = USER_AUTH_SERIAL.lock() => Ok(guard),
        _ = cancel.cancelled() => Err(Error::Cancelled),
    }
}

struct UserHost<F>(F);

#[async_trait]
impl<F: FnMut(&str) + Send> Host for UserHost<F> {
    async fn authentication_url(&mut self, url: &str) {
        (self.0)(url);
    }
}

/// User-command convenience API. The callback receives an ephemeral URL and
/// must send it only to the real user, never to model history or logs.
pub async fn login_user(
    launch: &Launch,
    on_url: impl FnMut(&str) + Send,
    cancel: &CancellationToken,
) -> Result<()> {
    let _guard = user_auth_guard(cancel).await?;
    let _auth_change = stop_live_children(cancel).await?;
    super::catalog::clear_catalog(&launch.opengrok_home)?;
    login(launch, &mut UserHost(on_url), cancel, |_| Ok(())).await?;
    let catalog = list_models(
        launch,
        &launch.cwd,
        &mut UserHost(|_: &str| {}),
        cancel,
        |_| Ok(()),
    )
    .await?;
    super::catalog::save_catalog(&launch.opengrok_home, &catalog)
}

/// User-command convenience API; callers must cancel active agents and clear
/// the Antigravity catalog before completing logout.
pub async fn logout_user(launch: &Launch, cancel: &CancellationToken) -> Result<()> {
    let _guard = user_auth_guard(cancel).await?;
    let _auth_change = stop_live_children(cancel).await?;
    super::catalog::clear_catalog(&launch.opengrok_home)?;
    logout(launch, &mut UserHost(|_: &str| {}), cancel, |_| Ok(())).await
}

/// Fetch only the external account's advertised model identifiers.
pub async fn list_user_models(
    launch: &Launch,
    cwd: &Path,
    cancel: &CancellationToken,
) -> Result<ModelCatalog> {
    let _guard = user_auth_guard(cancel).await?;
    super::catalog::clear_catalog(&launch.opengrok_home)?;
    let catalog = list_models(launch, cwd, &mut UserHost(|_: &str| {}), cancel, |_| Ok(())).await?;
    super::catalog::save_catalog(&launch.opengrok_home, &catalog)?;
    Ok(catalog)
}

pub(crate) struct RunRequest<'a> {
    pub launch: &'a Launch,
    pub access: ParentAccess,
    pub lease: xai_grok_workspace::permission::UnrestrictedPermissionLease,
    pub cwd: &'a Path,
    pub model: &'a str,
    pub prompt: &'a str,
    pub resume_session: Option<&'a str>,
    /// Must accurately describe what the host's reverse RPC implementation supports.
    pub capabilities: Value,
    /// Root session scope must own the external process tree.
    pub process_scope: &'a xai_tty_utils::ProcessScope,
}

pub(crate) struct RunResult {
    pub session_id: String,
    pub text: String,
    pub output_truncated: bool,
    pub stop_reason: String,
}

/// Called only from an actual user-initiated sign-in command. Never invoke from
/// an agent message or child spawn. The agent manages OAuth and token storage.
pub async fn login(
    launch: &Launch,
    host: &mut impl Host,
    cancel: &CancellationToken,
    configure: impl FnOnce(&mut Command) -> Result<()> + Send,
) -> Result<()> {
    let mut client = Client::spawn_for_login(launch, configure).await?;
    let result = async {
        client.initialize(host, json!({}), cancel).await?;
        client.authenticate(host, cancel).await
    }
    .await;
    client.shutdown().await;
    result
}

/// Called only after the user requests Antigravity logout. The integration must
/// also revoke live children and invalidate the Antigravity catalog.
pub async fn logout(
    launch: &Launch,
    host: &mut impl Host,
    cancel: &CancellationToken,
    configure: impl FnOnce(&mut Command) -> Result<()> + Send,
) -> Result<()> {
    let mut client = Client::spawn_for_login(launch, configure).await?;
    let result = async {
        client.initialize(host, json!({}), cancel).await?;
        client.logout(host, cancel).await
    }
    .await;
    client.shutdown().await;
    result
}

/// Query the authenticated account's advertised session catalog. No prompt is
/// sent, and the catalog-only connection refuses reverse tool requests.
pub async fn list_models(
    launch: &Launch,
    cwd: &Path,
    host: &mut impl Host,
    cancel: &CancellationToken,
    configure: impl FnOnce(&mut Command) -> Result<()> + Send,
) -> Result<ModelCatalog> {
    let mut client = Client::spawn_for_catalog(launch, configure).await?;
    let result = async {
        client.initialize(host, json!({}), cancel).await?;
        client.new_session(host, cwd, cancel).await?;
        client.catalog.clone().ok_or(Error::ModelUnavailable)
    }
    .await;
    client.shutdown().await;
    result
}

/// Runs one typed ACP session and returns only its final agent-message text.
/// A fresh or loaded session is explicitly pinned to an advertised model.
/// Host callbacks are the only route for progress, permissions and questions.
pub(crate) async fn run_child(
    request: RunRequest<'_>,
    host: &mut impl Host,
    cancel: &CancellationToken,
    configure: impl FnOnce(&mut Command) -> Result<()> + Send,
) -> Result<RunResult> {
    let mut client = Client::spawn_scoped(
        request.launch,
        request.access,
        &request.lease,
        request.process_scope,
        configure,
    )
    .await?;
    let work = async {
        client
            .initialize(host, request.capabilities, cancel)
            .await?;
        if let Some(id) = request.resume_session {
            client.load_session(host, id, request.cwd, cancel).await?;
        } else {
            client.new_session(host, request.cwd, cancel).await?;
        }
        client.select_model(host, request.model, cancel).await?;
        let session_id = client.session_id.clone().ok_or(Error::MissingSession)?;
        if !host.ready(&session_id).await {
            return Err(Error::Cancelled);
        }
        let mut output = StreamingHost {
            inner: host,
            session_id: &session_id,
            text: String::new(),
            truncated: false,
        };
        let response = client.prompt(&mut output, request.prompt, cancel).await?;
        let stop_reason = response
            .get("stopReason")
            .and_then(Value::as_str)
            .ok_or(Error::Protocol)?
            .to_owned();
        Ok(RunResult {
            session_id: session_id.clone(),
            text: output.text,
            output_truncated: output.truncated,
            stop_reason,
        })
    };
    let result = tokio::select! {
        biased;
        _ = request.lease.revoked() => Err(Error::UnverifiedAuthority),
        result = work => result,
    };
    client.shutdown().await;
    result
}

struct StreamingHost<'a, H> {
    inner: &'a mut H,
    session_id: &'a str,
    text: String,
    truncated: bool,
}

#[async_trait]
impl<H: Host> Host for StreamingHost<'_, H> {
    async fn update(&mut self, method: &str, params: &Value) {
        if method == "session/update"
            && params.get("sessionId").and_then(Value::as_str) == Some(self.session_id)
            && params
                .pointer("/update/sessionUpdate")
                .and_then(Value::as_str)
                == Some("agent_message_chunk")
            && params
                .pointer("/update/content/type")
                .and_then(Value::as_str)
                == Some("text")
        {
            if let Some(text) = params
                .pointer("/update/content/text")
                .and_then(Value::as_str)
            {
                let remaining = MAX_RESULT_BYTES.saturating_sub(self.text.len());
                let mut end = remaining.min(text.len());
                while !text.is_char_boundary(end) {
                    end -= 1;
                }
                self.text.push_str(&text[..end]);
                self.truncated |= end < text.len();
            }
        }
        self.inner.update(method, params).await;
    }

    async fn request(&mut self, method: &str, params: &Value) -> Option<Value> {
        self.inner.request(method, params).await
    }

    async fn question(&mut self, params: &Value) -> Option<Value> {
        self.inner.question(params).await
    }

    async fn authentication_url(&mut self, url: &str) {
        self.inner.authentication_url(url).await;
    }
}

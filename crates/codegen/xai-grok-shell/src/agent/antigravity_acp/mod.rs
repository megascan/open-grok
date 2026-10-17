//! Isolated ACP transport for Google's Antigravity agent.
//!
//! This module is not a permission or sandbox boundary. Its caller must ensure
//! the launched process and its native tools cannot bypass the parent session's
//! restrictions, and must route client requests through the inherited policy.
//! The caller must preserve the user's permission and export boundaries.

mod catalog;
mod runner;
#[cfg(test)]
mod tests;

use async_trait::async_trait;
use serde_json::{Value, json};
use std::{
    io,
    path::{Path, PathBuf},
    process::Stdio,
    time::Duration,
};
use thiserror::Error;
use tokio::{
    io::{AsyncBufReadExt, AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, BufReader},
    process::{Child, Command},
    sync::mpsc,
};
use tokio_util::sync::CancellationToken;

pub use self::catalog::{ModelCatalog, cached_catalog};
pub(crate) use self::runner::{RunRequest, RunResult, register_child, run_child};
pub use self::runner::{list_models, list_user_models, login, login_user, logout, logout_user};

const MAX_FRAME: usize = 8 * 1024 * 1024;
const CANCEL_GRACE: Duration = Duration::from_secs(3);
const AUTH_METHOD: &str = "oauth-personal";

#[derive(Debug, Error)]
pub enum Error {
    #[error("Antigravity ACP I/O failed")]
    Io(#[from] io::Error),
    #[error("Antigravity ACP sent invalid protocol data")]
    Protocol,
    #[error("Antigravity ACP request failed with code {0}")]
    Rpc(i64),
    #[error("Antigravity ACP request was cancelled")]
    Cancelled,
    #[error("Antigravity ACP does not advertise personal OAuth")]
    OAuthUnavailable,
    #[error("Antigravity ACP model is not in the advertised session catalog")]
    ModelUnavailable,
    #[error("Antigravity ACP session did not provide a session ID")]
    MissingSession,
    #[error("Antigravity ACP does not advertise the requested capability")]
    Unsupported,
    #[error("Antigravity ACP confinement could not be applied")]
    Confinement,
    #[error(
        "Antigravity ACP requires an explicitly unrestricted parent; restricted parents need verified confinement and mediation"
    )]
    RestrictedParent,
    #[error("Antigravity ACP parent permission was revoked")]
    UnverifiedAuthority,
}

pub type Result<T> = std::result::Result<T, Error>;
type ReadPipe = Box<dyn AsyncRead + Unpin + Send>;
type WritePipe = Box<dyn AsyncWrite + Unpin + Send>;

/// Receives agent updates and handles reverse RPC. The host is responsible for
/// routing permission requests, fs, terminal, and elicitation with correct
/// authority. `None` denies a request; no request is approved by default.
#[async_trait]
pub trait Host: Send {
    /// The host durably records and admits a prepared child before its prompt.
    async fn ready(&mut self, _session_id: &str) -> bool {
        true
    }
    async fn update(&mut self, _method: &str, _params: &Value) {}
    async fn request(&mut self, _method: &str, _params: &Value) -> Option<Value> {
        None
    }
    /// `interaction_*` requests are questions with answer choices, not
    /// permission grants. A host may only answer from actual user input.
    async fn question(&mut self, _params: &Value) -> Option<Value> {
        None
    }
    /// Display the short-lived Google authorization URL to the user only.
    /// Do not persist or log it; it contains OAuth state.
    async fn authentication_url(&mut self, _url: &str) {}
}

/// Launch input is explicit: never discover or copy another application's
/// tokens. `opengrok_home` must be the already-resolved Open Grok state root.
pub struct Launch {
    pub binary: PathBuf,
    pub args: Vec<String>,
    pub opengrok_home: PathBuf,
    pub cwd: PathBuf,
}

impl Launch {
    /// Discover only the Open Grok managed runtime or an explicit override;
    /// never discover another application's installation or profile.
    pub fn discover(opengrok_home: PathBuf, cwd: PathBuf) -> Result<Self> {
        let binary = std::env::var_os("OPENGROK_ANTIGRAVITY_ACP")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                opengrok_home
                    .join("antigravity-acp/runtime/1.4.0")
                    .join(if cfg!(windows) {
                        "agy_acp_server.exe"
                    } else {
                        "agy_acp_server.par"
                    })
            });
        if !opengrok_home.is_absolute() || !cwd.is_absolute() || !binary.is_absolute() {
            return Err(Error::Protocol);
        }
        let args = if cfg!(target_os = "linux") {
            vec!["--uid=".to_owned()]
        } else {
            Vec::new()
        };
        Ok(Self {
            binary,
            args,
            opengrok_home,
            cwd,
        })
    }
}

/// Positive evidence supplied by the shell from live, user-authorized policy.
/// Missing or unresolved information must be false. YOLO alone is not enough:
/// managed deny/ask rules and hooks still precede its fast path.
#[derive(Clone, Copy, Default)]
pub(crate) struct ParentAccess {
    pub yolo: bool,
    pub no_policy_restrictions: bool,
    pub no_sandbox: bool,
    pub no_hooks: bool,
    pub child_full_access: bool,
}

impl ParentAccess {
    fn authorize(self) -> Result<()> {
        if self.yolo
            && self.no_policy_restrictions
            && self.no_sandbox
            && self.no_hooks
            && self.child_full_access
        {
            Ok(())
        } else {
            // Restricted launch is intentionally unsupported until equivalent
            // OS confinement AND per-tool mediation have been proven.
            Err(Error::RestrictedParent)
        }
    }
}

pub(super) struct Client {
    input: BufReader<ReadPipe>,
    frame: Vec<u8>,
    output: WritePipe,
    child: Option<Child>,
    process_group: Option<std::sync::Arc<xai_tty_utils::ProcessGroup>>,
    next_id: u64,
    auth_methods: Vec<String>,
    can_load_session: bool,
    can_logout: bool,
    pub session_id: Option<String>,
    pub catalog: Option<ModelCatalog>,
    selected_model: Option<String>,
    oauth_urls: mpsc::Receiver<String>,
    oauth_active: bool,
    scope: Scope,
    closed: bool,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Scope {
    Login,
    Catalog,
    Agent,
}

impl Client {
    /// An external task requires both shell policy checks and a live lease.
    pub async fn spawn_scoped(
        launch: &Launch,
        access: ParentAccess,
        lease: &xai_grok_workspace::permission::UnrestrictedPermissionLease,
        parent_scope: &xai_tty_utils::ProcessScope,
        apply_confinement: impl FnOnce(&mut Command) -> Result<()> + Send,
    ) -> Result<Self> {
        access.authorize()?;
        Self::spawn_inner(
            launch,
            Scope::Agent,
            Some(parent_scope),
            Some(lease),
            apply_confinement,
        )
        .await
    }

    /// Explicit user-initiated login does not need an agent execution grant.
    /// This connection cannot create, load, select, or prompt an agent session.
    pub async fn spawn_for_login(
        launch: &Launch,
        apply_confinement: impl FnOnce(&mut Command) -> Result<()> + Send,
    ) -> Result<Self> {
        Self::spawn_inner(launch, Scope::Login, None, None, apply_confinement).await
    }

    /// Explicit catalog inspection can create an empty session but cannot
    /// send prompts or run tools. Reverse tool requests remain denied.
    pub async fn spawn_for_catalog(
        launch: &Launch,
        apply_confinement: impl FnOnce(&mut Command) -> Result<()> + Send,
    ) -> Result<Self> {
        Self::spawn_inner(launch, Scope::Catalog, None, None, apply_confinement).await
    }

    async fn spawn_inner(
        launch: &Launch,
        scope: Scope,
        parent_scope: Option<&xai_tty_utils::ProcessScope>,
        lease: Option<&xai_grok_workspace::permission::UnrestrictedPermissionLease>,
        apply_confinement: impl FnOnce(&mut Command) -> Result<()> + Send,
    ) -> Result<Self> {
        if !launch.opengrok_home.is_absolute()
            || !launch.cwd.is_absolute()
            || !launch.binary.is_absolute()
        {
            return Err(Error::Protocol);
        }
        let profile = prepare_profile(&launch.opengrok_home)?;
        let mut command = Command::new(&launch.binary);
        command
            .args(&launch.args)
            .current_dir(&launch.cwd)
            .env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .env("HOME", &profile)
            .env("GEMINI_HOME", &profile)
            .env("AGY_ACP_FORCE_FILE_STORAGE", "1")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            // Only the narrowly validated OAuth URL is exposed; arbitrary
            // proprietary-agent stderr must never enter logs or transcripts.
            .stderr(Stdio::piped())
            .kill_on_drop(true);
        #[cfg(windows)]
        for key in ["SystemRoot", "WINDIR", "TEMP", "TMP"] {
            if let Some(value) = std::env::var_os(key) {
                command.env(key, value);
            }
        }
        // Required hook: the integrator may apply platform-specific process
        // handling before starting even an explicitly unrestricted child.
        apply_confinement(&mut command)?;
        let process_scope = parent_scope.cloned().unwrap_or_default();
        process_scope.prepare(&mut command);
        let mut spawn = || -> Result<(Child, std::sync::Arc<xai_tty_utils::ProcessGroup>)> {
            let mut child = command.spawn()?;
            let group = match process_scope.enroll(&child) {
                Ok(group) => group,
                Err(error) => {
                    let _ = child.start_kill();
                    return Err(error.into());
                }
            };
            Ok((child, group))
        };
        let (mut child, process_group) = if let Some(lease) = lease {
            lease
                .with_active_process(|| {
                    let (child, group) = spawn()?;
                    Ok::<_, Error>(((child, group.clone()), group))
                })
                .map_err(|_| Error::UnverifiedAuthority)??
        } else {
            spawn()?
        };
        let input = child.stdout.take().ok_or(Error::Protocol)?;
        let output = child.stdin.take().ok_or(Error::Protocol)?;
        let stderr = child.stderr.take().ok_or(Error::Protocol)?;
        let (oauth_tx, oauth_rx) = mpsc::channel(8);
        tokio::spawn(read_oauth_urls(stderr, oauth_tx));
        let mut client = Self::from_pipes(Box::new(input), Box::new(output), Some(child), oauth_rx);
        client.process_group = Some(process_group);
        client.scope = scope;
        Ok(client)
    }

    fn from_pipes(
        input: ReadPipe,
        output: WritePipe,
        child: Option<Child>,
        oauth_urls: mpsc::Receiver<String>,
    ) -> Self {
        Self {
            input: BufReader::new(input),
            frame: Vec::new(),
            output,
            child,
            process_group: None,
            next_id: 1,
            auth_methods: Vec::new(),
            can_load_session: false,
            can_logout: false,
            session_id: None,
            catalog: None,
            selected_model: None,
            oauth_urls,
            oauth_active: false,
            scope: Scope::Agent,
            closed: false,
        }
    }

    /// Advertise only capabilities that the caller's Host actually implements.
    pub async fn initialize(
        &mut self,
        host: &mut impl Host,
        capabilities: Value,
        cancel: &CancellationToken,
    ) -> Result<Value> {
        let response = self
            .rpc(
                host,
                "initialize",
                json!({
                    "protocolVersion": 1,
                    "clientCapabilities": capabilities,
                    "clientInfo": {"name": "zed", "title": "Zed", "version": "0.0.0"}
                }),
                cancel,
                None,
            )
            .await?;
        if response.get("protocolVersion").and_then(Value::as_u64) != Some(1) {
            return Err(Error::Protocol);
        }
        self.can_load_session = response
            .pointer("/agentCapabilities/loadSession")
            .and_then(Value::as_bool)
            == Some(true);
        self.can_logout = response
            .pointer("/agentCapabilities/auth/logout")
            .is_some_and(Value::is_object);
        self.auth_methods = response
            .get("authMethods")
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
            .filter(|method| {
                method
                    .get("type")
                    .and_then(Value::as_str)
                    .unwrap_or("agent")
                    == "agent"
            })
            .filter_map(|method| method.get("id").and_then(Value::as_str).map(str::to_owned))
            .collect();
        Ok(response)
    }

    /// Explicit user-initiated sign-in only. Google runs its OAuth flow; Open
    /// Grok never reads or copies tokens, and never persists or logs auth URLs.
    pub async fn authenticate(
        &mut self,
        host: &mut impl Host,
        cancel: &CancellationToken,
    ) -> Result<()> {
        if !self.auth_methods.iter().any(|method| method == AUTH_METHOD) {
            return Err(Error::OAuthUnavailable);
        }
        while self.oauth_urls.try_recv().is_ok() {}
        self.oauth_active = true;
        let result = self
            .rpc(
                host,
                "authenticate",
                json!({"methodId": AUTH_METHOD}),
                cancel,
                None,
            )
            .await;
        self.oauth_active = false;
        result.map(|_| ())
    }

    /// Explicit user-initiated logout only; the caller must revoke active
    /// subagents and reconnect, since ACP does not define their fate.
    pub async fn logout(&mut self, host: &mut impl Host, cancel: &CancellationToken) -> Result<()> {
        if !self.can_logout {
            return Err(Error::Unsupported);
        }
        self.rpc(host, "logout", json!({}), cancel, None).await?;
        self.session_id = None;
        self.catalog = None;
        self.selected_model = None;
        Ok(())
    }

    pub async fn new_session(
        &mut self,
        host: &mut impl Host,
        cwd: &Path,
        cancel: &CancellationToken,
    ) -> Result<Value> {
        if self.scope == Scope::Login {
            return Err(Error::RestrictedParent);
        }
        if !cwd.is_absolute() {
            return Err(Error::Protocol);
        }
        self.session_id = None;
        self.catalog = None;
        self.selected_model = None;
        let response = self
            .rpc(
                host,
                "session/new",
                json!({"cwd": cwd, "mcpServers": []}),
                cancel,
                None,
            )
            .await?;
        self.session_id = Some(
            response
                .get("sessionId")
                .and_then(Value::as_str)
                .ok_or(Error::MissingSession)?
                .to_owned(),
        );
        self.catalog = ModelCatalog::from_session(&response);
        self.selected_model = None;
        Ok(response)
    }

    /// Load a previously persisted ACP session in the same isolated profile.
    pub async fn load_session(
        &mut self,
        host: &mut impl Host,
        id: &str,
        cwd: &Path,
        cancel: &CancellationToken,
    ) -> Result<Value> {
        if self.scope != Scope::Agent {
            return Err(Error::RestrictedParent);
        }
        if !self.can_load_session {
            return Err(Error::Unsupported);
        }
        if id.is_empty() || !cwd.is_absolute() {
            return Err(Error::Protocol);
        }
        self.session_id = None;
        self.catalog = None;
        self.selected_model = None;
        let response = self
            .rpc(
                host,
                "session/load",
                json!({"sessionId":id, "cwd":cwd, "mcpServers":[]}),
                cancel,
                None,
            )
            .await?;
        self.session_id = Some(id.to_owned());
        self.catalog = ModelCatalog::from_session(&response);
        Ok(response)
    }

    /// Never silently fall back to the agent's default model.
    pub async fn select_model(
        &mut self,
        host: &mut impl Host,
        model: &str,
        cancel: &CancellationToken,
    ) -> Result<()> {
        if self.scope != Scope::Agent {
            return Err(Error::RestrictedParent);
        }
        let session = self.session_id.clone().ok_or(Error::MissingSession)?;
        self.selected_model = None;
        let catalog = self.catalog.as_ref().ok_or(Error::ModelUnavailable)?;
        if !catalog.models.iter().any(|available| available == model) {
            return Err(Error::ModelUnavailable);
        }
        let method;
        let params;
        if let Some(id) = &catalog.config_id {
            method = "session/set_config_option";
            params = json!({"sessionId": session, "configId": id, "value": model});
        } else {
            method = "session/set_model";
            params = json!({"sessionId": session, "modelId": model});
        }
        let response = self.rpc(host, method, params, cancel, None).await?;
        if method == "session/set_config_option" {
            let updated = ModelCatalog::from_session(&response).ok_or(Error::Protocol)?;
            if updated.current.as_deref() != Some(model) {
                return Err(Error::ModelUnavailable);
            }
            self.catalog = Some(updated);
        } else if let Some(catalog) = &mut self.catalog {
            catalog.current = Some(model.to_owned());
        }
        self.selected_model = Some(model.to_owned());
        Ok(())
    }

    pub async fn prompt(
        &mut self,
        host: &mut impl Host,
        text: &str,
        cancel: &CancellationToken,
    ) -> Result<Value> {
        if self.scope != Scope::Agent {
            return Err(Error::RestrictedParent);
        }
        let session = self.session_id.clone().ok_or(Error::MissingSession)?;
        if self.selected_model.is_none() {
            return Err(Error::ModelUnavailable);
        }
        self.rpc(
            host,
            "session/prompt",
            json!({"sessionId": session,
            "prompt": [{"type": "text", "text": text}]}),
            cancel,
            Some(&session),
        )
        .await
    }

    async fn send(&mut self, value: &Value) -> Result<()> {
        let mut bytes = serde_json::to_vec(value).map_err(|_| Error::Protocol)?;
        if bytes.len() > MAX_FRAME {
            return Err(Error::Protocol);
        }
        bytes.push(b'\n');
        self.output.write_all(&bytes).await?;
        self.output.flush().await?;
        Ok(())
    }

    async fn next_frame(input: &mut BufReader<ReadPipe>, bytes: &mut Vec<u8>) -> Result<Value> {
        loop {
            let available = input.fill_buf().await?;
            if available.is_empty() {
                return Err(Error::Protocol);
            }
            let count = available
                .iter()
                .position(|b| *b == b'\n')
                .map_or(available.len(), |i| i + 1);
            if bytes.len() + count > MAX_FRAME {
                return Err(Error::Protocol);
            }
            let done = available[count - 1] == b'\n';
            bytes.extend_from_slice(&available[..count]);
            input.consume(count);
            if done {
                let frame = std::mem::take(bytes);
                return serde_json::from_slice(&frame).map_err(|_| Error::Protocol);
            }
        }
    }

    async fn cancel_request(&mut self, id: u64, session: Option<&str>) -> Result<()> {
        match session {
            Some(session) => self.send(&json!({"jsonrpc":"2.0", "method":"session/cancel", "params":{"sessionId": session}})).await,
            None => self.send(&json!({"jsonrpc":"2.0", "method":"$/cancel_request", "params":{"id":id}})).await,
        }
    }

    async fn rpc(
        &mut self,
        host: &mut impl Host,
        method: &str,
        params: Value,
        cancel: &CancellationToken,
        session: Option<&str>,
    ) -> Result<Value> {
        if self.closed || cancel.is_cancelled() {
            return Err(Error::Cancelled);
        }
        let id = self.next_id;
        self.next_id = self.next_id.checked_add(1).ok_or(Error::Protocol)?;
        let request = json!({"jsonrpc":"2.0", "id": id, "method": method, "params": params});
        tokio::select! {
            result = self.send(&request) => result?,
            _ = cancel.cancelled() => {
                self.shutdown().await;
                return Err(Error::Cancelled);
            }
        }
        let mut cancelled = false;
        let mut deadline = None;
        loop {
            if cancel.is_cancelled() && !cancelled {
                if !matches!(
                    tokio::time::timeout(CANCEL_GRACE, self.cancel_request(id, session)).await,
                    Ok(Ok(()))
                ) {
                    self.shutdown().await;
                    return Err(Error::Cancelled);
                }
                cancelled = true;
                deadline = Some(tokio::time::Instant::now() + CANCEL_GRACE);
            }
            let message = tokio::select! {
                _ = cancel.cancelled(), if !cancelled => {
                    if !matches!(tokio::time::timeout(CANCEL_GRACE, self.cancel_request(id, session)).await, Ok(Ok(()))) {
                        self.shutdown().await;
                        return Err(Error::Cancelled);
                    }
                    cancelled = true;
                    deadline = Some(tokio::time::Instant::now() + CANCEL_GRACE);
                    continue;
                }
                _ = async { tokio::time::sleep_until(deadline.expect("deadline exists")).await }, if deadline.is_some() => {
                    self.shutdown().await;
                    return Err(Error::Cancelled);
                }
                Some(url) = self.oauth_urls.recv(), if self.oauth_active => {
                    if cancelled { continue; }
                    tokio::select! {
                        _ = host.authentication_url(&url) => {},
                        _ = cancel.cancelled(), if !cancelled => {},
                    }
                    continue;
                }
                result = Self::next_frame(&mut self.input, &mut self.frame) => result?,
            };
            if message.get("jsonrpc").and_then(Value::as_str) != Some("2.0") {
                return Err(Error::Protocol);
            }
            if let Some(incoming_method) = message.get("method").and_then(Value::as_str) {
                let incoming_params = message.get("params").cloned().unwrap_or_else(|| json!({}));
                if let Some(incoming_id) = message.get("id") {
                    let is_question = incoming_method == "session/request_permission"
                        && incoming_params
                            .pointer("/toolCall/toolCallId")
                            .and_then(Value::as_str)
                            .is_some_and(|id| id.starts_with("interaction_"));
                    let reply = tokio::select! {
                        reply = async {
                            if self.scope != Scope::Agent && incoming_method != "elicitation/create" { None }
                            else if is_question { host.question(&incoming_params).await }
                            else { host.request(incoming_method, &incoming_params).await }
                        } => reply,
                        _ = cancel.cancelled() => None,
                    };
                    let reply = if cancel.is_cancelled() { None } else { reply };
                    // A permission cancellation is an ACP result, not a grant.
                    let response = match reply {
                        Some(value) => json!({"jsonrpc":"2.0", "id":incoming_id, "result":value}),
                        None if incoming_method == "session/request_permission" => {
                            json!({"jsonrpc":"2.0", "id":incoming_id, "result":{"outcome":{"outcome":"cancelled"}}})
                        }
                        None => {
                            json!({"jsonrpc":"2.0", "id":incoming_id, "error":{"code":-32601, "message":"Unsupported request"}})
                        }
                    };
                    tokio::select! {
                        result = self.send(&response) => result?,
                        _ = cancel.cancelled() => {
                            self.shutdown().await;
                            return Err(Error::Cancelled);
                        }
                    }
                } else {
                    if cancelled {
                        continue;
                    }
                    if incoming_method == "session/update"
                        && incoming_params
                            .pointer("/update/sessionUpdate")
                            .and_then(Value::as_str)
                            == Some("config_option_update")
                        && incoming_params.get("sessionId").and_then(Value::as_str)
                            == self.session_id.as_deref()
                    {
                        let updated = ModelCatalog::from_session(&incoming_params["update"]);
                        if let Some(selected) = &self.selected_model {
                            if updated.as_ref().and_then(|c| c.current.as_deref())
                                != Some(selected.as_str())
                            {
                                if let Some(session) = session {
                                    let _ = tokio::time::timeout(
                                        CANCEL_GRACE,
                                        self.cancel_request(id, Some(session)),
                                    )
                                    .await;
                                }
                                self.shutdown().await;
                                return Err(Error::ModelUnavailable);
                            }
                        }
                        self.catalog = updated;
                    }
                    tokio::select! {
                        _ = host.update(incoming_method, &incoming_params) => {},
                        _ = cancel.cancelled(), if !cancelled => {},
                    }
                }
                continue;
            }
            if message.get("id").and_then(Value::as_u64) != Some(id) {
                return Err(Error::Protocol);
            }
            if cancelled || cancel.is_cancelled() {
                return Err(Error::Cancelled);
            }
            if let Some(error) = message.get("error") {
                // Never forward agent-supplied error text: it can contain secrets.
                return Err(Error::Rpc(
                    error.get("code").and_then(Value::as_i64).unwrap_or(-32603),
                ));
            }
            return message.get("result").cloned().ok_or(Error::Protocol);
        }
    }

    /// Kill the owned process group before reaping its leader.
    pub async fn shutdown(&mut self) {
        if self.closed {
            return;
        }
        self.closed = true;
        if let Some(group) = &self.process_group {
            let _ = group.kill();
        }
        let _ = tokio::time::timeout(CANCEL_GRACE, self.output.shutdown()).await;
        if let Some(child) = &mut self.child {
            let _ = child.start_kill();
            let _ = child.wait().await;
        }
        self.process_group = None;
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        if let Some(group) = &self.process_group {
            let _ = group.kill();
        }
    }
}

/// Only one known diagnostic form is surfaced, and only while authenticating.
/// All other stderr is discarded. No log contains the URL or source line.
async fn read_oauth_urls(stderr: impl AsyncRead + Unpin, sender: mpsc::Sender<String>) {
    let mut reader = BufReader::new(stderr);
    let mut line = Vec::new();
    let mut discarding = false;
    loop {
        // Bound memory even for malicious or malformed stderr without newline.
        line.clear();
        let read = match (&mut reader)
            .take(16 * 1024)
            .read_until(b'\n', &mut line)
            .await
        {
            Ok(read) => read,
            Err(_) => break,
        };
        if read == 0 {
            break;
        }
        if line.last() != Some(&b'\n') {
            discarding = true;
            continue;
        }
        if discarding {
            discarding = false;
            continue;
        }
        let Ok(line) = std::str::from_utf8(&line) else {
            continue;
        };
        let Some(candidate) = line
            .trim()
            .strip_prefix("Open the following link to authenticate the ACP server: ")
        else {
            continue;
        };
        let Ok(url) = reqwest::Url::parse(candidate) else {
            continue;
        };
        if url.scheme() == "https"
            && url.host_str() == Some("accounts.google.com")
            && url.username().is_empty()
            && url.password().is_none()
            && url.port().is_none()
            && url.fragment().is_none()
        {
            let _ = sender.try_send(candidate.to_owned());
        }
    }
}

fn prepare_profile(home: &Path) -> Result<PathBuf> {
    let profile = home.join("antigravity-acp").join("profile");
    let settings_dir = profile.join("antigravity-acp");
    std::fs::create_dir_all(&settings_dir)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&profile, std::fs::Permissions::from_mode(0o700))?;
        std::fs::set_permissions(&settings_dir, std::fs::Permissions::from_mode(0o700))?;
    }
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    match options.open(settings_dir.join("settings.json")) {
        Ok(mut file) => {
            use std::io::Write;
            file.write_all(b"{\"auth\":{\"type\":\"oauth-personal\"}}\n")?;
        }
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {}
        Err(error) => return Err(error.into()),
    }
    Ok(profile)
}

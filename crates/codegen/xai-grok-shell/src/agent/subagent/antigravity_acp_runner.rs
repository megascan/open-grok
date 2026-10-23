//! Antigravity child route, lifecycle, and parent-policy checks.

use super::*;
use crate::agent::antigravity_acp::{self, Host, Launch, ParentAccess, RunRequest};
use crate::session::persistence::{PersistenceMsg, ProviderBoundary};
use async_trait::async_trait;
use serde_json::{Value, json};

const PREFIX: &str = "antigravity-acp:";

/// Only a catalog-backed selector denotes this transport. The reserved
/// namespace fails closed when its catalog is missing or the model is stale.
pub(super) fn resolve_model(
    selected: Option<&str>,
) -> Result<Option<(String, String)>, &'static str> {
    let Some(selected) = selected.filter(|s| s.starts_with(PREFIX)) else {
        return Ok(None);
    };
    let catalog = antigravity_acp::cached_catalog(&crate::util::grok_home::grok_home())
        .ok_or("Antigravity model catalog is unavailable; sign in again")?;
    let model = catalog
        .resolve_child_model(selected)
        .ok_or("Antigravity model is not in the signed-in account catalog")?;
    Ok(Some((selected.to_owned(), model.to_owned())))
}

#[derive(serde::Serialize, serde::Deserialize)]
struct ExternalSession {
    transport: String,
    model: String,
    session_id: String,
}

fn safe_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 128
        && id != "."
        && id != ".."
        && id
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
}

fn read_bounded_sidecar(path: &Path) -> std::io::Result<Vec<u8>> {
    use std::io::Read;
    let mut bytes = Vec::new();
    std::fs::File::open(path)?
        .take(16 * 1024 + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() > 16 * 1024 {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "oversized ACP resume metadata",
        ));
    }
    Ok(bytes)
}

/// The sidecar establishes transport identity on resume, including when the
/// ordinary model field has been lost or corrupted. Never resume such a
/// child through the native sampler fallback.
pub(super) fn resume_model(
    ctx: &SubagentSpawnContext,
    source: &ResumeSourceData,
) -> Result<Option<String>, &'static str> {
    if !safe_id(&source.subagent_id) {
        return if source
            .model_id
            .as_deref()
            .is_some_and(|id| id.starts_with(PREFIX))
        {
            Err("Invalid Antigravity resume identity")
        } else {
            Ok(None)
        };
    }
    let path = metadata_dir(ctx, &source.subagent_id).join("antigravity-acp.json");
    let data = match read_bounded_sidecar(&path) {
        Ok(data) => data,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return if source
                .model_id
                .as_deref()
                .is_some_and(|id| id.starts_with(PREFIX))
            {
                Err("Antigravity resume metadata is unavailable")
            } else {
                Ok(None)
            };
        }
        Err(_) => return Err("Antigravity resume metadata is unavailable"),
    };
    let saved: ExternalSession =
        serde_json::from_slice(&data).map_err(|_| "Antigravity resume metadata is invalid")?;
    let alias = format!("{PREFIX}{}", saved.model);
    if saved.transport != "antigravity-acp"
        || source.model_id.as_deref() != Some(alias.as_str())
        || source.model_route.is_some()
        || saved.session_id.is_empty()
        || saved.session_id.len() > 4096
        || saved.session_id.chars().any(char::is_control)
    {
        return Err("Antigravity resume metadata is inconsistent");
    }
    Ok(Some(alias))
}

fn metadata_dir(ctx: &SubagentSpawnContext, id: &str) -> PathBuf {
    let parent = SessionInfo {
        id: acp::SessionId::new(ctx.parent_session_id.clone()),
        cwd: ctx.parent_cwd.to_string_lossy().into_owned(),
    };
    session::persistence::session_dir(&parent)
        .join("subagents")
        .join(id)
}

fn load_external_session(
    ctx: &SubagentSpawnContext,
    source: &ResumeSourceData,
    model: &str,
) -> Option<String> {
    let bytes =
        read_bounded_sidecar(&metadata_dir(ctx, &source.subagent_id).join("antigravity-acp.json"))
            .ok()?;
    let saved: ExternalSession = serde_json::from_slice(&bytes).ok()?;
    (saved.transport == "antigravity-acp"
        && saved.model == model
        && !saved.session_id.is_empty()
        && saved.session_id.len() <= 4096
        && !saved.session_id.chars().any(char::is_control))
    .then_some(saved.session_id)
}

fn definition_allows_external(
    definition: &xai_grok_agent::config::AgentDefinition,
    runtime: &xai_grok_subagent_resolution::EffectiveRuntimeConfig,
) -> bool {
    use xai_tool_types::SubagentCapabilityMode;
    !(definition.name != "general-purpose"
        || definition.is_inline_profile()
        || definition.source_path.is_some()
        || definition.plugin_name.is_some()
        || !definition.tools.is_empty()
        || !definition.disallowed_tools.is_empty()
        || definition.session_tools_allowlist.is_some()
        || definition.session_tools_denylist.is_some()
        || definition.hooks.is_some()
        || !matches!(
            definition.permission_mode,
            PermissionMode::Default | PermissionMode::BypassPermissions
        )
        || !matches!(
            runtime.capability_mode,
            None | Some(SubagentCapabilityMode::All)
        ))
}

async fn authorize(
    ctx: &SubagentSpawnContext,
    parent: &SessionHandle,
    definition: &xai_grok_agent::config::AgentDefinition,
    runtime: &xai_grok_subagent_resolution::EffectiveRuntimeConfig,
) -> Result<xai_grok_workspace::permission::UnrestrictedPermissionLease, &'static str> {
    use crate::session::plan_mode::PlanModeState;

    // `ctx.yolo_mode` is a session-start snapshot. The permission actor's
    // lease below validates the current mode, including changes made since.
    if !definition_allows_external(definition, runtime)
        || ctx
            .hook_registry
            .as_ref()
            .is_some_and(|hooks| !hooks.is_empty())
    {
        return Err("Antigravity requires an unrestricted general-purpose child and parent");
    }
    let permission = ctx
        .permission_handle
        .as_ref()
        .ok_or("Parent permissions are unavailable")?;
    let fence = parent
        .sandbox_mode_lease_registration
        .as_ref()
        .ok_or("Parent sandbox mode cannot be verified")?;
    let sandbox_revision = fence.revision();
    let hook_generation = {
        let plan = parent.plan_mode.lock();
        if plan.state() != PlanModeState::Inactive {
            return Err("Antigravity is unavailable in plan mode");
        }
        plan.hook_generation()
    };
    if !parent
        .try_snapshot_client_hooks()
        .await
        .ok_or("Parent hooks are unavailable")?
        .is_empty()
        || !matches!(
            ctx.workspace_ops.sandbox_requires_local_enforcement().await,
            Ok(false)
        )
    {
        return Err("Antigravity cannot enforce the parent hooks or sandbox");
    }
    let lease = permission
        .acquire_unrestricted_lease()
        .await
        .ok_or("Parent permissions are restricted")?;
    {
        let plan = parent.plan_mode.lock();
        if plan.state() != PlanModeState::Inactive
            || plan.hook_generation() != hook_generation
            || lease.is_revoked()
            || !fence.is_current(sandbox_revision)
        {
            return Err("Parent permissions changed while launching Antigravity");
        }
    }
    Ok(lease)
}

struct ExternalHost {
    reporter: grok_build::task::coordinator::ChildReporter<ShellChildRuntime>,
    request: SubagentRequest,
    cancel: CancellationToken,
    gateway: GatewaySender,
    parent_cmd_tx: Option<mpsc::UnboundedSender<SessionCommand>>,
    dir: PathBuf,
    meta: SubagentMeta,
    model: String,
    session_id: Option<String>,
    definition_background: bool,
    spawned: bool,
    hunk_tracker: Option<HunkTrackerHandle>,
    prompt_index: usize,
    cwd: PathBuf,
    edit_calls: std::collections::HashMap<String, Vec<ReportedDiff>>,
}

#[derive(Clone)]
struct ReportedDiff {
    path: PathBuf,
    old: Option<String>,
    new: String,
}

fn reported_diffs(update: &Value) -> Vec<ReportedDiff> {
    update
        .get("content")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|item| {
            if item.get("type")?.as_str()? != "diff" {
                return None;
            }
            let path = PathBuf::from(item.get("path")?.as_str()?);
            let new = item.get("newText")?.as_str()?;
            let old = match item.get("oldText") {
                None | Some(Value::Null) => None,
                Some(value) => Some(value.as_str()?.to_owned()),
            };
            if new.len() > 1024 * 1024 || old.as_ref().is_some_and(|text| text.len() > 1024 * 1024)
            {
                return None;
            }
            Some(ReportedDiff {
                path,
                old,
                new: new.to_owned(),
            })
        })
        .take(64)
        .collect()
}

async fn verified_diff_path(root: &Path, cwd: &Path, diff: &ReportedDiff) -> Option<PathBuf> {
    use tokio::io::AsyncReadExt;
    let path = if diff.path.is_absolute() {
        diff.path.clone()
    } else {
        cwd.join(&diff.path)
    };
    let path = tokio::fs::canonicalize(path).await.ok()?;
    if !path.starts_with(root) {
        return None;
    }
    let metadata = tokio::fs::metadata(&path).await.ok()?;
    if !metadata.is_file() || metadata.len() != diff.new.len() as u64 {
        return None;
    }
    let file = tokio::fs::File::open(&path).await.ok()?;
    let mut bytes = Vec::new();
    file.take((diff.new.len() + 1) as u64)
        .read_to_end(&mut bytes)
        .await
        .ok()?;
    (bytes == diff.new.as_bytes()).then_some(path)
}

fn permission_response(session_id: Option<&str>, method: &str, params: &Value) -> Option<Value> {
    if method != "session/request_permission"
        || session_id.is_none()
        || params.get("sessionId").and_then(Value::as_str) != session_id
    {
        return None;
    }
    let id = params
        .pointer("/toolCall/toolCallId")
        .and_then(Value::as_str)?;
    if id.starts_with("interaction_") {
        return None;
    }
    let kind = params.pointer("/toolCall/kind").and_then(Value::as_str)?;
    if !matches!(
        kind,
        "read" | "edit" | "delete" | "move" | "search" | "execute" | "fetch"
    ) {
        return None;
    }
    let option = params
        .get("options")?
        .as_array()?
        .iter()
        .find(|option| option.get("kind").and_then(Value::as_str) == Some("allow_once"))?;
    Some(json!({"outcome": {"outcome": "selected", "optionId": option.get("optionId")?.as_str()?}}))
}

#[async_trait]
impl Host for ExternalHost {
    async fn update(&mut self, method: &str, params: &Value) {
        if self.hunk_tracker.is_none()
            || method != "session/update"
            || params.get("sessionId").and_then(Value::as_str) != self.session_id.as_deref()
        {
            return;
        }
        let Some(update) = params.get("update") else {
            return;
        };
        let event = update.get("sessionUpdate").and_then(Value::as_str);
        if !matches!(event, Some("tool_call" | "tool_call_update")) {
            return;
        }
        let Some(id) = update
            .get("toolCallId")
            .and_then(Value::as_str)
            .filter(|id| id.len() <= 512)
        else {
            return;
        };
        if update.get("kind").and_then(Value::as_str) == Some("edit") && self.edit_calls.len() < 128
        {
            self.edit_calls.entry(id.to_owned()).or_default();
        }
        let Some(diffs) = self.edit_calls.get_mut(id) else {
            return;
        };
        let reported = reported_diffs(update);
        if !reported.is_empty() {
            *diffs = reported;
        }
        match update.get("status").and_then(Value::as_str) {
            Some("completed") => {
                let diffs = self.edit_calls.remove(id).unwrap_or_default();
                let Ok(root) = tokio::fs::canonicalize(&self.cwd).await else {
                    return;
                };
                for diff in diffs {
                    // A reported preview is not proof of a write. Attribute only
                    // completed edits whose complete on-disk bytes match the ACP diff.
                    let Some(path) = verified_diff_path(&root, &self.cwd, &diff).await else {
                        continue;
                    };
                    if let Some(tracker) = &self.hunk_tracker {
                        tracker.record_agent_write(path, diff.new, self.prompt_index, diff.old);
                    }
                }
            }
            Some("failed") => {
                self.edit_calls.remove(id);
            }
            _ => {}
        }
    }

    async fn ready(&mut self, session_id: &str) -> bool {
        let saved = ExternalSession {
            transport: "antigravity-acp".into(),
            model: self.model.clone(),
            session_id: session_id.into(),
        };
        let Ok(data) = serde_json::to_string(&saved) else {
            return false;
        };
        if atomic_write(&self.dir.join("antigravity-acp.json"), &data).is_err()
            || !write_subagent_meta(&self.dir, &self.meta)
        {
            return false;
        }
        if !self
            .reporter
            .started(StartedChild {
                child_session_id: self.request.id.clone(),
                persona: self.meta.persona.clone(),
                resumed_from: self.request.resume_from.clone(),
                child_cwd: self.meta.child_cwd.clone().unwrap_or_default(),
                worktree_path: self.meta.worktree_path.clone(),
                effective_model_id: self.meta.effective_model_id.clone().unwrap_or_default(),
                definition_background: self.definition_background,
                control: ShellChildRuntime::Antigravity(self.cancel.clone()),
            })
            .await
        {
            return false;
        }
        self.session_id = Some(session_id.to_string());
        self.spawned = true;
        emit_subagent_notification(
            &self.gateway,
            &self.request.parent_session_id,
            SessionUpdate::SubagentSpawned {
                subagent_id: self.request.id.clone(),
                child_session_id: self.request.id.clone(),
                parent_session_id: self.request.parent_session_id.clone(),
                parent_prompt_id: self.request.parent_prompt_id.clone(),
                subagent_type: self.request.subagent_type.clone(),
                description: self.request.description.clone(),
                effective_context_source: self.meta.effective_context_source.clone(),
                context_normalized: false,
                capability_mode: None,
                persona: self.meta.persona.clone(),
                role: None,
                model: self.meta.effective_model_id.clone(),
                resumed_from: self.request.resume_from.clone(),
                swarm_id: self.request.swarm.as_ref().map(|s| s.swarm_id.clone()),
                swarm_description: self.request.swarm.as_ref().map(|s| s.description.clone()),
                swarm_index: self.request.swarm.as_ref().map(|s| s.index),
                swarm_item: self.request.swarm.as_ref().and_then(|s| s.item.clone()),
                swarm_expected_members: self.request.swarm.as_ref().map(|s| s.expected_members),
                workflow_run_id: self.request.owner.workflow_run_id().map(str::to_string),
            },
            self.parent_cmd_tx.as_ref(),
        );
        true
    }

    async fn request(&mut self, method: &str, params: &Value) -> Option<Value> {
        if self.cancel.is_cancelled() {
            return None;
        }
        permission_response(self.session_id.as_deref(), method, params)
    }
}

#[allow(clippy::too_many_arguments)]
pub(super) async fn run_external_child(
    request: &SubagentRequest,
    ctx: &SubagentSpawnContext,
    gateway: &GatewaySender,
    parent: Option<&SessionHandle>,
    reporter: grok_build::task::coordinator::ChildReporter<ShellChildRuntime>,
    cancel: &CancellationToken,
    definition: &xai_grok_agent::config::AgentDefinition,
    runtime: &xai_grok_subagent_resolution::EffectiveRuntimeConfig,
    source: Option<&ResumeSourceData>,
    worktree: Option<&Path>,
    alias: &str,
    model: &str,
    mut completion: ShellCompletionData,
    start: std::time::Instant,
) -> ChildRunOutput<ShellCompletionData> {
    macro_rules! fail {
        ($message:expr) => {
            return child_run_output(failure_result(request, $message), completion, None)
        };
    }
    if !safe_id(&request.id)
        || source.is_some_and(|source| {
            !safe_id(&source.subagent_id)
                || request.resume_from.as_deref() != Some(source.subagent_id.as_str())
        })
    {
        fail!("Invalid Antigravity subagent identity");
    }
    if request.context.is_explicit_fork()
        || request.runtime_overrides.native_agent.is_some()
        || runtime.reasoning_effort.is_some()
        || request.runtime_overrides.output_schema.is_some()
        || request.runtime_overrides.output_token_budget.is_some()
    {
        fail!("Antigravity does not support the requested context or runtime overrides");
    }
    if source.is_none()
        && runtime.isolation != xai_tool_types::SubagentIsolationMode::None
        && worktree.is_none()
    {
        fail!("Could not create the requested Antigravity worktree");
    }
    let Some(parent) = parent else {
        fail!("Parent session is unavailable");
    };
    let Some(scope) = ctx.process_scope.as_ref() else {
        fail!("Parent process scope is unavailable");
    };
    let lease = match authorize(ctx, parent, definition, runtime).await {
        Ok(lease) => lease,
        Err(error) => fail!(error),
    };
    let Some(_active_child) = antigravity_acp::register_child(alias, cancel) else {
        fail!("Antigravity sign-in changed or its model catalog is unavailable");
    };
    let resume = if let Some(source) = source {
        match load_external_session(ctx, source, model) {
            Some(id) => Some(id),
            None => fail!("Antigravity resume metadata is unavailable or inconsistent"),
        }
    } else if request.resume_from.is_some() {
        fail!("Antigravity resume source is unavailable");
    } else {
        None
    };
    let cwd = resolve_child_cwd(
        worktree,
        select_override_cwd(source, request.cwd.as_deref()),
        &ctx.parent_cwd,
    );
    if !cwd.is_absolute() || !cwd.is_dir() {
        fail!("Antigravity working directory is unavailable");
    }
    let launch = match Launch::discover(crate::util::grok_home::grok_home(), cwd.clone()) {
        Ok(launch) => launch,
        Err(_) => fail!("Antigravity runtime is unavailable"),
    };
    if let Err(message) = close_parent_export(
        &ctx.provider_boundary,
        ctx.parent_persistence_tx.as_ref(),
        cancel,
    )
    .await
    {
        fail!(message);
    }
    let mut prompt = String::new();
    if let Some(role) = &runtime.role_prompt {
        prompt.push_str(role);
        prompt.push_str("\n\n");
    }
    if let Some(persona) = &runtime.persona_instructions {
        prompt.push_str(persona);
        prompt.push_str("\n\n");
    }
    prompt.push_str(&request.prompt);
    let dir = metadata_dir(ctx, &request.id);
    let worktree_string = worktree.map(|p| p.to_string_lossy().into_owned());
    let meta = SubagentMeta {
        subagent_id: request.id.clone(),
        parent_session_id: ctx.parent_session_id.clone(),
        child_session_id: request.id.clone(),
        subagent_type: request.subagent_type.clone(),
        description: request.description.clone(),
        prompt: request.prompt.clone(),
        status: "running".into(),
        started_at: chrono::Utc::now(),
        completed_at: None,
        duration_ms: None,
        tool_calls: None,
        turns: None,
        error: None,
        effective_context_source: Some(if resume.is_some() { "resumed" } else { "new" }.into()),
        context_normalized: false,
        fork_copy_error: None,
        persona: runtime.persona.clone(),
        resumed_from: request.resume_from.clone(),
        child_cwd: Some(cwd.to_string_lossy().into_owned()),
        worktree_path: worktree_string.clone(),
        snapshot_ref: None,
        effective_model_id: Some(alias.into()),
        model_route: None,
    };
    let mut host = ExternalHost {
        reporter,
        request: request.clone(),
        cancel: cancel.clone(),
        gateway: gateway.clone(),
        parent_cmd_tx: ctx.parent_cmd_tx.clone(),
        dir: dir.clone(),
        meta,
        model: model.into(),
        session_id: None,
        definition_background: definition.background.unwrap_or(false),
        spawned: false,
        hunk_tracker: ctx
            .hunk_tracking_enabled
            .then(|| ctx.hunk_tracker_handle.clone()),
        prompt_index: parent
            .tool_context
            .active_message_parent_prompt_index
            .load(std::sync::atomic::Ordering::Acquire),
        cwd: cwd.clone(),
        edit_calls: std::collections::HashMap::new(),
    };
    let outcome = antigravity_acp::run_child(
        RunRequest {
            launch: &launch,
            access: ParentAccess {
                yolo: true,
                no_policy_restrictions: true,
                no_sandbox: true,
                no_hooks: true,
                child_full_access: true,
            },
            lease,
            cwd: &cwd,
            model,
            prompt: &prompt,
            resume_session: resume.as_deref(),
            capabilities: json!({}),
            process_scope: scope,
        },
        &mut host,
        cancel,
        |_| Ok(()),
    )
    .await;
    completion.spawned_notification_emitted = host.spawned;
    if host.spawned {
        let _ = host.reporter.finalizing().await;
    }
    let mut result = match outcome {
        Ok(output) if output.stop_reason == "end_turn" && !output.output_truncated => {
            SubagentResult {
                success: true,
                output: Arc::from(output.text),
                output_usage_incomplete: true,
                turns: 1,
                ..Default::default()
            }
        }
        Ok(output) if output.stop_reason == "cancelled" => {
            cancelled_result(request, "Antigravity task was cancelled")
        }
        Ok(_) => failure_result(request, "Antigravity task stopped before completion"),
        Err(antigravity_acp::Error::Cancelled) => {
            cancelled_result(request, "Antigravity task was cancelled")
        }
        Err(error) => failure_result(request, &format!("Antigravity task failed: {error}")),
    };
    result.subagent_id = request.id.clone();
    result.child_session_id = request.id.clone();
    result.subagent_type = request.subagent_type.clone();
    result.worktree_path = worktree_string;
    result.duration_ms = start.elapsed().as_millis().min(u64::MAX as u128) as u64;
    if host.spawned {
        completion.set_persisted_output_dir(persist_subagent_output(&dir, &result));
    }
    if dir.join("meta.json").is_file() {
        host.meta.status = result.status().into();
        host.meta.completed_at = Some(chrono::Utc::now());
        host.meta.duration_ms = Some(result.duration_ms);
        host.meta.turns = Some(result.turns);
        host.meta.error = result.error.clone();
        write_subagent_meta(&dir, &host.meta);
    }
    child_run_output(result, completion, None)
}

/// Close the in-memory export guard immediately and require durable persistence
/// before external session content is sent or accepted. Any uncertainty fails
/// closed, including a missing actor, write failure or cancellation.
pub(super) async fn close_parent_export(
    boundary: &ProviderBoundary,
    tx: Option<&mpsc::UnboundedSender<PersistenceMsg>>,
    cancel: &CancellationToken,
) -> Result<(), &'static str> {
    boundary.close_xai_export();
    let tx = tx.ok_or("Parent session persistence is unavailable")?;
    let (respond_to, response) = oneshot::channel();
    tx.send(PersistenceMsg::CloseXaiExportAndAck { respond_to })
        .map_err(|_| "Parent session persistence is unavailable")?;
    tokio::select! {
        result = response => match result {
            Ok(Ok(())) => Ok(()),
            _ => Err("Could not persist the external agent export boundary"),
        },
        _ = cancel.cancelled() => Err("External agent launch was cancelled"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn external_child_cannot_override_definition_restrictions() {
        use xai_tool_types::SubagentCapabilityMode;
        let mut definition = xai_grok_agent::config::AgentDefinition::general_purpose();
        let mut runtime = xai_grok_subagent_resolution::EffectiveRuntimeConfig::default();
        assert!(definition_allows_external(&definition, &runtime));
        runtime.capability_mode = Some(SubagentCapabilityMode::ReadOnly);
        assert!(!definition_allows_external(&definition, &runtime));
        runtime.capability_mode = Some(SubagentCapabilityMode::All);
        definition.session_tools_denylist = Some(vec!["bash".into()]);
        assert!(!definition_allows_external(&definition, &runtime));
        definition.session_tools_denylist = None;
        definition.tools.push("read_file".into());
        assert!(!definition_allows_external(&definition, &runtime));
        definition.tools.clear();
        definition.permission_mode = PermissionMode::Plan;
        assert!(!definition_allows_external(&definition, &runtime));
        assert!(!definition_allows_external(
            &xai_grok_agent::config::AgentDefinition::explore(),
            &runtime
        ));
    }

    #[test]
    fn external_session_ids_are_safe_metadata_components() {
        for id in ["task-123", "019e0000-0000-7000-8000-0000000000bb"] {
            assert!(safe_id(id));
        }
        for id in [
            "",
            ".",
            "..",
            "../other",
            "/absolute",
            "nested/name",
            "name\\part",
        ] {
            assert!(!safe_id(id));
        }
    }

    #[test]
    fn only_known_session_tool_permissions_may_be_approved() {
        let mut request = json!({"sessionId":"session", "toolCall":{"toolCallId":"tool_1", "kind":"edit"},
            "options":[{"kind":"allow_once", "optionId":"yes"}]});
        assert_eq!(
            permission_response(Some("session"), "session/request_permission", &request),
            Some(json!({"outcome":{"outcome":"selected", "optionId":"yes"}}))
        );
        assert!(
            permission_response(Some("other"), "session/request_permission", &request).is_none()
        );
        assert!(permission_response(None, "session/request_permission", &request).is_none());
        request["toolCall"]["toolCallId"] = json!("interaction_question");
        assert!(
            permission_response(Some("session"), "session/request_permission", &request).is_none()
        );
        request["toolCall"]["toolCallId"] = json!("tool_2");
        request["toolCall"]["kind"] = json!("other");
        assert!(
            permission_response(Some("session"), "session/request_permission", &request).is_none()
        );
        request["toolCall"]["kind"] = json!("edit");
        request["options"][0]["kind"] = json!("allow_always");
        assert!(
            permission_response(Some("session"), "session/request_permission", &request).is_none()
        );
    }

    #[test]
    fn only_complete_text_diffs_are_eligible_for_attribution() {
        let update = json!({"content":[
            {"type":"diff", "path":"a.txt", "oldText":"before", "newText":"after"},
            {"type":"diff", "path":"b.txt", "newText":"created"},
            {"type":"diff", "path":"missing.txt"},
            {"type":"content", "content":{"type":"text", "text":"no diff"}}
        ]});
        let diffs = reported_diffs(&update);
        assert_eq!(diffs.len(), 2);
        assert_eq!(diffs[0].path, PathBuf::from("a.txt"));
        assert_eq!(diffs[0].old.as_deref(), Some("before"));
        assert_eq!(diffs[0].new, "after");
        assert_eq!(diffs[1].old, None);
    }

    #[tokio::test]
    async fn attribution_requires_matching_contents_inside_child_workspace() {
        let root = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        std::fs::write(root.path().join("file"), "actual").unwrap();
        std::fs::write(outside.path().join("file"), "actual").unwrap();
        let canonical_root = std::fs::canonicalize(root.path()).unwrap();
        let mut diff = ReportedDiff {
            path: "file".into(),
            old: Some("before".into()),
            new: "actual".into(),
        };
        assert!(
            verified_diff_path(&canonical_root, root.path(), &diff)
                .await
                .is_some()
        );
        diff.new = "preview".into();
        assert!(
            verified_diff_path(&canonical_root, root.path(), &diff)
                .await
                .is_none()
        );
        diff.path = outside.path().join("file");
        diff.new = "actual".into();
        assert!(
            verified_diff_path(&canonical_root, root.path(), &diff)
                .await
                .is_none()
        );
    }

    #[tokio::test]
    async fn external_agent_waits_for_durable_export_marker() {
        let boundary = ProviderBoundary::default();
        let (tx, mut rx) = mpsc::unbounded_channel();
        let cancel = CancellationToken::new();
        let prepare = close_parent_export(&boundary, Some(&tx), &cancel);
        let persistence = async {
            let Some(PersistenceMsg::CloseXaiExportAndAck { respond_to }) = rx.recv().await else {
                panic!("expected external export boundary write");
            };
            assert!(!boundary.allows_xai_export());
            respond_to.send(Ok(())).unwrap();
        };
        let (result, ()) = tokio::join!(prepare, persistence);
        assert!(result.is_ok());
        assert!(!boundary.allows_xai_export());
    }

    #[tokio::test]
    async fn missing_or_failed_persistence_keeps_export_closed() {
        let boundary = ProviderBoundary::default();
        let cancel = CancellationToken::new();
        assert!(close_parent_export(&boundary, None, &cancel).await.is_err());
        assert!(!boundary.allows_xai_export());
        let (tx, mut rx) = mpsc::unbounded_channel();
        let prepare = close_parent_export(&boundary, Some(&tx), &cancel);
        let persistence = async {
            let Some(PersistenceMsg::CloseXaiExportAndAck { respond_to }) = rx.recv().await else {
                panic!("expected external export boundary write");
            };
            respond_to
                .send(Err(std::io::Error::other("write failed")))
                .unwrap();
        };
        let (result, ()) = tokio::join!(prepare, persistence);
        assert!(result.is_err());
        assert!(!boundary.allows_xai_export());
    }
}

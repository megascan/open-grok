//! Shared folder identity and approval modes for sandboxed hub calls.
use crate::permission::manager::{
    evaluate_bash_with_ambient, protected_target, record_prompt_outcome, session_grant_for_hub,
};
use crate::permission::state::{CachedStateStore, PermissionState, StateFileAccess};
use crate::permission::{
    AccessKind, Decision, PermissionHookTransport, PromptOutcome, prompt_outcome_allows,
    request_permission_via_hub,
};
use std::path::{Path, PathBuf};
use xai_grok_agent::repo::RepoDirChain;
use xai_grok_paths::AbsPathBuf;
use xai_tool_runtime::{ToolApprovalPolicy, ToolError, ToolErrorKind};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ToolApprovalGate {
    Enforced,
    Off,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum PromptGate {
    PreRun,
    SandboxCard,
}

/// Per-session owner approval and the folder grants it may remember.
#[derive(Default)]
pub(crate) struct SessionApproval {
    policy: parking_lot::Mutex<ToolApprovalPolicy>,
    grants: tokio::sync::Mutex<Option<FolderGrants>>,
}

struct FolderGrants {
    cwd: AbsPathBuf,
    served_root: PathBuf,
    grant_dir: AbsPathBuf,
    store: CachedStateStore,
    state: PermissionState,
    allow_edits: bool,
}

impl FolderGrants {
    async fn load(cwd: AbsPathBuf, root: &Path, access: StateFileAccess) -> Self {
        let grant_dir = grant_dir_for(&cwd, root).await;
        let (store, state) =
            CachedStateStore::resolve_and_load_with(&grant_dir, None, access).await;
        Self {
            cwd,
            served_root: root.to_path_buf(),
            grant_dir,
            store,
            state,
            allow_edits: false,
        }
    }

    async fn pre_decision(
        &mut self,
        access: &AccessKind,
        policy: ToolApprovalPolicy,
    ) -> (Option<Result<(), String>>, bool) {
        if let Some(fresh) = self.store.reload_if_changed().await {
            self.state = fresh;
        }
        let bash = match access {
            AccessKind::Bash(cmd) => {
                let (cmd, state, cwd) = (cmd.clone(), self.state.clone(), self.cwd.clone());
                match tokio::task::spawn_blocking(move || {
                    evaluate_bash_with_ambient(&cmd, &state, cwd.as_path())
                })
                .await
                {
                    Ok(value) => Some(value),
                    Err(_) => return (None, true),
                }
            }
            _ => None,
        };
        let protected = protected_target(access, bash.as_ref(), self.cwd.as_path());
        let pin = (policy != ToolApprovalPolicy::UnattendedAllowed)
            .then_some("blanket bash approval requires unattended access");
        let decision =
            session_grant_for_hub(access, bash.as_ref(), &self.state, self.allow_edits, pin);
        // A remembered denial wins even when the target also needs a prompt.
        if let Some((Decision::Reject(reason) | Decision::PolicyDeny(reason), _)) = &decision {
            return (Some(Err(reason.clone())), protected);
        }
        if protected {
            return (None, true);
        }
        (
            decision.and_then(|(decision, _)| match decision {
                Decision::Allow => Some(Ok(())),
                _ => None,
            }),
            false,
        )
    }

    async fn record(&mut self, access: &AccessKind, outcome: &PromptOutcome) {
        if matches!(outcome, PromptOutcome::AllowEditsForSession) {
            self.allow_edits = true;
        } else if record_prompt_outcome(&mut self.state, access, outcome).is_some() {
            self.store.persist(&self.grant_dir, &self.state).await;
        }
    }
}

impl SessionApproval {
    pub(crate) fn set_policy(&self, policy: ToolApprovalPolicy) {
        *self.policy.lock() = policy;
    }
    pub(crate) fn policy(&self) -> ToolApprovalPolicy {
        *self.policy.lock()
    }

    /// Settle a parsed call using the owner's transport. Missing transport and
    /// malformed cwd fail closed. The caller selects the gate after sandbox mode
    /// and ensures the tool was parsed before invoking this method.
    pub(crate) async fn settle(
        &self,
        cwd: &Path,
        served_root: &Path,
        access: &AccessKind,
        call_id: &str,
        transport: Option<&dyn PermissionHookTransport>,
        gate: PromptGate,
        store_access: StateFileAccess,
        yolo: bool,
    ) -> Result<(), ToolError> {
        if matches!(
            access,
            AccessKind::Read(_) | AccessKind::Grep { .. } | AccessKind::WebSearch(_)
        ) {
            return Ok(());
        }
        let policy = self.policy();
        let mut slot = self.grants.lock().await;
        if slot.as_ref().is_none_or(|folder| {
            *folder.store.access() != store_access
                || folder.cwd.as_path() != cwd
                || folder.served_root != served_root
        }) {
            let cwd = AbsPathBuf::new(cwd.to_path_buf()).map_err(|_| {
                ToolError::new(
                    ToolErrorKind::PermissionDenied,
                    "invalid permission working directory",
                )
            })?;
            *slot = Some(FolderGrants::load(cwd, served_root, store_access).await);
        }
        let mut folder = slot.as_mut();
        let mut force_prompt = false;
        if let Some(folder) = &mut folder {
            let (decision, protected) = folder.pre_decision(access, policy).await;
            force_prompt = protected;
            if let Some(result) = decision
                && (result.is_err() || policy != ToolApprovalPolicy::AlwaysPrompt)
            {
                return result
                    .map_err(|reason| ToolError::new(ToolErrorKind::PermissionDenied, reason));
            }
        }
        if !force_prompt && policy == ToolApprovalPolicy::UnattendedAllowed && yolo {
            return Ok(());
        }
        if !force_prompt
            && gate == PromptGate::SandboxCard
            && policy != ToolApprovalPolicy::AlwaysPrompt
        {
            return Ok(());
        }
        let transport = transport.ok_or_else(|| {
            ToolError::new(
                ToolErrorKind::PermissionDenied,
                "tool permission unavailable (no owner transport)",
            )
        })?;
        let outcome = request_permission_via_hub(transport, access, call_id, None).await;
        if policy != ToolApprovalPolicy::AlwaysPrompt {
            if let Some(folder) = &mut folder {
                folder.record(access, &outcome).await;
            }
        }
        if prompt_outcome_allows(&outcome) {
            Ok(())
        } else {
            Err(ToolError::new(
                ToolErrorKind::PermissionDenied,
                "tool permission denied",
            ))
        }
    }
}

/// Share a repo-less desktop scratch directory's grants with the served root;
/// repository-backed sessions retain their own repository-root key.
pub(crate) async fn grant_dir_for(cwd: &AbsPathBuf, served_root: &Path) -> AbsPathBuf {
    if cwd.as_path() == served_root {
        return cwd.clone();
    }
    let Ok(root) = AbsPathBuf::new(served_root.to_path_buf()) else {
        return cwd.clone();
    };
    let (probe_cwd, probe_root) = (cwd.clone(), root.clone());
    let under_root_outside_repo = tokio::task::spawn_blocking(move || {
        let canonical =
            |path: &Path| dunce::canonicalize(path).unwrap_or_else(|_| path.to_path_buf());
        canonical(probe_cwd.as_path()).starts_with(canonical(probe_root.as_path()))
            && RepoDirChain::resolve(probe_cwd.as_path())
                .git_root
                .is_none()
    })
    .await
    .unwrap_or(false);
    if under_root_outside_repo {
        root
    } else {
        cwd.clone()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use async_trait::async_trait;
    use serde_json::{Value, json};
    use std::sync::atomic::{AtomicUsize, Ordering};

    struct Owner {
        calls: AtomicUsize,
        reply: Result<Value, String>,
    }

    #[async_trait]
    impl PermissionHookTransport for Owner {
        async fn request_permission(&self, _: Value) -> Result<Value, String> {
            self.calls.fetch_add(1, Ordering::SeqCst);
            self.reply.clone()
        }
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn remembered_denial_beats_grant_even_when_always_prompt_or_yolo() {
        let tmp = tempfile::tempdir().unwrap();
        let cwd = AbsPathBuf::new(tmp.path().join("project")).unwrap();
        std::fs::create_dir(cwd.as_path()).unwrap();
        let home = tmp.path().join("home");
        std::fs::create_dir(&home).unwrap();
        let access = StateFileAccess::DaemonOwned { grok_home: home };
        let (store, mut state) =
            CachedStateStore::resolve_and_load_with(&cwd, None, access.clone()).await;
        state.allowed_mcp_tools.insert("example__tool".into());
        state.disallowed_mcp_tools.insert("example__tool".into());
        store.persist(&cwd, &state).await;
        let request = AccessKind::MCPTool {
            name: "example__tool".into(),
            input: Value::Null,
        };
        let owner = Owner {
            calls: AtomicUsize::new(0),
            reply: Ok(json!({"outcome": "approve"})),
        };
        let approval = SessionApproval::default();
        for policy in [
            ToolApprovalPolicy::AlwaysPrompt,
            ToolApprovalPolicy::GrantsAllowed,
            ToolApprovalPolicy::UnattendedAllowed,
        ] {
            approval.set_policy(policy);
            assert!(
                approval
                    .settle(
                        cwd.as_path(),
                        cwd.as_path(),
                        &request,
                        "1",
                        Some(&owner),
                        PromptGate::PreRun,
                        access.clone(),
                        true
                    )
                    .await
                    .is_err()
            );
        }
        assert_eq!(owner.calls.load(Ordering::SeqCst), 0);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn always_prompt_ignores_grants_and_missing_or_failed_owner_rejects() {
        let tmp = tempfile::tempdir().unwrap();
        let cwd = AbsPathBuf::new(tmp.path().join("project")).unwrap();
        std::fs::create_dir(cwd.as_path()).unwrap();
        let home = tmp.path().join("home");
        std::fs::create_dir(&home).unwrap();
        let access = StateFileAccess::DaemonOwned { grok_home: home };
        let (store, mut state) =
            CachedStateStore::resolve_and_load_with(&cwd, None, access.clone()).await;
        state.allowed_mcp_tools.insert("example__tool".into());
        store.persist(&cwd, &state).await;
        let request = AccessKind::MCPTool {
            name: "example__tool".into(),
            input: Value::Null,
        };
        let owner = Owner {
            calls: AtomicUsize::new(0),
            reply: Ok(json!({"outcome": "approve"})),
        };
        let approval = SessionApproval::default();
        assert!(
            approval
                .settle(
                    cwd.as_path(),
                    cwd.as_path(),
                    &request,
                    "1",
                    None,
                    PromptGate::PreRun,
                    access.clone(),
                    false
                )
                .await
                .is_ok()
        );
        approval.set_policy(ToolApprovalPolicy::AlwaysPrompt);
        assert!(
            approval
                .settle(
                    cwd.as_path(),
                    cwd.as_path(),
                    &request,
                    "2",
                    None,
                    PromptGate::PreRun,
                    access.clone(),
                    true
                )
                .await
                .is_err()
        );
        assert!(
            approval
                .settle(
                    cwd.as_path(),
                    cwd.as_path(),
                    &request,
                    "3",
                    Some(&owner),
                    PromptGate::PreRun,
                    access.clone(),
                    true
                )
                .await
                .is_ok()
        );
        assert_eq!(owner.calls.load(Ordering::SeqCst), 1);
        let failed = Owner {
            calls: AtomicUsize::new(0),
            reply: Err("owner disconnected".into()),
        };
        assert!(
            approval
                .settle(
                    cwd.as_path(),
                    cwd.as_path(),
                    &request,
                    "4",
                    Some(&failed),
                    PromptGate::PreRun,
                    access,
                    true
                )
                .await
                .is_err()
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn built_in_safe_command_list_is_not_a_hub_grant() {
        let tmp = tempfile::tempdir().unwrap();
        let cwd = tmp.path().join("project");
        let home = tmp.path().join("home");
        std::fs::create_dir(&cwd).unwrap();
        std::fs::create_dir(&home).unwrap();
        let approval = SessionApproval::default();
        let access = StateFileAccess::DaemonOwned { grok_home: home };
        assert!(
            approval
                .settle(
                    &cwd,
                    &cwd,
                    &AccessKind::Bash("pwd".into()),
                    "1",
                    None,
                    PromptGate::PreRun,
                    access,
                    false
                )
                .await
                .is_err()
        );
    }
}

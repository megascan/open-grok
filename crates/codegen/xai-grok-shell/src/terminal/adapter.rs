//! `AcpTerminalAdapter`: implements `xai-grok-tools::TerminalBackend` over ACP
//! gateway calls, for bash execution when the terminal is served by the client.

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use super::exit_watcher::{poll_for_terminal_exit, release_terminal, watch_for_exit};
use super::output_recorder::{OutputRecorder, read_log_tail};
use agent_client_protocol as acp;
use xai_acp_lib::{AcpAgentGatewaySender as GatewaySender, acp_channel_failure};
use xai_grok_tools::computer::types::{
    BackgroundHandle, ComputerError, KillOutcome, KillSource, TaskKind, TaskSnapshot,
    TerminalBackend, TerminalRunRequest, TerminalRunResult,
};
use xai_grok_tools::notification::{
    BashExecutionBackgrounded, BashNotificationBase, ToolNotificationHandle,
};

/// A snapshot's per-completion fields, grouped to avoid transposed positional args.
#[derive(Clone)]
pub(super) struct SnapshotOutput {
    pub(super) output: String,
    pub(super) truncated: bool,
    pub(super) exit_code: Option<i32>,
    pub(super) signal: Option<String>,
}

pub(super) struct TrackedTask {
    command: String,
    raw_command: String,
    tool_call_id: String,
    pub(super) notification_handle: Option<ToolNotificationHandle>,
    teardown_claimed: bool,
    display_command: Option<String>,
    cwd: String,
    output_file: PathBuf,
    start_time: std::time::SystemTime,
    /// Stamped once when the task completes, so repeated snapshots agree.
    end_time: Option<std::time::SystemTime>,
    pub(super) completed: bool,
    exit_code: Option<i32>,
    signal: Option<String>,
    last_output: String,
    last_truncated: bool,
    block_waited: bool,
    explicitly_killed: bool,
    kill_result_delivered: bool,
    /// In-flight `wait_for_completion` callers. ACP has no oneshot list;
    /// this is the live-waiter count used for kill delivery.
    live_waiters: usize,
    kind: TaskKind,
    owner_session_id: Option<String>,
    description: Option<String>,
    output_byte_limit: usize,
}

/// Hand-written (`SystemTime` has no `Default`); call sites spread from it.
impl Default for TrackedTask {
    fn default() -> Self {
        Self {
            command: String::new(),
            raw_command: String::new(),
            tool_call_id: String::new(),
            notification_handle: None,
            teardown_claimed: false,
            display_command: None,
            cwd: String::new(),
            output_file: PathBuf::new(),
            start_time: std::time::SystemTime::now(),
            end_time: None,
            completed: false,
            exit_code: None,
            signal: None,
            last_output: String::new(),
            last_truncated: false,
            block_waited: false,
            explicitly_killed: false,
            kill_result_delivered: false,
            live_waiters: 0,
            kind: TaskKind::Bash,
            owner_session_id: None,
            description: None,
            output_byte_limit: crate::terminal::DEFAULT_OUTPUT_BYTE_LIMIT,
        }
    }
}

impl TrackedTask {
    pub(super) fn mark_completed(&mut self, out: SnapshotOutput) {
        self.completed = true;
        self.end_time = Some(std::time::SystemTime::now());
        self.exit_code = out.exit_code;
        self.signal = out.signal;
        self.last_output = out.output;
        self.last_truncated = out.truncated;
    }

    pub(super) fn to_snapshot(&self, task_id: &str, out: SnapshotOutput) -> TaskSnapshot {
        let completed = self.completed || out.exit_code.is_some() || out.signal.is_some();
        TaskSnapshot {
            task_id: task_id.to_string(),
            command: self.command.clone(),
            display_command: self.display_command.clone(),
            cwd: self.cwd.clone(),
            start_time: self.start_time,
            end_time: self
                .end_time
                .or_else(|| completed.then(std::time::SystemTime::now)),
            output: out.output,
            output_file: self.output_file.clone(),
            truncated: out.truncated,
            exit_code: out.exit_code,
            signal: out.signal,
            completed,
            block_waited: self.block_waited,
            explicitly_killed: self.explicitly_killed,
            kill_result_delivered: self.kill_result_delivered,
            kind: self.kind,
            owner_session_id: self.owner_session_id.clone(),
            description: self.description.clone(),
            // ACP tracked tasks are only registered via run_background.
            is_backgrounded: true,
            output_total_bytes: 0,
        }
    }
}

pub(super) type TaskMap = Arc<Mutex<HashMap<String, TrackedTask>>>;

#[must_use]
struct LiveWaiter<'a> {
    tasks: TaskMap,
    task_id: &'a str,
    /// Set on the normal return path so Drop does not treat this wait as
    /// cancelled. A cancelled wait must clear `block_waited` immediately:
    /// ClientUi kill only does that after the kill RPC, and the exit
    /// watcher can emit `TaskCompleted` in that gap.
    finished: bool,
}

impl Drop for LiveWaiter<'_> {
    fn drop(&mut self) {
        if let Ok(mut tasks) = self.tasks.lock()
            && let Some(task) = tasks.get_mut(self.task_id)
        {
            task.live_waiters = task.live_waiters.saturating_sub(1);
            if task.live_waiters == 0 && !self.finished {
                task.block_waited = false;
            }
        }
    }
}

fn wrap_command(command: &str) -> Result<String, ComputerError> {
    #[cfg(not(unix))]
    {
        let _ = command;
        Ok(command.to_string())
    }
    #[cfg(unix)]
    {
        let quoted = shlex::try_quote(command).map_err(|_| ComputerError::CommandNotQuoted)?;
        Ok(format!(
            "{} -lc {quoted}",
            crate::terminal::default_shell_path()
        ))
    }
}

fn to_env(env: HashMap<String, String>) -> Vec<acp::EnvVariable> {
    env.into_iter()
        .map(|(name, value)| acp::EnvVariable::new(name, value))
        .collect()
}

pub(super) fn parse_exit(
    status: &Option<acp::TerminalExitStatus>,
) -> (Option<i32>, Option<String>) {
    match status {
        Some(e) => (e.exit_code.map(|v| v as i32), e.signal.clone()),
        None => (None, None),
    }
}

/// Wraps xai-grok-shell's ACP gateway to satisfy xai-grok-tools' TerminalBackend.
pub struct AcpTerminalAdapter {
    gateway: GatewaySender,
    session_id: acp::SessionId,
    /// Parent session runtime owns background watchers even when a child uses
    /// this shared adapter and its runtime is subsequently shut down.
    owner_runtime: tokio::runtime::Handle,
    tasks: TaskMap,
    foreground: Arc<Mutex<HashMap<String, Option<String>>>>,
    sandbox_guard: Option<Arc<dyn AcpTerminalSandboxGuard>>,
}

#[async_trait::async_trait]
trait AcpTerminalSandboxGuard: Send + Sync {
    async fn requires_local_enforcement(&self) -> Result<bool, ComputerError>;
}

#[async_trait::async_trait]
impl AcpTerminalSandboxGuard for xai_grok_workspace::WorkspaceOps {
    async fn requires_local_enforcement(&self) -> Result<bool, ComputerError> {
        self.sandbox_requires_local_enforcement()
            .await
            .map_err(|error| {
                ComputerError::io(format!("Could not verify workspace sandbox: {error}"))
            })
    }
}

impl AcpTerminalAdapter {
    pub fn new(gateway: GatewaySender, session_id: acp::SessionId) -> Self {
        Self {
            gateway,
            session_id,
            owner_runtime: tokio::runtime::Handle::current(),
            tasks: Arc::new(Mutex::new(HashMap::new())),
            foreground: Arc::new(Mutex::new(HashMap::new())),
            sandbox_guard: None,
        }
    }

    pub(crate) fn with_workspace_sandbox(
        mut self,
        workspace: xai_grok_workspace::WorkspaceOps,
    ) -> Self {
        self.sandbox_guard = Some(Arc::new(workspace));
        self
    }

    async fn create_terminal(
        &self,
        command: String,
        request: &TerminalRunRequest,
    ) -> Result<acp::CreateTerminalResponse, ComputerError> {
        if let Some(guard) = &self.sandbox_guard
            && guard.requires_local_enforcement().await?
        {
            return Err(ComputerError::io(
                "Workspace sandbox enforcement requires a local terminal; the ACP client terminal cannot enforce it",
            ));
        }
        if request.notification_handle.is_delivery_revoked() {
            return Err(ComputerError::io("originating turn was cancelled"));
        }
        self.gateway
            .send(
                acp::CreateTerminalRequest::new(self.session_id.clone(), command)
                    .args(vec![])
                    .env(to_env(request.env.clone()))
                    .cwd(Some(request.working_directory.clone()))
                    .output_byte_limit(Some(request.output_byte_limit as u64)),
            )
            .await
            .map_err(|e| ComputerError::io(e.to_string()))
    }

    fn terminal_id(&self, task_id: &str) -> acp::TerminalId {
        acp::TerminalId::new(task_id)
    }

    async fn kill_and_release(&self, id: &acp::TerminalId) -> bool {
        let result = tokio::time::timeout(Duration::from_secs(5), async {
            self.gateway
                .send(acp::KillTerminalRequest::new(
                    self.session_id.clone(),
                    id.clone(),
                ))
                .await?;
            self.gateway
                .send(acp::ReleaseTerminalRequest::new(
                    self.session_id.clone(),
                    id.clone(),
                ))
                .await?;
            Ok::<_, acp::Error>(())
        })
        .await;
        match result {
            Ok(Ok(())) => true,
            Ok(Err(error)) => {
                tracing::warn!(terminal_id = %id.0, %error, "ACP terminal cleanup was not confirmed");
                false
            }
            Err(_) => {
                tracing::warn!(terminal_id = %id.0, "ACP terminal cleanup timed out; termination is unconfirmed");
                false
            }
        }
    }

    async fn kill_foreground_owned(&self, owner: Option<&str>) {
        let ids = {
            let foreground = self.foreground.lock().unwrap();
            foreground
                .iter()
                .filter(|(_, task_owner)| {
                    owner.is_none_or(|owner| task_owner.as_deref() == Some(owner))
                })
                .map(|(id, _)| id.clone())
                .collect::<Vec<_>>()
        };
        futures::future::join_all(ids.into_iter().map(|id| async move {
            if self.kill_and_release(&self.terminal_id(&id)).await {
                self.foreground.lock().unwrap().remove(&id);
            }
        }))
        .await;
    }

    async fn kill_background_for_teardown(&self, task_id: &str) {
        let result = tokio::time::timeout(
            Duration::from_secs(5),
            self.gateway.send(acp::KillTerminalRequest::new(
                self.session_id.clone(),
                self.terminal_id(task_id),
            )),
        )
        .await;
        match result {
            Ok(Ok(_)) => {
                if let Some(task) = self.tasks.lock().unwrap().get_mut(task_id) {
                    task.explicitly_killed = true;
                }
            }
            Ok(Err(error)) => {
                tracing::warn!(task_id, %error, "ACP background termination was not confirmed")
            }
            Err(_) => tracing::warn!(
                task_id,
                "ACP background termination timed out; termination is unconfirmed"
            ),
        }
    }

    fn claim_background_for_teardown(&self, owner: Option<&str>) -> Vec<String> {
        let mut tasks = self.tasks.lock().unwrap();
        tasks
            .iter_mut()
            .filter_map(|(id, task)| {
                if task.completed
                    || !owner.is_none_or(|owner| task.owner_session_id.as_deref() == Some(owner))
                {
                    return None;
                }
                // Reparent uses this same lock and rejects claimed tasks, so a
                // child sweep cannot kill work already transferred to the parent.
                task.teardown_claimed = true;
                task.kill_result_delivered = true;
                task.block_waited = false;
                Some(id.clone())
            })
            .collect()
    }
}

struct ForegroundRegistration {
    entries: Arc<Mutex<HashMap<String, Option<String>>>>,
    id: String,
    confirmed_exit: bool,
}
impl Drop for ForegroundRegistration {
    fn drop(&mut self) {
        if self.confirmed_exit {
            self.entries.lock().unwrap().remove(&self.id);
        }
    }
}

#[async_trait::async_trait]
impl TerminalBackend for AcpTerminalAdapter {
    async fn run(&self, request: TerminalRunRequest) -> Result<TerminalRunResult, ComputerError> {
        let command = wrap_command(&request.command)?;
        let create_res = self.create_terminal(command, &request).await?;
        let id = create_res.terminal_id.0.to_string();
        let admitted = {
            let mut foreground = self.foreground.lock().unwrap();
            if request.notification_handle.is_delivery_revoked() {
                false
            } else {
                foreground.insert(id.clone(), request.owner_session_id.clone());
                true
            }
        };
        if !admitted {
            if !self.kill_and_release(&create_res.terminal_id).await {
                self.foreground
                    .lock()
                    .unwrap()
                    .insert(id, request.owner_session_id.clone());
            }
            return Err(ComputerError::io("originating turn was cancelled"));
        }
        let mut registration = ForegroundRegistration {
            entries: self.foreground.clone(),
            id,
            confirmed_exit: false,
        };

        let timed_out = match tokio::time::timeout(
            request.timeout,
            self.gateway.send(acp::WaitForTerminalExitRequest::new(
                self.session_id.clone(),
                create_res.terminal_id.clone(),
            )),
        )
        .await
        {
            Ok(Ok(_)) => {
                registration.confirmed_exit = true;
                false
            }
            Ok(Err(e)) => {
                release_terminal(&self.gateway, &self.session_id, &create_res.terminal_id).await;
                return Err(ComputerError::io(e.to_string()));
            }
            Err(_) => {
                let killed = self
                    .gateway
                    .send(acp::KillTerminalRequest::new(
                        self.session_id.clone(),
                        create_res.terminal_id.clone(),
                    ))
                    .await;
                registration.confirmed_exit = killed.is_ok();
                true
            }
        };

        let output = match self
            .gateway
            .send(acp::TerminalOutputRequest::new(
                self.session_id.clone(),
                create_res.terminal_id.clone(),
            ))
            .await
        {
            Ok(output) => output,
            Err(e) => {
                release_terminal(&self.gateway, &self.session_id, &create_res.terminal_id).await;
                return Err(ComputerError::io(e.to_string()));
            }
        };

        release_terminal(&self.gateway, &self.session_id, &create_res.terminal_id).await;

        let (exit_code, signal) = parse_exit(&output.exit_status);
        let total_bytes = output.output.len();

        let mut recorder =
            OutputRecorder::new(request.output_file.clone(), request.output_byte_limit);
        recorder.initialize().await;
        if let Err(e) = recorder.append(&output.output).await {
            tracing::warn!(error = %e, "output recorder failed to write foreground output");
        }

        Ok(TerminalRunResult {
            combined_output: output.output,
            exit_code,
            truncated: output.truncated,
            signal,
            timed_out,
            output_file: request.output_file,
            total_bytes,
            pid: None,
        })
    }

    async fn run_background(
        &self,
        request: TerminalRunRequest,
    ) -> Result<BackgroundHandle, ComputerError> {
        let command = wrap_command(&request.command)?;
        let notification_handle = request.notification_handle.clone();
        let display_command = request.display_command.clone();
        let cwd = request.working_directory.to_string_lossy().to_string();
        let output_file = request.output_file.clone();

        let create_res = self.create_terminal(command.clone(), &request).await?;
        let task_id = create_res.terminal_id.0.to_string();
        let description = request.description;

        let admitted = {
            let mut tasks = self.tasks.lock().unwrap();
            if request.notification_handle.is_delivery_revoked() {
                false
            } else {
                tasks.insert(
                    task_id.clone(),
                    TrackedTask {
                        command,
                        raw_command: request.command.clone(),
                        tool_call_id: request.tool_call_id.clone(),
                        notification_handle: Some(notification_handle.clone()),
                        display_command,
                        cwd,
                        output_file: output_file.clone(),
                        kind: request.kind,
                        owner_session_id: request.owner_session_id.clone(),
                        description,
                        output_byte_limit: request.output_byte_limit,
                        ..Default::default()
                    },
                );
                true
            }
        };
        if !admitted {
            if !self.kill_and_release(&create_res.terminal_id).await {
                self.foreground
                    .lock()
                    .unwrap()
                    .insert(task_id, request.owner_session_id.clone());
            }
            return Err(ComputerError::io("originating turn was cancelled"));
        }

        let recorder = OutputRecorder::new(output_file.clone(), request.output_byte_limit);
        recorder.initialize().await;
        self.owner_runtime.spawn(watch_for_exit(
            self.gateway.clone(),
            self.session_id.clone(),
            task_id.clone(),
            Arc::clone(&self.tasks),
            notification_handle,
            recorder,
        ));

        Ok(BackgroundHandle {
            task_id,
            output_file,
            pid: None,
        })
    }

    async fn get_task(&self, task_id: &str) -> Option<TaskSnapshot> {
        let live = self
            .gateway
            .send(acp::TerminalOutputRequest::new(
                self.session_id.clone(),
                self.terminal_id(task_id),
            ))
            .await
            .ok();

        // The std Mutex guard cannot be held across the await below, so resolve
        // under the lock and read the log file after releasing it.
        enum Resolved {
            Ready(TaskSnapshot),
            FromLog {
                snapshot: TaskSnapshot,
                output_file: PathBuf,
                limit: usize,
            },
            Missing,
        }
        let resolved = {
            let tasks = self.tasks.lock().unwrap();
            match (live, tasks.get(task_id)) {
                (Some(output), Some(tracked)) => {
                    let (exit_code, signal) = parse_exit(&output.exit_status);
                    Resolved::Ready(tracked.to_snapshot(
                        task_id,
                        SnapshotOutput {
                            output: output.output,
                            truncated: output.truncated,
                            exit_code,
                            signal,
                        },
                    ))
                }
                (Some(output), None) => {
                    let (exit_code, signal) = parse_exit(&output.exit_status);
                    Resolved::Ready(TrackedTask::default().to_snapshot(
                        task_id,
                        SnapshotOutput {
                            output: output.output,
                            truncated: output.truncated,
                            exit_code,
                            signal,
                        },
                    ))
                }
                // Live poll failed: a completed task keeps its authoritative
                // last_output; only a still-running task falls back to the log.
                (None, Some(tracked)) => {
                    let snapshot = tracked.to_snapshot(
                        task_id,
                        SnapshotOutput {
                            output: tracked.last_output.clone(),
                            truncated: tracked.last_truncated,
                            exit_code: tracked.exit_code,
                            signal: tracked.signal.clone(),
                        },
                    );
                    if snapshot.completed {
                        Resolved::Ready(snapshot)
                    } else {
                        Resolved::FromLog {
                            snapshot,
                            output_file: tracked.output_file.clone(),
                            limit: tracked.output_byte_limit,
                        }
                    }
                }
                (None, None) => Resolved::Missing,
            }
        };

        match resolved {
            Resolved::Ready(snapshot) => Some(snapshot),
            Resolved::Missing => None,
            Resolved::FromLog {
                mut snapshot,
                output_file,
                limit,
            } => {
                if let Some(tail) = read_log_tail(&output_file, limit).await {
                    snapshot.output = tail.text;
                    snapshot.truncated = tail.truncated;
                }
                Some(snapshot)
            }
        }
    }

    async fn kill_task(&self, task_id: &str) -> KillOutcome {
        self.kill_task_with_source(task_id, KillSource::ModelTool)
            .await
    }

    async fn kill_task_with_source(&self, task_id: &str, source: KillSource) -> KillOutcome {
        #[derive(Copy, Clone)]
        enum Tracked {
            Running,
            Completed,
            Unknown,
        }
        let tracked = {
            let mut tasks = self.tasks.lock().unwrap();
            match tasks.get_mut(task_id) {
                Some(task) if task.completed => Tracked::Completed,
                Some(task) => {
                    task.explicitly_killed = true;
                    // ModelTool/Teardown suppress auto-wake immediately
                    // (`marks_result_delivered(false)` is true). ClientUi
                    // waits until after the kill RPC so a cancelled waiter
                    // is visible. Setting only `explicitly_killed` here
                    // would let the exit watcher emit TaskCompleted with
                    // `kill_result_delivered` still false.
                    if source.marks_result_delivered(false) {
                        task.kill_result_delivered = true;
                    }
                    if task.live_waiters == 0 {
                        task.block_waited = false;
                    }
                    Tracked::Running
                }
                None => Tracked::Unknown,
            }
        };

        match tracked {
            Tracked::Completed => return KillOutcome::AlreadyExited,
            Tracked::Running => {}
            Tracked::Unknown => {
                let probe = self
                    .gateway
                    .send(acp::TerminalOutputRequest::new(
                        self.session_id.clone(),
                        self.terminal_id(task_id),
                    ))
                    .await;
                match probe {
                    Err(err) if acp_channel_failure(&err).is_none() => {
                        return KillOutcome::NotFound;
                    }
                    Err(_) => {}
                    Ok(output) if output.exit_status.is_some() => {
                        return KillOutcome::AlreadyExited;
                    }
                    Ok(_) => {}
                }
            }
        }

        let outcome = match self
            .gateway
            .send(acp::KillTerminalRequest::new(
                self.session_id.clone(),
                self.terminal_id(task_id),
            ))
            .await
        {
            Ok(_) => KillOutcome::Killed,
            Err(_) => KillOutcome::NotFound,
        };
        // ClientUi: sample waiters after the RPC so a wait cancelled at
        // its own `.await` (Drop of `LiveWaiter`) is not still counted as
        // delivered. ACP has no oneshot list; this is the closest we get
        // to local's `reply.send(...).is_ok()`. ModelTool/Teardown already
        // marked delivered under the first lock.
        if matches!(tracked, Tracked::Running)
            && !source.marks_result_delivered(false)
            && let Ok(mut tasks) = self.tasks.lock()
            && let Some(task) = tasks.get_mut(task_id)
        {
            let any_delivered = task.live_waiters > 0;
            task.kill_result_delivered = any_delivered;
            if !any_delivered {
                task.block_waited = false;
            }
        }
        outcome
    }

    async fn wait_for_completion(
        &self,
        task_id: &str,
        timeout: Option<Duration>,
    ) -> Option<TaskSnapshot> {
        let timeout = timeout.unwrap_or(Duration::from_secs(30));

        enum WaitStart {
            Immediate,
            Block,
            ProbeThenMaybeBlock,
        }
        let start = {
            let mut tasks = self.tasks.lock().unwrap();
            match tasks.get_mut(task_id) {
                Some(task) => {
                    task.block_waited = true;
                    if task.completed {
                        WaitStart::Immediate
                    } else {
                        task.live_waiters = task.live_waiters.saturating_add(1);
                        WaitStart::Block
                    }
                }
                None => WaitStart::ProbeThenMaybeBlock,
            }
        };
        match start {
            WaitStart::Immediate => return self.get_task(task_id).await,
            WaitStart::ProbeThenMaybeBlock => {
                // Not tracked as running: one output probe. A dead or unknown
                // terminal must not burn the wait budget on WaitForTerminalExit.
                // Match kill's untracked probe: `get_task` maps every
                // TerminalOutput error to None, so a transport/channel blip
                // on a still-live resumed terminal must not look like
                // not-found. Only a client that answered and disowned the
                // id is a definitive miss.
                let probe = self
                    .gateway
                    .send(acp::TerminalOutputRequest::new(
                        self.session_id.clone(),
                        self.terminal_id(task_id),
                    ))
                    .await;
                match probe {
                    Err(err) if acp_channel_failure(&err).is_none() => return None,
                    Err(_) => {}
                    Ok(output) if output.exit_status.is_some() => {
                        let (exit_code, signal) = parse_exit(&output.exit_status);
                        return Some(TrackedTask::default().to_snapshot(
                            task_id,
                            SnapshotOutput {
                                output: output.output,
                                truncated: output.truncated,
                                exit_code,
                                signal,
                            },
                        ));
                    }
                    Ok(_) => {}
                }
            }
            WaitStart::Block => {}
        }
        let mut live_waiter = LiveWaiter {
            tasks: self.tasks.clone(),
            task_id,
            finished: false,
        };

        let gateway_result = tokio::time::timeout(
            timeout,
            self.gateway.send(acp::WaitForTerminalExitRequest::new(
                self.session_id.clone(),
                self.terminal_id(task_id),
            )),
        )
        .await;

        match &gateway_result {
            Ok(Ok(_)) => {}
            Ok(Err(e)) => {
                let completed_meanwhile = {
                    let tasks = self.tasks.lock().unwrap();
                    tasks.get(task_id).is_some_and(|task| task.completed)
                };
                if !completed_meanwhile {
                    tracing::warn!(task_id, error = %e, "gateway error waiting for terminal exit, falling back to polling");
                    let deadline = tokio::time::Instant::now() + timeout;
                    poll_for_terminal_exit(
                        &self.gateway,
                        &self.session_id,
                        &self.terminal_id(task_id),
                        Some(deadline),
                    )
                    .await;
                }
            }
            Err(_) => {
                tracing::debug!(task_id, "timeout waiting for terminal exit");
                let mut tasks = self.tasks.lock().unwrap();
                if let Some(task) = tasks.get_mut(task_id) {
                    task.block_waited = false;
                }
            }
        }

        let snapshot = self.get_task(task_id).await;
        live_waiter.finished = true;
        snapshot
    }

    async fn list_tasks(&self) -> Vec<TaskSnapshot> {
        let task_ids: Vec<String> = {
            let tasks = self.tasks.lock().unwrap();
            tasks.keys().cloned().collect()
        };
        let mut snapshots = Vec::new();
        for task_id in task_ids {
            if let Some(snapshot) = self.get_task(&task_id).await {
                snapshots.push(snapshot);
            }
        }
        snapshots
    }

    async fn kill_all_background_tasks(&self) {
        let task_ids = self.claim_background_for_teardown(None);
        futures::future::join_all(
            task_ids
                .iter()
                .map(|id| self.kill_background_for_teardown(id)),
        )
        .await;
    }

    async fn kill_all_background_tasks_by_owner(&self, owner: &str) {
        let task_ids = self.claim_background_for_teardown(Some(owner));
        futures::future::join_all(
            task_ids
                .iter()
                .map(|id| self.kill_background_for_teardown(id)),
        )
        .await;
    }

    async fn kill_foreground_commands(&self) {
        self.kill_foreground_owned(None).await;
        let session_id = self.session_id.0.to_string();
        crate::terminal::kill_and_release_all_for_session(&session_id).await;
    }

    async fn kill_foreground_commands_by_owner(&self, owner: &str) {
        self.kill_foreground_owned(Some(owner)).await;
    }

    async fn reparent_notifications(
        &self,
        old_owner: &str,
        new_owner: &str,
        new_handle: ToolNotificationHandle,
        backend_weak: std::sync::Weak<dyn TerminalBackend>,
    ) -> Result<(), String> {
        let mut monitors = Vec::new();
        {
            let mut tasks = self.tasks.lock().unwrap();
            if tasks.values().any(|task| {
                !task.completed
                    && task.teardown_claimed
                    && task.owner_session_id.as_deref() == Some(old_owner)
            }) {
                return Err("child background task teardown is already in progress".to_owned());
            }
            for (task_id, task) in tasks.iter_mut() {
                if task.completed || task.owner_session_id.as_deref() != Some(old_owner) {
                    continue;
                }
                task.owner_session_id = Some(new_owner.to_owned());
                task.notification_handle = Some(new_handle.clone());
                let monitor = task.kind == TaskKind::Monitor;
                let monitor_description = monitor
                    .then(|| {
                        task.display_command
                            .as_deref()
                            .and_then(|s| s.strip_prefix("[monitor] "))
                            .filter(|s| !s.trim().is_empty())
                            .map(str::to_owned)
                    })
                    .flatten();
                let description = task
                    .description
                    .clone()
                    .filter(|s| !s.trim().is_empty())
                    .or_else(|| monitor_description.clone());
                let command = if monitor {
                    task.raw_command.clone()
                } else {
                    task.display_command
                        .clone()
                        .unwrap_or_else(|| task.raw_command.clone())
                };
                // The exit watcher uses this same lock, so the parent always
                // observes the synthetic start before the completion.
                new_handle.send_backgrounded(BashExecutionBackgrounded {
                    base: BashNotificationBase {
                        tool_call_id: task.tool_call_id.clone(),
                        command,
                        output: Vec::new(),
                        total_bytes: 0,
                        truncated: false,
                        cwd: PathBuf::from(&task.cwd),
                    },
                    output_file: task.output_file.clone(),
                    task_id: task_id.clone(),
                    monitor_description,
                    description: description.clone(),
                });
                if monitor {
                    let offset = std::fs::metadata(&task.output_file)
                        .map(|m| m.len())
                        .unwrap_or(0);
                    monitors.push((
                        task_id.clone(),
                        description.unwrap_or_else(|| task.raw_command.clone()),
                        task.output_file.clone(),
                        offset,
                    ));
                }
            }
        }
        for (task_id, description, output_file, offset) in monitors {
            let handle = new_handle.clone();
            let backend = backend_weak.clone();
            tokio::spawn(async move {
                xai_grok_tools::implementations::grok_build::monitor::tool::run_monitor_pipeline(
                    &task_id,
                    &description,
                    backend,
                    &handle,
                    &output_file,
                    Some("kill_command_or_subagent".to_owned()),
                    offset,
                )
                .await;
            });
        }
        Ok(())
    }
}

#[cfg(test)]
#[path = "adapter_tests.rs"]
mod tests;

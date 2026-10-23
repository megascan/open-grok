use crate::extensions::notification::{
    SessionNotification as XaiSessionNotification, SessionUpdate as XaiSessionUpdate,
    WorkflowAgentInfo, WorkflowPhaseInfo,
};
use crate::session::persistence::PersistenceMsg;

use super::store::WorkflowRunStore;
use super::tracker::WorkflowRunState;

pub(crate) fn emit_wake_resolved(gateway: &xai_acp_lib::AcpAgentGatewaySender, prompt_id: &str) {
    emit_wake_resolved_with_generation(gateway, prompt_id, None);
}

pub(crate) fn emit_wake_resolved_with_generation(
    gateway: &xai_acp_lib::AcpAgentGatewaySender,
    prompt_id: &str,
    generation: Option<&str>,
) {
    if let Ok(params) = serde_json::value::to_raw_value(
        &serde_json::json!({"prompt_id": prompt_id, "generation": generation}),
    ) {
        gateway.forward_fire_and_forget(agent_client_protocol::ExtNotification::new(
            "x.ai/background_wake_resolved",
            params.into(),
        ));
    }
}

pub(crate) fn emit_workflow_wake_decision(
    gateway: &xai_acp_lib::AcpAgentGatewaySender,
    run_id: &str,
    revision: u64,
    will_wake: bool,
) {
    if let Ok(params) = serde_json::value::to_raw_value(&serde_json::json!({
        "run_id": run_id, "revision": revision, "will_wake": will_wake,
    })) {
        gateway.forward_fire_and_forget(agent_client_protocol::ExtNotification::new(
            "x.ai/workflow_wake_decision",
            params.into(),
        ));
    }
}

#[derive(Clone)]
pub(crate) struct WorkflowNotifySender {
    session_id: agent_client_protocol::SessionId,
    gateway: xai_acp_lib::AcpAgentGatewaySender,
    persistence_tx: tokio::sync::mpsc::UnboundedSender<PersistenceMsg>,
    store: WorkflowRunStore,
}

impl WorkflowNotifySender {
    pub(crate) fn new(
        session_id: agent_client_protocol::SessionId,
        gateway: xai_acp_lib::AcpAgentGatewaySender,
        persistence_tx: tokio::sync::mpsc::UnboundedSender<PersistenceMsg>,
        store: WorkflowRunStore,
    ) -> Self {
        Self {
            session_id,
            gateway,
            persistence_tx,
            store,
        }
    }

    pub(crate) fn wake_decision(&self, run_id: &str, revision: u64, will_wake: bool) {
        emit_workflow_wake_decision(&self.gateway, run_id, revision, will_wake);
    }

    /// Reserve before launch returns, then settle only after the client has
    /// received the workflow start. The bridge itself never waits on the actor.
    pub(crate) fn emit_start(
        &self,
        state: &WorkflowRunState,
        elapsed_ms: u64,
        delivery: Option<xai_grok_tools::notification::BackgroundStartReservation>,
    ) -> Result<(), String> {
        if let Err(error) = self.store.persist(state) {
            tracing::warn!(run_id = %state.run_id, %error, "failed to queue workflow manifest");
        }
        let receipt = self.dispatch(build_workflow_updated(state, elapsed_ms, 0), true);
        if let Some(mut delivery) = delivery {
            delivery.mark_started();
            tokio::spawn(async move {
                let result = match receipt {
                    Some(receipt) => {
                        tokio::time::timeout(std::time::Duration::from_secs(30), receipt)
                            .await
                            .map_err(|_| "workflow lifecycle delivery timed out".to_owned())
                            .and_then(|result| result.map_err(|error| error.to_string()))
                            .and_then(|result| result.map_err(|error| error.to_string()))
                    }
                    None => Err("could not serialize workflow start notification".into()),
                };
                delivery.resolve(result);
            });
        }
        Ok(())
    }

    pub(crate) fn emit(&self, state: &WorkflowRunState, elapsed_ms: u64, active_agents: u32) {
        if let Err(error) = self.store.persist(state) {
            tracing::warn!(run_id = %state.run_id, %error, "failed to queue workflow manifest");
        }
        let _ = self.dispatch(
            build_workflow_updated(state, elapsed_ms, active_agents),
            true,
        );
    }

    pub(crate) fn emit_ephemeral(
        &self,
        state: &WorkflowRunState,
        elapsed_ms: u64,
        active_agents: u32,
    ) {
        if let Err(error) = self.store.persist(state) {
            tracing::warn!(run_id = %state.run_id, %error, "failed to queue workflow manifest");
        }
        let _ = self.dispatch(
            build_workflow_updated(state, elapsed_ms, active_agents),
            false,
        );
    }

    pub(crate) fn broadcast(
        &self,
        state: &WorkflowRunState,
        elapsed_ms: u64,
        active_agents: u32,
        persist_update: bool,
    ) {
        let _ = self.dispatch(
            build_workflow_updated(state, elapsed_ms, active_agents),
            persist_update,
        );
    }

    fn dispatch(
        &self,
        update: XaiSessionUpdate,
        persist: bool,
    ) -> Option<tokio::sync::oneshot::Receiver<xai_acp_lib::AcpResult<()>>> {
        let mut meta = None;
        crate::util::event_id::ensure_event_id_meta(&self.session_id.0, &mut meta);
        let notification = XaiSessionNotification {
            session_id: self.session_id.clone(),
            update,
            meta: meta.map(serde_json::Value::Object),
        };
        let raw = serde_json::to_value(&notification)
            .and_then(|v| serde_json::value::to_raw_value(&v))
            .ok();
        if persist {
            let _ = self.persistence_tx.send(PersistenceMsg::Update(
                crate::session::storage::SessionUpdate::Xai(Box::new(notification)),
            ));
        }
        if let Some(raw) = raw {
            let ext = agent_client_protocol::ExtNotification::new(
                "x.ai/session_notification",
                raw.into(),
            );
            return Some(self.gateway.forward_with_completion(ext));
        }
        None
    }
}

pub(crate) fn build_workflow_updated(
    state: &WorkflowRunState,
    elapsed_ms: u64,
    _active_agents: u32,
) -> XaiSessionUpdate {
    let last = state.history.last();

    let run_complete = state.status == super::tracker::WorkflowRunStatus::Complete;
    let current_idx = state
        .current_phase
        .as_deref()
        .and_then(|cur| state.phases.iter().position(|p| p.title == cur));
    let phases = state
        .phases
        .iter()
        .enumerate()
        .map(|(idx, p)| WorkflowPhaseInfo {
            title: p.title.clone(),
            state: match current_idx {
                Some(cur) if idx < cur => "done",
                Some(cur) if idx == cur && run_complete => "done",
                Some(cur) if idx == cur => "active",
                _ => "pending",
            }
            .to_string(),
        })
        .collect();

    let agents: Vec<WorkflowAgentInfo> = state
        .agents
        .iter()
        .map(|a| WorkflowAgentInfo {
            agent_id: a.agent_id.clone(),
            label: a.label.clone(),
            phase: a.phase.clone(),
            model: a.model.clone(),
            state: a.state.clone(),
            tokens_used: a.tokens_used,
            duration_ms: a.duration_ms,
        })
        .collect();
    let current_agent_label = agents
        .iter()
        .rev()
        .find(|a| a.state == "running")
        .map(|a| a.label.clone());

    let active_agents = agents
        .iter()
        .filter(|agent| agent.state == "running")
        .count() as u32;

    let agents_reserved = 0u64;
    let agents_remaining = state
        .agent_budget
        .map(|total| total.saturating_sub(state.agents_used.saturating_add(agents_reserved)));

    XaiSessionUpdate::WorkflowUpdated {
        run_id: state.run_id.clone(),
        revision: state.revision,
        name: state.name.clone(),
        objective: state.objective.clone(),
        status: state.status.as_str().to_string(),
        foreground: false,
        phases,
        current_phase: state.current_phase.clone(),
        agent_budget: state.agent_budget,
        agents_used: state.agents_used,
        agents_reserved,
        agents_remaining,
        agent_usage_incomplete: state.agent_usage_incomplete,
        elapsed_ms,
        active_agents,
        current_agent_label,
        agents,
        last_event: last.map(|e| e.event.clone()),
        last_event_detail: last.and_then(|e| e.detail.clone()),
        last_event_timestamp: last.map(|e| e.at.clone()),
        pause_message: state.pause_message.clone(),
        result_summary: state.result_summary.clone(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::session::workflow::tracker::WorkflowTracker;

    #[test]
    fn phase_states_derive_from_current() {
        let mut t = WorkflowTracker::default();
        t.start_run(
            "wf_1".into(),
            "demo".into(),
            "obj".into(),
            vec![
                xai_workflow::PhaseMeta {
                    title: "Plan".into(),
                    detail: None,
                },
                xai_workflow::PhaseMeta {
                    title: "Execute".into(),
                    detail: None,
                },
                xai_workflow::PhaseMeta {
                    title: "Verify".into(),
                    detail: None,
                },
            ],
            None,
            None,
        );
        t.set_phase("wf_1", "Execute");
        let state = t.get("wf_1").unwrap();
        match build_workflow_updated(&state, 0, 0) {
            XaiSessionUpdate::WorkflowUpdated { phases, .. } => {
                let states: Vec<(String, String)> =
                    phases.into_iter().map(|p| (p.title, p.state)).collect();
                assert_eq!(
                    states,
                    vec![
                        ("Plan".to_string(), "done".to_string()),
                        ("Execute".to_string(), "active".to_string()),
                        ("Verify".to_string(), "pending".to_string()),
                    ]
                );
            }
            _ => panic!("expected WorkflowUpdated"),
        }
    }

    #[test]
    fn completed_run_marks_current_phase_done() {
        let mut t = WorkflowTracker::default();
        t.start_run(
            "wf_2".into(),
            "demo".into(),
            "obj".into(),
            vec![
                xai_workflow::PhaseMeta {
                    title: "Plan".into(),
                    detail: None,
                },
                xai_workflow::PhaseMeta {
                    title: "Verify".into(),
                    detail: None,
                },
            ],
            None,
            None,
        );
        t.set_phase("wf_2", "Verify");
        t.apply_outcome(
            "wf_2",
            &xai_workflow::WorkflowOutcome::Completed {
                result: serde_json::json!("done"),
            },
        );
        let state = t.get("wf_2").unwrap();
        match build_workflow_updated(&state, 0, 0) {
            XaiSessionUpdate::WorkflowUpdated { phases, status, .. } => {
                assert_eq!(status, "complete");
                let states: Vec<(String, String)> =
                    phases.into_iter().map(|p| (p.title, p.state)).collect();
                assert_eq!(
                    states,
                    vec![
                        ("Plan".to_string(), "done".to_string()),
                        ("Verify".to_string(), "done".to_string()),
                    ]
                );
            }
            _ => panic!("expected WorkflowUpdated"),
        }
    }
}

//! Order cancellation and rewind-point enumeration on the same ACP channel.
use crate::app::actions::TaskResult;
use crate::app::agent::AgentId;
use agent_client_protocol as acp;
use xai_acp_lib::{AcpAgentTx, acp_send};

/// Preserve the prompt identity for no-output rewinds so the shell can reject
/// a cancellation that arrives after a newer prompt has started.
pub(super) fn cancel_meta(
    cancel_subagents: bool,
    trigger: Option<&str>,
    rewind_prompt_id: Option<&str>,
) -> serde_json::Map<String, serde_json::Value> {
    let mut meta = serde_json::Map::new();
    meta.insert("cancelSubagents".into(), cancel_subagents.into());
    if let Some(trigger) = trigger {
        meta.insert("cancelTrigger".into(), trigger.into());
    }
    if let Some(prompt_id) = rewind_prompt_id {
        meta.insert("promptId".into(), prompt_id.into());
        meta.insert("rewindIfNoOutput".into(), true.into());
        meta.insert("rewindIfPristine".into(), true.into());
    }
    meta
}

pub(super) async fn cancel_then_fetch(
    tx: &AcpAgentTx,
    agent_id: AgentId,
    session_id: acp::SessionId,
) -> TaskResult {
    let meta = serde_json::json!({"cancelSubagents": true})
        .as_object()
        .cloned();
    let request = acp::CancelNotification::new(session_id.clone()).meta(meta);
    if let Err(error) = acp_send(request, tx).await {
        return TaskResult::RewindPointsFailed {
            agent_id,
            error: super::sanitize_user_error(&error.to_string()),
        };
    }
    fetch(tx, agent_id, session_id).await
}

pub(super) async fn fetch(
    tx: &AcpAgentTx,
    agent_id: AgentId,
    session_id: acp::SessionId,
) -> TaskResult {
    let request = acp::ExtRequest::new(
        "x.ai/rewind/points",
        serde_json::value::to_raw_value(&serde_json::json!({"sessionId":session_id.0.to_string()}))
            .expect("serialize rewind points")
            .into(),
    );
    match acp_send(request, tx).await {
        Ok(response) => {
            let result = serde_json::from_str::<serde_json::Value>(response.0.get())
                .map_err(|e| format!("invalid response: {e}"))
                .and_then(|wrapper| {
                    if let Some(error) = wrapper.get("error").filter(|v| !v.is_null()) {
                        return Err(error.as_str().unwrap_or("unknown error").to_owned());
                    }
                    let value = wrapper.get("result").cloned().unwrap_or(wrapper);
                    serde_json::from_value::<crate::views::rewind::RewindPointsResponse>(value)
                        .map_err(|e| format!("invalid response: {e}"))
                });
            match result {
                Ok(points) => TaskResult::RewindPointsLoaded {
                    agent_id,
                    points: points.rewind_points,
                },
                Err(error) => TaskResult::RewindPointsFailed { agent_id, error },
            }
        }
        Err(error) => TaskResult::RewindPointsFailed {
            agent_id,
            error: super::sanitize_user_error(&error.to_string()),
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;
    use xai_acp_lib::AcpAgentMessage;

    #[test]
    fn rewind_cancel_wire_binds_prompt_identity() {
        let meta = cancel_meta(true, Some("esc"), Some("p-1"));
        assert_eq!(meta["promptId"], "p-1");
        assert_eq!(meta["rewindIfNoOutput"], true);
        assert_eq!(meta["rewindIfPristine"], true);
        assert_eq!(meta["cancelSubagents"], true);
        assert_eq!(meta["cancelTrigger"], "esc");
        let ordinary = cancel_meta(false, None, None);
        assert!(!ordinary.contains_key("promptId"));
        assert!(!ordinary.contains_key("rewindIfNoOutput"));
    }

    #[tokio::test]
    async fn rewind_points_wait_for_cancel_acknowledgement() {
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel();
        let task = tokio::spawn(async move {
            cancel_then_fetch(&tx, AgentId(0), acp::SessionId::new("sess-1")).await
        });
        let Some(AcpAgentMessage::Cancel(cancel)) = rx.recv().await else {
            panic!("cancel must be first")
        };
        tokio::task::yield_now().await;
        assert!(rx.try_recv().is_err());
        let meta = cancel.request.meta.as_ref().unwrap();
        assert_eq!(meta["cancelSubagents"], true);
        assert!(!meta.contains_key("rewindIfNoOutput"));
        cancel.response_tx.send(Ok(())).unwrap();
        let Some(AcpAgentMessage::ExtMethod(points)) = rx.recv().await else {
            panic!("points must follow cancel")
        };
        assert_eq!(points.request.method.as_ref(), "x.ai/rewind/points");
        let body =
            serde_json::value::RawValue::from_string(r#"{"rewind_points":[]}"#.to_owned()).unwrap();
        points
            .response_tx
            .send(Ok(acp::ExtResponse::new(Arc::from(body))))
            .unwrap();
        assert!(
            matches!(task.await.unwrap(), TaskResult::RewindPointsLoaded { points, .. } if points.is_empty())
        );
    }

    #[tokio::test]
    async fn rewind_does_not_fetch_after_cancel_fails() {
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel();
        let task = tokio::spawn(async move {
            cancel_then_fetch(&tx, AgentId(0), acp::SessionId::new("sess-1")).await
        });
        let Some(AcpAgentMessage::Cancel(cancel)) = rx.recv().await else {
            panic!("expected cancel")
        };
        drop(cancel.response_tx);
        assert!(matches!(
            task.await.unwrap(),
            TaskResult::RewindPointsFailed { .. }
        ));
        assert!(rx.try_recv().is_err());
    }
}

//! Typed ACP transport for post-execution sandbox decisions.
use agent_client_protocol as acp;
use serde_json::{Value, json};
use xai_acp_lib::AcpAgentGatewaySender;
use xai_grok_workspace::permission::PermissionHookTransport;

pub(crate) struct AcpSandboxPermissionTransport {
    pub(crate) gateway: AcpAgentGatewaySender,
    pub(crate) session_id: acp::SessionId,
    pub(crate) pending: crate::session::pending_interaction::PendingInteractions,
}

fn hold_card_id(hold_id: &str) -> String {
    format!("sandbox-hold-{hold_id}")
}

fn offered(card: &Value) -> Vec<&str> {
    if card.get("schema_version").and_then(Value::as_u64) != Some(1) {
        return Vec::new();
    }
    card.get("offered_scopes")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(Value::as_str)
        .filter(|scope| matches!(*scope, "call" | "session" | "workspace" | "global"))
        .collect()
}

fn validated_reply(card: &Value, response: acp::RequestPermissionResponse) -> Value {
    let denied = || json!({"outcome":"reject", "reason":"decode_failed"});
    let acp::RequestPermissionOutcome::Selected(selected) = response.outcome else {
        return json!({"outcome":"cancelled"});
    };
    let Some(decision) = response
        .meta
        .as_ref()
        .and_then(|m| m.get("sandbox_decision"))
    else {
        return denied();
    };
    let id = selected.option_id.0.as_ref();
    let outcome = decision.get("outcome").and_then(Value::as_str);
    if id == "sandbox:deny" && outcome == Some("reject") {
        return decision.clone();
    }
    let Some(duration) = decision.pointer("/duration/kind").and_then(Value::as_str) else {
        return denied();
    };
    let subject = decision.pointer("/scope/kind").and_then(Value::as_str);
    let subject_valid = match subject {
        Some("fs_write_root" | "fs_read" | "net_host") => decision
            .pointer("/scope/value")
            .and_then(Value::as_str)
            .is_some_and(|v| !v.trim().is_empty()),
        Some("net_all" | "build_caches") => true,
        _ => false,
    };
    if outcome == Some("approve")
        && subject_valid
        && offered(card).contains(&duration)
        && id.strip_prefix("sandbox:") == Some(duration)
    {
        decision.clone()
    } else {
        denied()
    }
}

#[async_trait::async_trait]
impl PermissionHookTransport for AcpSandboxPermissionTransport {
    async fn request_permission(&self, payload: Value) -> Result<Value, String> {
        let card = payload
            .get("sandbox_violation")
            .cloned()
            .ok_or("missing sandbox card")?;
        let _parent_call_id = payload
            .get("tool_call_id")
            .and_then(Value::as_str)
            .ok_or("missing tool call id")?;
        let call_id = card
            .get("hold_id")
            .and_then(Value::as_str)
            .map(hold_card_id)
            .unwrap_or_else(|| format!("sandbox-card-{}", uuid::Uuid::new_v4()));
        let mut options = vec![acp::PermissionOption::new(
            "sandbox:deny",
            "Deny".to_owned(),
            acp::PermissionOptionKind::RejectOnce,
        )];
        for scope in offered(&card) {
            options.push(acp::PermissionOption::new(
                format!("sandbox:{scope}"),
                format!("Allow for {scope}"),
                acp::PermissionOptionKind::AllowOnce,
            ));
        }
        let update = acp::ToolCallUpdate::new(
            acp::ToolCallId::new(call_id.clone()),
            acp::ToolCallUpdateFields::new().status(Some(acp::ToolCallStatus::InProgress)),
        );
        let mut meta = serde_json::Map::new();
        meta.insert("sandbox_violation".to_owned(), card.clone());
        let request = acp::RequestPermissionRequest::new(self.session_id.clone(), update, options)
            .meta(Some(meta));
        let _pending = crate::session::pending_interaction::PendingInteractionGuard::new(
            self.pending.clone(),
            self.gateway.clone(),
            self.session_id.clone(),
            call_id,
            crate::session::pending_interaction::PendingKind::Permission,
        );
        let response = self
            .gateway
            .send(request)
            .await
            .map_err(|e| e.to_string())?;
        Ok(validated_reply(&card, response))
    }

    async fn withdraw_permission(&self, hold_id: &str) {
        let notification = crate::extensions::notification::SessionNotification {
            session_id: self.session_id.clone(),
            update: crate::extensions::notification::SessionUpdate::InteractionResolved {
                tool_call_id: hold_card_id(hold_id),
            },
            meta: None,
        };
        if let Ok(params) = serde_json::value::to_raw_value(&notification) {
            self.gateway
                .forward_fire_and_forget(acp::ExtNotification::new(
                    "x.ai/session_notification",
                    params.into(),
                ));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn response(option: &str, decision: Option<Value>) -> acp::RequestPermissionResponse {
        let outcome = acp::RequestPermissionOutcome::Selected(acp::SelectedPermissionOutcome::new(
            acp::PermissionOptionId::new(option.to_owned()),
        ));
        let mut meta = serde_json::Map::new();
        if let Some(d) = decision {
            meta.insert("sandbox_decision".into(), d);
        }
        acp::RequestPermissionResponse::new(outcome).meta(Some(meta))
    }
    #[test]
    fn typed_decision_must_match_offered_option_and_subject() {
        let card = json!({"schema_version":1,"offered_scopes":["call","session"]});
        let decision = json!({"outcome":"approve","scope":{"kind":"fs_write_root","value":"/tmp/x"},"duration":{"kind":"session"}});
        assert_eq!(
            validated_reply(&card, response("sandbox:session", Some(decision.clone()))),
            decision
        );
        for reply in [
            response("sandbox:call", Some(decision.clone())),
            response("sandbox:session", None),
            response("sandbox:global", Some(decision.clone())),
        ] {
            assert_eq!(validated_reply(&card, reply)["outcome"], "reject");
        }
        assert_eq!(
            validated_reply(
                &json!({"schema_version":99,"offered_scopes":["session"]}),
                response("sandbox:session", Some(decision))
            )["outcome"],
            "reject"
        );
    }

    #[tokio::test]
    async fn gateway_round_trip_requires_typed_decision_and_drops_cancelled_waiter() {
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel();
        let transport = std::sync::Arc::new(AcpSandboxPermissionTransport {
            gateway: AcpAgentGatewaySender::new(tx),
            session_id: acp::SessionId::new("test-session"),
            pending: Default::default(),
        });
        let card = json!({"schema_version":1,"offered_scopes":["call"],"hold_id":"hold-1"});
        let payload = json!({"tool_call_id":"tool-1","sandbox_violation":card});
        let worker = {
            let transport = transport.clone();
            let payload = payload.clone();
            tokio::spawn(async move { transport.request_permission(payload).await })
        };
        let args = loop {
            if let xai_acp_lib::AcpClientMessage::RequestPermission(args) = rx.recv().await.unwrap()
            {
                break args;
            }
        };
        assert_eq!(
            args.request.meta.as_ref().unwrap()["sandbox_violation"],
            card
        );
        assert_eq!(args.request.options.len(), 2);
        assert_eq!(
            args.request.tool_call.tool_call_id.0.as_ref(),
            "sandbox-hold-hold-1"
        );
        let decision = json!({"outcome":"approve","scope":{"kind":"fs_write_root","value":"/tmp/x"},"duration":{"kind":"call"}});
        args.response_tx
            .send(Ok(response("sandbox:call", Some(decision.clone()))))
            .unwrap();
        assert_eq!(worker.await.unwrap().unwrap(), decision);

        let worker = tokio::spawn(async move { transport.request_permission(payload).await });
        let args = loop {
            if let xai_acp_lib::AcpClientMessage::RequestPermission(args) = rx.recv().await.unwrap()
            {
                break args;
            }
        };
        worker.abort();
        let _ = worker.await;
        assert!(
            args.response_tx
                .send(Ok(response("sandbox:call", Some(decision))))
                .is_err()
        );
    }
}

//! Receipt ownership across the real post-hook and conversation bridge.
use super::super::support::{
    create_test_actor, install_pre_tool_use_hooks, post_tool_use_spec, test_agent_with_tools,
    tool_result_text,
};
use super::*;
use xai_grok_tools::implementations::grok_build::task::types::{
    AgentMailboxMessage, AgentMailboxMessageKind, MailboxReceipt, WaitAgentMessagesOutput,
};

fn delivery() -> DrainedToolSuccess {
    let messages = ["first", "second"]
        .into_iter()
        .enumerate()
        .map(|(i, body)| AgentMailboxMessage {
            message_id: format!("message-{i}"),
            team_scope_id: "team".into(),
            from_agent_id: "sender".into(),
            to_agent_id: "recipient".into(),
            kind: AgentMailboxMessageKind::FollowupTask,
            body: body.into(),
            created_at_ms: i as u64,
            native: None,
        })
        .collect();
    let output = ToolsToolOutput::WaitAgentMessages(WaitAgentMessagesOutput {
        messages,
        timed_out: false,
    });
    let prompt_text = output.to_prompt_format();
    DrainedToolSuccess::new(ToolRunResult {
        output,
        prompt_text,
        effective_tool_name: None,
    })
}

fn prepared() -> PreparedToolCall {
    PreparedToolCall {
        mcp_binding: None,
        mcp_request_meta: None,
        call_id: "mail".into(),
        tool_call_id: acp::ToolCallId::new("mail"),
        tool_name: "wait_agent".into(),
        raw_arguments: "{}".into(),
        mcp_file: None,
        parsed_args: json!({}),
        model_id: None,
        invocation_id: "mail".into(),
        tool_id: "wait_agent".into(),
        tool_version: None,
        concatenated_json_count: 0,
        coercion_note: None,
        dispatch_target_name: None,
        is_read_only: true,
        is_edit: false,
        plan_file: None,
        rewriting_hook: None,
        additional_context: Vec::new(),
    }
}

fn receipt() -> (MailboxReceipt, std::sync::mpsc::Receiver<bool>) {
    let (tx, rx) = std::sync::mpsc::channel();
    (
        MailboxReceipt::new(move |committed| {
            let _ = tx.send(committed);
        }),
        rx,
    )
}

async fn actor() -> SessionActor {
    let (gateway, _) = tokio::sync::mpsc::unbounded_channel();
    let (persistence, _) = tokio::sync::mpsc::unbounded_channel();
    create_test_actor(0, 256_000, 85, gateway, persistence).await
}

async fn bridge(
    actor: &SessionActor,
    drained: DrainedToolSuccess,
    receipt: MailboxReceipt,
    replacement: Option<String>,
) {
    actor
        .handle_bridge_tool_success_with_mailbox_receipt(
            BridgeToolSuccess {
                tool_call_id: &acp::ToolCallId::new("mail"),
                call_id: "mail",
                requested_tool_name: "wait_agent",
                effective_tool_name: "wait_agent",
                drained,
                concatenated_json_count: 0,
                coercion_note: None,
                model_id: "test",
                tool_parsed_args: &json!({}),
                model_output_override: replacement,
            },
            Some(receipt),
        )
        .await
        .unwrap();
}

#[tokio::test(flavor = "current_thread")]
async fn mailbox_delivery_commits_only_after_complete_result_is_appended() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let actor = actor().await;
            let (receipt, result) = receipt();
            bridge(&actor, delivery(), receipt, None).await;
            let text = tool_result_text(&actor, "mail").await;
            assert!(text.contains("first") && text.contains("second"));
            assert_eq!(result.try_recv(), Ok(true));
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn mailbox_hook_replacement_rolls_back_without_exposing_partial_batch() {
    tokio::task::LocalSet::new().run_until(async {
        let mut actor = actor().await;
        install_pre_tool_use_hooks(&mut actor, vec![post_tool_use_spec("redact", None,
            r#"echo '{"hookSpecificOutput":{"updatedToolOutput":{"type":"WaitAgentMessages","messages":[],"timed_out":false}}}'"#)]);
        let drained = delivery();
        let (mut hook, _) = actor.dispatch_post_tool_use_hook(&prepared(), drained.output(), None).await;
        let replacement = hook.model_output.take().expect("hook replaces the output");
        let (receipt, result) = receipt();
        bridge(&actor, drained, receipt, Some(replacement)).await;
        let text = tool_result_text(&actor, "mail").await;
        assert!(text.contains("call wait_agent again"));
        assert!(!text.contains("first") && !text.contains("second"));
        assert_eq!(result.try_recv(), Ok(false));
    }).await;
}

#[tokio::test(flavor = "current_thread")]
async fn mailbox_bridge_cancellation_after_poll_rolls_back_before_append() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let actor = actor().await;
            let held = actor.mcp_state.lock().await;
            let (receipt, result) = receipt();
            let mut pending = Box::pin(bridge(&actor, delivery(), receipt, None));
            assert!(futures::poll!(pending.as_mut()).is_pending());
            assert!(result.try_recv().is_err());
            drop(pending);
            drop(held);
            assert_eq!(result.try_recv(), Ok(false));
            assert!(actor.chat_state_handle.get_conversation().await.is_empty());
        })
        .await;
}

// No child runs are needed: this coordinator delivers a peer's passive mail
// to the root mailbox, then the normal registered tool drains it.
struct MailboxControl;
impl xai_grok_tools::implementations::grok_build::task::coordinator::ChildControl
    for MailboxControl
{
    type ProgressFuture = std::future::Ready<
        xai_grok_tools::implementations::grok_build::task::coordinator::SubagentProgress,
    >;
    fn progress(&self) -> Self::ProgressFuture {
        std::future::ready(Default::default())
    }
    fn cancel(&self) {}
}

struct MailboxRunner;
impl xai_grok_tools::implementations::grok_build::task::coordinator::ChildRunner for MailboxRunner {
    type Control = MailboxControl;
    type CompletionData = ();
    type RunFuture = std::future::Pending<
        xai_grok_tools::implementations::grok_build::task::coordinator::ChildRunOutput<()>,
    >;
    type ValidateFuture = std::future::Ready<
        xai_grok_tools::implementations::grok_build::task::types::SubagentValidateTypeOutcome,
    >;
    type DescribeFuture = std::future::Ready<
        xai_grok_tools::implementations::grok_build::task::types::SubagentDescribeOutcome,
    >;
    fn run(
        &self,
        _: xai_grok_tools::implementations::grok_build::task::coordinator::ChildRunRequest<
            Self::Control,
        >,
    ) -> Self::RunFuture {
        std::future::pending()
    }
    fn validate_type(&self, _: String, _: String) -> Self::ValidateFuture {
        std::future::ready(xai_grok_tools::implementations::grok_build::task::types::SubagentValidateTypeOutcome::Ok)
    }
    fn describe_type(&self, _: String, _: Option<String>, _: String) -> Self::DescribeFuture {
        std::future::ready(xai_grok_tools::implementations::grok_build::task::types::SubagentDescribeOutcome::Unavailable)
    }
    fn on_completed(
        &self,
        _: xai_grok_tools::implementations::grok_build::task::coordinator::ChildCompletion<()>,
    ) {
    }
}

fn wait_call(id: &str) -> crate::sampling::types::ToolCallResponse {
    crate::sampling::types::ToolCallResponse {
        id: id.to_owned(),
        kind: "function".to_owned(),
        function: crate::sampling::types::ToolCallFunction::new(
            "wait_agent",
            r#"{"timeout_ms":0}"#,
        ),
    }
}

#[tokio::test(flavor = "current_thread")]
async fn mailbox_coordinator_registry_drainer_hook_and_chat_commit() {
    use xai_grok_tools::implementations::grok_build::{
        agent_collaboration::WaitAgentTool,
        task::{
            backend::{ChannelBackend, SubagentBackend, SubagentBackendResource},
            coordinator::{CoordinatorConfig, SubagentCoordinator},
            types::AgentMailboxIdentity,
        },
    };
    use xai_grok_tools::registry::types::ToolConfig;

    tokio::task::LocalSet::new().run_until(async {
        let mut actor = actor().await;
        *actor.agent.borrow_mut() = test_agent_with_tools(vec![ToolConfig::for_tool::<WaitAgentTool>()]).await;
        let (sender, receiver) = SubagentCoordinator::<MailboxRunner>::channel();
        let coordinator = tokio::spawn(SubagentCoordinator::from_channel(receiver, MailboxRunner, CoordinatorConfig::default()).run());
        let backend = ChannelBackend::for_coordinator_session(sender, "test-actor");
        let root = AgentMailboxIdentity { team_scope_id: "test-actor".into(), agent_id: "test-actor".into() };
        let peer = AgentMailboxIdentity { team_scope_id: "test-actor".into(), agent_id: "peer".into() };
        let tool_bridge = actor.agent.borrow().tool_bridge().clone();
        tool_bridge.update_resource(SubagentBackendResource(Arc::new(backend.clone()))).await;
        tool_bridge.update_resource(root.clone()).await;
        actor.workspace_ops.bind_local_session("test-actor", actor.tool_context.cwd.as_path().to_path_buf(),
            xai_hunk_tracker::HunkTrackerHandle::noop(), tool_bridge.toolset(), None).unwrap();
        for (index, body) in ["first", "second"].into_iter().enumerate() {
            backend.send_agent_message(peer.clone(), "root", AgentMailboxMessage {
                message_id: format!("f{index}"), team_scope_id: "test-actor".into(),
                from_agent_id: "peer".into(), to_agent_id: "test-actor".into(),
                kind: AgentMailboxMessageKind::FollowupTask, body: body.into(), created_at_ms: index as u64, native: None,
            }).await.unwrap();
        }
        install_pre_tool_use_hooks(&mut actor, vec![post_tool_use_spec("redact", None,
            r#"echo '{"hookSpecificOutput":{"updatedToolOutput":{"type":"WaitAgentMessages","messages":[],"timed_out":false}}}'"#)]);
        actor.execute_tool_calls(vec![wait_call("redacted")], None).await.unwrap();
        let redacted = tool_result_text(&actor, "redacted").await;
        assert!(redacted.contains("call wait_agent again"));
        assert!(!redacted.contains("first") && !redacted.contains("second"));
        let retry = backend.wait_agent_messages(root.clone(), 0).await;
        assert_eq!(retry.messages.iter().map(|m| m.body.as_str()).collect::<Vec<_>>(), ["first", "second"]);
        drop(retry);
        backend.list_agents(root.clone()).await;
        actor.hook_registry.borrow_mut().take();
        actor.execute_tool_calls(vec![wait_call("committed")], None).await.unwrap();
        let committed = tool_result_text(&actor, "committed").await;
        assert!(committed.contains("first") && committed.contains("second"));
        backend.list_agents(root.clone()).await;
        assert!(backend.wait_agent_messages(root, 0).await.messages.is_empty());
        coordinator.abort();
    }).await;
}

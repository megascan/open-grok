use super::support::*;
use super::*;

const AGENT_SWARM_EXCLUSIVITY_ERROR: &str = "`agent_swarm` must be the only tool call in its batch. Inspect briefly, then make one exclusive agent_swarm call for independent work; use ordinary task calls for heterogeneous small work.";

/// Direct nested dispatch bypasses AgentTask, which normally owns this scope.
fn install_test_delivery_scope(
    actor: &mut SessionActor,
) -> (
    xai_grok_tools::notification::BackgroundDeliveryGuard,
    tokio::sync::mpsc::UnboundedReceiver<xai_grok_tools::notification::BridgeToolNotification>,
) {
    let (handle, receiver) = xai_grok_tools::notification::ToolNotificationHandle::bridge_channel();
    let guard = handle.begin_turn_delivery();
    Arc::get_mut(&mut actor.rebuild_spec)
        .expect("test rebuild spec is uniquely owned")
        .tools_notification_handle = handle;
    (guard, receiver)
}

#[derive(Default)]
struct RecordingPermissionClient {
    prompts: std::rc::Rc<std::cell::RefCell<Vec<acp::RequestPermissionRequest>>>,
}

#[async_trait::async_trait(?Send)]
impl acp::Client for RecordingPermissionClient {
    async fn request_permission(
        &self,
        args: acp::RequestPermissionRequest,
    ) -> acp::Result<acp::RequestPermissionResponse> {
        let selected = args
            .options
            .iter()
            .find(|option| option.kind == acp::PermissionOptionKind::RejectOnce)
            .cloned()
            .unwrap_or_else(|| {
                args.options
                    .first()
                    .cloned()
                    .expect("permission prompt must expose at least one option")
            });
        self.prompts.borrow_mut().push(args);
        Ok(acp::RequestPermissionResponse::new(
            acp::RequestPermissionOutcome::Selected(acp::SelectedPermissionOutcome::new(
                selected.option_id,
            )),
        ))
    }

    async fn session_notification(&self, _: acp::SessionNotification) -> acp::Result<()> {
        Ok(())
    }
}

fn install_recording_permissions(
    actor: &mut SessionActor,
) -> std::rc::Rc<std::cell::RefCell<Vec<acp::RequestPermissionRequest>>> {
    let prompts = std::rc::Rc::new(std::cell::RefCell::new(Vec::new()));
    let (gateway, receiver) =
        xai_acp_lib::acp_gateway::<acp::AgentSide, _>(RecordingPermissionClient {
            prompts: prompts.clone(),
        });
    tokio::task::spawn_local(receiver.run());

    let cwd =
        xai_grok_paths::AbsPathBuf::new(std::path::PathBuf::from(actor.session_info.cwd.clone()))
            .unwrap_or_else(|_| {
                xai_grok_paths::AbsPathBuf::new(std::path::PathBuf::from("/tmp"))
                    .expect("fallback /tmp")
            });
    let (handle, _events) = xai_grok_workspace::permission::spawn_permission_manager(
        actor.session_info.id.clone(),
        gateway,
        cwd,
        xai_grok_workspace::permission::ClientType::Generic,
        Some(xai_grok_workspace::permission::types::PermissionConfig::new(vec![])),
        vec![],
        vec![],
        false,
        None,
    );
    actor.permissions = handle;
    prompts
}

fn tool_result_for_call(conversation: &[ConversationItem], call_id: &str) -> Option<String> {
    conversation.iter().find_map(|item| match item {
        xai_grok_sampling_types::ConversationItem::ToolResult(tr) if tr.tool_call_id == call_id => {
            Some(tr.content.to_string())
        }
        _ => None,
    })
}

fn batch_call(id: &str, name: &str, arguments: &str) -> ToolCallResponse {
    ToolCallResponse {
        id: id.to_string(),
        kind: "function".to_string(),
        function: crate::sampling::types::ToolCallFunction::new(name, arguments),
    }
}

fn install_client_hook(
    actor: &SessionActor,
    event: xai_grok_hooks::event::HookEventName,
    callback_ids: &[&str],
) {
    let mut client_hooks = crate::extensions::hooks::ClientHooks::new();
    client_hooks.insert(
        event,
        vec![crate::extensions::hooks::ClientHookGroup {
            matcher: None,
            callback_ids: callback_ids.iter().map(|s| s.to_string()).collect(),
            timeout: None,
        }],
    );
    *actor.client_hooks.borrow_mut() = client_hooks;
}

async fn test_actor() -> (
    SessionActor,
    tokio::sync::mpsc::UnboundedReceiver<xai_acp_lib::AcpClientMessage>,
    tokio::sync::mpsc::UnboundedReceiver<PersistenceMsg>,
) {
    let (gateway_tx, gateway_rx) =
        tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpClientMessage>();
    let (persistence_tx, persistence_rx) = tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
    let actor = create_test_actor(0, 256_000, 85, gateway_tx, persistence_tx).await;
    (actor, gateway_rx, persistence_rx)
}

fn post_tool_use_envelope(actor: &SessionActor) -> xai_grok_hooks::event::HookEventEnvelope {
    actor.make_hook_envelope(
        xai_grok_hooks::event::HookEventName::PostToolUse,
        None,
        xai_grok_hooks::event::HookPayload::PostToolUse {
            tool_name: "run_terminal_command".to_string(),
            tool_use_id: "call_1".to_string(),
            tool_input: serde_json::json!({}),
            tool_result: serde_json::json!({}),
            tool_input_truncated: false,
            tool_result_truncated: false,
            duration_ms: None,
            is_backgrounded: false,
            subagent_type: None,
        },
    )
}

fn spawn_run_responder(
    mut gateway_rx: tokio::sync::mpsc::UnboundedReceiver<xai_acp_lib::AcpClientMessage>,
    reply: impl Fn(&serde_json::Value) -> serde_json::Value + 'static,
) {
    tokio::task::spawn_local(async move {
        while let Some(msg) = gateway_rx.recv().await {
            match msg {
                xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                    let params: serde_json::Value =
                        serde_json::from_str(args.request.params.get()).unwrap();
                    let body: Arc<serde_json::value::RawValue> =
                        serde_json::value::to_raw_value(&reply(&params))
                            .unwrap()
                            .into();
                    let _ = args.response_tx.send(Ok(acp::ExtResponse::new(body)));
                }
                xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                    let _ = args.response_tx.send(Ok(()));
                }
                _ => {}
            }
        }
    });
}

fn spawn_deny_responder(
    gateway_rx: tokio::sync::mpsc::UnboundedReceiver<xai_acp_lib::AcpClientMessage>,
    reason: &'static str,
) {
    spawn_run_responder(
        gateway_rx,
        move |_| serde_json::json!({ "decision": "deny", "systemMessage": reason }),
    );
}

#[tokio::test(flavor = "current_thread")]
async fn pre_tool_use_force_stop_cancels_prepared_batch() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PreToolUse,
                &["stop-second"],
            );
            spawn_run_responder(gateway_rx, |params| {
                if params["toolUseId"] == "second" {
                    serde_json::json!({"continue": false, "stopReason": "batch stopped"})
                } else {
                    serde_json::json!({})
                }
            });
            let args = r#"{"todos":[{"id":"t1","content":"do","status":"completed"}]}"#;
            let calls = vec![
                batch_call("first", "todo_write", args),
                batch_call("second", "todo_write", args),
                batch_call("third", "todo_write", args),
            ];
            let result = actor.execute_tool_calls(calls, None).await.unwrap();
            assert!(
                matches!(result, ToolLoop::HookStop(ref stop) if stop.reason == "batch stopped")
            );
            let conversation = actor.chat_state_handle.get_conversation().await;
            let first =
                tool_result_for_call(&conversation, "first").expect("prepared call has a result");
            assert!(first.contains("stopped by an earlier hook"), "{first}");
            assert!(
                tool_result_for_call(&conversation, "second")
                    .unwrap()
                    .contains("batch stopped")
            );
            assert!(
                tool_result_for_call(&conversation, "third")
                    .unwrap()
                    .contains("cancelled")
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn post_tool_use_force_stop_ends_after_recording_tool_result() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
            actor
                .agent
                .borrow()
                .tool_bridge()
                .clone()
                .register_mcp_tools(
                    "mock_replacement_tool".into(),
                    McpReplacementResultTool,
                    Some(serde_json::json!({"type":"object"})),
                )
                .await
                .unwrap();
            actor
                .workspace_ops
                .bind_local_session(
                    &actor.session_id_string(),
                    actor.tool_context.cwd.as_path().to_path_buf(),
                    actor.tool_context.hunk_tracker_handle.clone(),
                    actor.agent.borrow().tool_bridge().toolset(),
                    None,
                )
                .unwrap();
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PostToolUse,
                &["stop-after"],
            );
            spawn_run_responder(
                gateway_rx,
                |_| serde_json::json!({"continue":false,"stopReason":"post stopped"}),
            );
            let result = actor
                .execute_tool_calls(
                    vec![batch_call("post", "mock_replacement_tool", "{}")],
                    None,
                )
                .await
                .unwrap();
            assert!(
                matches!(result, ToolLoop::HookStop(ref stop) if stop.reason == "post stopped")
            );
            let conversation = actor.chat_state_handle.get_conversation().await;
            assert!(
                tool_result_for_call(&conversation, "post")
                    .unwrap()
                    .contains("private-original-output")
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn post_tool_stop_latches_before_slow_client_callback_finishes() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;
            *actor.current_prompt_id.lock().unwrap() = Some("prompt-a".into());
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PostToolUse,
                &["slow", "stop"],
            );
            tokio::task::spawn_local(async move {
                let mut held = Vec::new();
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            let params: serde_json::Value =
                                serde_json::from_str(args.request.params.get()).unwrap();
                            if params["hookCallbackId"] == "stop" {
                                let response: Arc<serde_json::value::RawValue> =
                                    serde_json::value::to_raw_value(
                                        &serde_json::json!({"continue":false,"stopReason":"halt"}),
                                    )
                                    .unwrap()
                                    .into();
                                let _ = args.response_tx.send(Ok(acp::ExtResponse::new(response)));
                            } else {
                                held.push(args.response_tx);
                            }
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });
            let actor = std::rc::Rc::new(actor);
            let pending = {
                let actor = actor.clone();
                tokio::task::spawn_local(async move {
                    actor
                        .run_post_tool_use_client_hooks(&post_tool_use_envelope(&actor))
                        .await
                })
            };
            tokio::time::timeout(std::time::Duration::from_secs(5), async {
                while actor.current_hook_stop().is_none() {
                    tokio::task::yield_now().await;
                }
            })
            .await
            .expect("stop must latch while slow callback remains pending");
            assert_eq!(actor.current_hook_stop().unwrap().reason, "halt");
            let outcome = tokio::time::timeout(std::time::Duration::from_secs(5), pending)
                .await
                .expect("stop must not wait for the slow callback")
                .unwrap();
            assert_eq!(outcome.force_stop.unwrap().reason, "halt");
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn nested_hook_force_stop_cancels_cell_and_is_scoped_to_exact_turn() {
    use xai_grok_code_mode_protocol::{
        CellId, CodeModeNestedToolCall, CodeModeToolKind, ToolName, nested_tool_progress_channel,
    };
    for event in [
        xai_grok_hooks::event::HookEventName::PreToolUse,
        xai_grok_hooks::event::HookEventName::PostToolUse,
    ] {
        tokio::task::LocalSet::new()
            .run_until(async {
                let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;
                let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
                *actor.current_prompt_id.lock().unwrap() = Some("prompt-a".into());
                *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
                actor
                    .agent
                    .borrow()
                    .tool_bridge()
                    .clone()
                    .register_mcp_tools(
                        "mock_replacement_tool".into(),
                        McpReplacementResultTool,
                        Some(serde_json::json!({"type":"object"})),
                    )
                    .await
                    .unwrap();
                actor
                    .workspace_ops
                    .bind_local_session(
                        &actor.session_id_string(),
                        actor.tool_context.cwd.as_path().to_path_buf(),
                        actor.tool_context.hunk_tracker_handle.clone(),
                        actor.agent.borrow().tool_bridge().toolset(),
                        None,
                    )
                    .unwrap();
                install_client_hook(&actor, event, &["force-stop"]);
                let seen = Arc::new(std::sync::atomic::AtomicUsize::new(0));
                let observed = seen.clone();
                spawn_run_responder(gateway_rx, move |_| {
                    observed.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
                    serde_json::json!({"continue": false, "stopReason": "nested stopped"})
                });
                let invocation = || CodeModeNestedToolCall {
                    cell_id: CellId::new("test-cell".into()),
                    runtime_tool_call_id: "nested".into(),
                    tool_name: ToolName::plain("mock_replacement_tool"),
                    tool_kind: CodeModeToolKind::Function,
                    input: Some(serde_json::json!({})),
                };
                let token = tokio_util::sync::CancellationToken::new();
                let (progress, _) = nested_tool_progress_channel();
                let _ = actor
                    .dispatch_code_mode_nested_tool(invocation(), token.clone(), progress)
                    .await;
                assert!(
                    token.is_cancelled(),
                    "the cell must stop even if JavaScript catches the tool error"
                );
                assert_eq!(actor.current_hook_stop().unwrap().reason, "nested stopped");
                assert!(actor.current_hook_stop_token().unwrap().is_cancelled());
                let (progress, _) = nested_tool_progress_channel();
                let later = actor
                    .dispatch_code_mode_nested_tool(
                        invocation(),
                        tokio_util::sync::CancellationToken::new(),
                        progress,
                    )
                    .await;
                assert!(later.is_err());
                assert_eq!(seen.load(std::sync::atomic::Ordering::SeqCst), 1);
                let old_identity = actor.hook_stop_identity().unwrap();
                actor.turn_report.start_next_turn();
                assert!(actor.current_hook_stop().is_none());
                assert!(!actor.current_hook_stop_token().unwrap().is_cancelled());
                assert!(!actor.record_hook_stop_for(
                    &old_identity,
                    xai_grok_hooks::dispatcher::StopBlock {
                        hook_name: "old".into(),
                        reason: "late".into()
                    }
                ));
                *actor.current_prompt_id.lock().unwrap() = Some("prompt-b".into());
                assert!(actor.current_hook_stop().is_none());
            })
            .await;
    }
}

#[tokio::test(flavor = "current_thread")]
async fn session_tool_restrictions_block_direct_and_nested_calls() {
    use xai_grok_code_mode_protocol::{
        CellId, CodeModeNestedToolCall, CodeModeToolKind, ToolName, nested_tool_progress_channel,
    };
    tokio::task::LocalSet::new()
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            *actor.current_prompt_id.lock().unwrap() = Some("restricted-turn".into());
            let dir = tempfile::tempdir().unwrap();
            let mut definition = xai_grok_agent::AgentDefinition::default_grok_build();
            crate::agent::config::CliAgentOverrides {
                tools: Some(vec!["read_file".into()]),
                disallowed_tools: Some(vec!["run_terminal_cmd".into()]),
                ..Default::default()
            }
            .apply_to_definition(&mut definition);
            *actor.agent.borrow_mut() = xai_grok_agent::AgentBuilder::new(
                dir.path().to_owned(),
                Arc::new(xai_grok_tools::computer::local::LocalTerminalBackend::new()),
                xai_grok_tools::notification::ToolNotificationHandle::noop(),
            )
            .from_definition(definition)
            .build()
            .await
            .unwrap();
            spawn_run_responder(gateway_rx, |_| serde_json::json!({}));
            let (progress, _) = nested_tool_progress_channel();
            let nested = actor
                .dispatch_code_mode_nested_tool(
                    CodeModeNestedToolCall {
                        cell_id: CellId::new("restricted".into()),
                        runtime_tool_call_id: "nested-denied".into(),
                        tool_name: ToolName::plain("run_terminal_cmd"),
                        tool_kind: CodeModeToolKind::Function,
                        input: Some(serde_json::json!({"command":"echo forbidden"})),
                    },
                    tokio_util::sync::CancellationToken::new(),
                    progress,
                )
                .await;
            assert!(nested.is_err(), "nested denied tool must not run");
            let result = actor
                .execute_tool_calls(
                    vec![batch_call(
                        "direct-denied",
                        "run_terminal_cmd",
                        r#"{"command":"echo forbidden"}"#,
                    )],
                    None,
                )
                .await
                .unwrap();
            assert!(matches!(result, ToolLoop::Continue));
            let conversation = actor.chat_state_handle.get_conversation().await;
            let result =
                tool_result_for_call(&conversation, "direct-denied").expect("denied call result");
            assert!(
                !result.contains("forbidden\n"),
                "denied tool must not execute: {result}"
            );
            assert!(
                actor
                    .agent
                    .borrow()
                    .tool_bridge()
                    .tool_kind("run_terminal_cmd")
                    .is_none()
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn client_hooks_fire_without_file_registry() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;

            assert!(
                actor.hook_registry.borrow().is_none(),
                "fixture must have no file registry for this invariant"
            );
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_0"],
            );

            actor.fire_hook(
                xai_grok_hooks::event::HookEventName::Stop,
                None,
                xai_grok_hooks::event::HookPayload::Stop {
                    reason: "end_turn".to_string(),
                    stop_hook_active: false,
                    last_assistant_message: None,
                    background_tasks: None,
                    session_crons: None,
                },
            );

            let msg = gateway_rx
                .try_recv()
                .expect("client hook must fire with no file registry");
            let xai_acp_lib::AcpClientMessage::ExtNotification(args) = msg else {
                panic!("expected an x.ai/hooks/event ext notification");
            };
            assert_eq!(args.request.method.as_ref(), "x.ai/hooks/event");
            let params: serde_json::Value =
                serde_json::from_str(args.request.params.get()).unwrap();
            assert_eq!(params["hookCallbackId"], "cb_0");
            assert_eq!(params["hookEventName"], "stop");
        })
        .await;
}

/// A `use_tool` call whose wire `function.name` is the dispatcher surfaces to PreToolUse
/// hooks as its resolved target, so a matcher keyed on the qualified MCP name
/// (`linear__save_issue`) gates the dispatch. Drives the real `prepare_tool_call` path;
/// the deny fires only if the resolved name reached the envelope.
#[tokio::test(flavor = "current_thread")]
async fn pre_tool_use_resolves_meta_dispatch_tool_name_end_to_end() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_agent_with_tools(vec![
                xai_grok_tools::registry::types::ToolConfig::for_tool::<
                    xai_grok_tools::implementations::use_tool::UseTool,
                >(),
            ])
            .await;

            let mut client_hooks = crate::extensions::hooks::ClientHooks::new();
            client_hooks.insert(
                xai_grok_hooks::event::HookEventName::PreToolUse,
                vec![crate::extensions::hooks::ClientHookGroup {
                    matcher: Some(
                        xai_grok_hooks::matcher::HookMatcher::new("linear__save_issue").unwrap(),
                    ),
                    callback_ids: vec!["cb_0".to_string()],
                    timeout: None,
                }],
            );
            *actor.client_hooks.borrow_mut() = client_hooks;
            spawn_deny_responder(gateway_rx, "nope");

            let call = ToolCallResponse {
                id: "call_1".to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new(
                    "use_tool",
                    r#"{"tool_name":"linear__save_issue","tool_input":{}}"#,
                ),
            };

            let mut deferred = Vec::new();
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.prepare_tool_call(call, &mut deferred, None),
            )
            .await
            .expect("prepare_tool_call must not hang")
            .expect("prepare_tool_call must not error");
            assert!(
                matches!(result, Err(ToolLoop::HookDenied { .. })),
                "a hook matched on the resolved tool must gate the use_tool dispatch; \
                 got {result:?}"
            );
        })
        .await;
}

/// Reproduces the prod inheritance seam (subagent.rs `ctx.client_hooks.clone()`) by
/// cloning the parent's hooks into a child `SessionActor`, so the subagent call hits
/// the parent's PreToolUse gate carrying the `subagentType`.
#[tokio::test(flavor = "current_thread")]
async fn subagent_inherits_parent_pre_tool_use_client_hook() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (parent, _parent_gateway_rx, _parent_persistence_rx) = test_actor().await;

            install_client_hook(
                &parent,
                xai_grok_hooks::event::HookEventName::PreToolUse,
                &["cb_0"],
            );

            let (subagent, mut child_gateway_rx, _child_persistence_rx) = test_actor().await;

            assert!(
                subagent.client_hooks.borrow().is_empty(),
                "the subagent starts with no hooks of its own"
            );
            *subagent.client_hooks.borrow_mut() = parent.client_hooks.borrow().clone();

            let seen_subagent_type = std::sync::Arc::new(std::sync::Mutex::new(None::<String>));
            let seen = seen_subagent_type.clone();
            tokio::task::spawn_local(async move {
                while let Some(msg) = child_gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            let params: serde_json::Value =
                                serde_json::from_str(args.request.params.get()).unwrap();
                            *seen.lock().unwrap() =
                                params["subagentType"].as_str().map(str::to_string);
                            let deny: Arc<serde_json::value::RawValue> =
                                serde_json::value::to_raw_value(&serde_json::json!({
                                    "decision": "deny",
                                }))
                                .unwrap()
                                .into();
                            let _ = args.response_tx.send(Ok(acp::ExtResponse::new(deny)));
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let call = ToolCallResponse {
                id: "call_1".to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new(
                    "run_terminal_command",
                    "{}",
                ),
            };
            let tool_call_id = acp::ToolCallId::new("call_1");
            let envelope = subagent.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::PreToolUse,
                None,
                xai_grok_hooks::event::HookPayload::PreToolUse {
                    tool_name: call.function.name.clone(),
                    tool_use_id: call.id.clone(),
                    tool_input: serde_json::json!({}),
                    tool_input_truncated: false,
                    subagent_type: Some("code-reviewer".to_string()),
                },
            );

            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                subagent.run_pre_tool_use_client_hook(&call, &tool_call_id, &envelope),
            )
            .await
            .expect("the gate must not hang")
            .expect("the gate must not error");

            assert!(
                matches!(result, Some(ToolLoop::HookDenied { .. })),
                "a subagent tool call must be blocked by the parent's inherited PreToolUse hook"
            );
            assert_eq!(
                seen_subagent_type.lock().unwrap().as_deref(),
                Some("code-reviewer"),
                "the parent's hook must observe the subagent's type on the dispatch"
            );
        })
        .await;
}

/// A slow/hung callback must not starve a later deny: with the first-registered callback
/// never replying and the second denying, the gate returns `HookDenied` quickly (a
/// sequential gate would block on the hung one's full timeout). Pins the concurrency claim.
#[tokio::test(flavor = "current_thread")]
async fn pre_tool_use_slow_callback_does_not_starve_a_deny() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PreToolUse,
                &["slow_cb", "deny_cb"],
            );

            tokio::task::spawn_local(async move {
                let mut held = Vec::new();
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            let params: serde_json::Value =
                                serde_json::from_str(args.request.params.get()).unwrap();
                            if params["hookCallbackId"] == "deny_cb" {
                                let deny: Arc<serde_json::value::RawValue> =
                                    serde_json::value::to_raw_value(&serde_json::json!({
                                        "decision": "deny",
                                    }))
                                    .unwrap()
                                    .into();
                                let _ = args.response_tx.send(Ok(acp::ExtResponse::new(deny)));
                            } else {
                                held.push(args.response_tx);
                            }
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let call = ToolCallResponse {
                id: "call_1".to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new(
                    "run_terminal_command",
                    "{}",
                ),
            };
            let tool_call_id = acp::ToolCallId::new("call_1");
            let envelope = actor.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::PreToolUse,
                None,
                xai_grok_hooks::event::HookPayload::PreToolUse {
                    tool_name: call.function.name.clone(),
                    tool_use_id: call.id.clone(),
                    tool_input: serde_json::json!({}),
                    tool_input_truncated: false,
                    subagent_type: None,
                },
            );

            // 5s ceiling is well under the hung callback's 30s per-callback timeout, so a
            // pass proves the deny was not serialized behind the slow callback.
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_pre_tool_use_client_hook(&call, &tool_call_id, &envelope),
            )
            .await
            .expect("a deny must resolve without waiting on the hung callback")
            .expect("the gate must not error");
            assert!(matches!(result, Some(ToolLoop::HookDenied { .. })));
        })
        .await;
}

/// PostToolUse and PostToolUseFailure must never both fire for one tool call: a hard
/// dispatch error fires only PostToolUseFailure; a successful dispatch fires only
/// PostToolUse.
#[tokio::test(flavor = "current_thread")]
async fn post_tool_use_and_failure_never_double_fire() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;

            let mut client_hooks = crate::extensions::hooks::ClientHooks::new();
            for event in [
                xai_grok_hooks::event::HookEventName::PostToolUse,
                xai_grok_hooks::event::HookEventName::PostToolUseFailure,
            ] {
                client_hooks.insert(
                    event,
                    vec![crate::extensions::hooks::ClientHookGroup {
                        matcher: None,
                        callback_ids: vec!["cb".to_string()],
                        timeout: None,
                    }],
                );
            }
            *actor.client_hooks.borrow_mut() = client_hooks;

            let todo_call = |id: &str| crate::sampling::types::ToolCallResponse {
                id: id.to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new(
                    "todo_write",
                    r#"{"todos":[{"id":"t1","content":"do","status":"completed"}]}"#,
                ),
            };

            // Failure: no workspace session is bound, so the dispatch hard-errors.
            actor
                .execute_tool_calls(vec![todo_call("call_err")], None)
                .await
                .expect("execute_tool_calls must not error");
            let mut failure_events = Vec::new();
            while let Ok(msg) = gateway_rx.try_recv() {
                if let xai_acp_lib::AcpClientMessage::ExtNotification(args) = msg
                    && args.request.method.as_ref() == "x.ai/hooks/event"
                {
                    let params: serde_json::Value =
                        serde_json::from_str(args.request.params.get()).unwrap();
                    if let Some(name) = params["hookEventName"].as_str() {
                        failure_events.push(name.to_string());
                    }
                }
            }
            assert_eq!(
                failure_events,
                ["post_tool_use_failure"],
                "an errored tool must fire only PostToolUseFailure, never PostToolUse"
            );

            let seen = Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
            let seen_task = seen.clone();
            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            if args.request.method.as_ref() == "x.ai/hooks/run" {
                                let params: serde_json::Value =
                                    serde_json::from_str(args.request.params.get()).unwrap();
                                if let Some(name) = params["hookEventName"].as_str() {
                                    seen_task.lock().unwrap().push(name.to_string());
                                }
                            }
                            let empty: Arc<serde_json::value::RawValue> =
                                serde_json::value::to_raw_value(&serde_json::json!({}))
                                    .unwrap()
                                    .into();
                            let _ = args.response_tx.send(Ok(acp::ExtResponse::new(empty)));
                        }
                        xai_acp_lib::AcpClientMessage::ExtNotification(args) => {
                            if args.request.method.as_ref() == "x.ai/hooks/event" {
                                let params: serde_json::Value =
                                    serde_json::from_str(args.request.params.get()).unwrap();
                                if let Some(name) = params["hookEventName"].as_str() {
                                    seen_task.lock().unwrap().push(name.to_string());
                                }
                            }
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            actor
                .workspace_ops
                .bind_local_session(
                    &actor.session_id_string(),
                    actor.tool_context.cwd.as_path().to_path_buf(),
                    actor.tool_context.hunk_tracker_handle.clone(),
                    actor.agent.borrow().tool_bridge().toolset(),
                    None,
                )
                .expect("bind_local_session must succeed");
            actor
                .execute_tool_calls(vec![todo_call("call_ok")], None)
                .await
                .expect("execute_tool_calls must not error");
            assert_eq!(
                *seen.lock().unwrap(),
                ["post_tool_use"],
                "a successful tool must fire PostToolUse exactly once, never PostToolUseFailure"
            );
        })
        .await;
}

#[derive(Debug)]
struct McpErrorResultTool;

#[derive(Debug)]
struct McpReplacementResultTool;

impl xai_grok_tools::types::tool_metadata::ToolMetadata for McpReplacementResultTool {
    fn kind(&self) -> xai_grok_tools::types::tool::ToolKind {
        xai_grok_tools::types::tool::ToolKind::Other
    }

    fn tool_namespace(&self) -> xai_grok_tools::types::tool::ToolNamespace {
        xai_grok_tools::types::tool::ToolNamespace::MCP
    }

    fn description_template(&self) -> &str {
        "MCP output replacement fixture"
    }
}

impl xai_tool_runtime::Tool for McpReplacementResultTool {
    type Args = serde_json::Value;
    type Output = xai_grok_tools::types::output::ToolOutput;

    fn id(&self) -> xai_tool_protocol::ToolId {
        xai_tool_protocol::ToolId::new("mock_replacement_tool").unwrap()
    }

    fn description(
        &self,
        _context: &xai_tool_runtime::ListToolsContext,
    ) -> xai_tool_types::ToolDescription {
        xai_tool_types::ToolDescription::new("mock_replacement_tool", "MCP replacement fixture")
    }

    async fn run(
        &self,
        _context: xai_tool_runtime::ToolCallContext,
        _arguments: serde_json::Value,
    ) -> Result<Self::Output, xai_tool_runtime::ToolError> {
        Ok(xai_grok_tools::types::output::ToolOutput::MCP(
            xai_grok_tools::types::output::MCPOutput::okay_output(
                "mock_replacement_tool".into(),
                "mock".into(),
                "private-original-output".into(),
            ),
        ))
    }
}

#[tokio::test(flavor = "current_thread")]
async fn nested_code_mode_hooks_replace_output_and_deliver_context_once_after_outer_result() {
    tokio::task::LocalSet::new().run_until(async {
        let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;
        let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
        *actor.current_prompt_id.lock().unwrap() = Some("nested-test".into());
        *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
        actor.agent.borrow().tool_bridge().clone().register_mcp_tools(
            "mock_replacement_tool".into(),
            McpReplacementResultTool,
            Some(serde_json::json!({"type": "object"})),
        ).await.unwrap();
        install_pre_tool_use_hooks(&mut actor, vec![
            pre_tool_use_spec("pre-context", None, r#"echo '{"hookSpecificOutput":{"additionalContext":"pre-note"}}'"#),
            post_tool_use_spec("post-context", None, r#"echo '{"hookSpecificOutput":{"updatedMCPToolOutput":"redacted-output","additionalContext":"post-note"}}'"#),
        ]);
        install_client_hook(&actor, xai_grok_hooks::event::HookEventName::PostToolUse, &["client-context"]);
        let observed = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let observed_callback = observed.clone();
        spawn_run_responder(gateway_rx, move |_| {
            observed_callback.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            serde_json::json!({"additionalContext": "client-note"})
        });
        actor.workspace_ops.bind_local_session(
            &actor.session_id_string(),
            actor.tool_context.cwd.as_path().to_path_buf(),
            actor.tool_context.hunk_tracker_handle.clone(),
            actor.agent.borrow().tool_bridge().toolset(),
            None,
        ).unwrap();
        let (progress, _receiver) = xai_grok_code_mode_protocol::nested_tool_progress_channel();
        let output = actor.dispatch_code_mode_nested_tool(
            xai_grok_code_mode_protocol::CodeModeNestedToolCall {
                cell_id: xai_grok_code_mode_protocol::CellId::new("hook-cell".into()),
                runtime_tool_call_id: "nested-hook-call".into(),
                tool_name: xai_grok_code_mode_protocol::ToolName::plain("mock_replacement_tool"),
                tool_kind: xai_grok_code_mode_protocol::CodeModeToolKind::Function,
                input: Some(serde_json::json!({})),
            },
            tokio_util::sync::CancellationToken::new(),
            progress,
        ).await.unwrap();
        assert_eq!(output, serde_json::json!("redacted-output"));
        assert_eq!(observed.load(std::sync::atomic::Ordering::SeqCst), 1);
        assert_eq!(actor.code_mode_hook_followups.borrow().len(), 3);
        let before = actor.chat_state_handle.get_conversation().await;
        assert!(!before.iter().any(|item| item.text_content().contains("pre-note")));
        actor.chat_state_handle.push_tool_result(ConversationItem::tool_result("outer-exec", "outer-result"));
        actor.flush_code_mode_hook_followups();
        actor.flush_code_mode_hook_followups();
        let conversation = actor.chat_state_handle.get_conversation().await;
        let result_index = conversation.iter().position(|item| item.text_content().contains("outer-result")).unwrap();
        let mut previous = result_index;
        for note in ["pre-note", "post-note", "client-note"] {
            let positions: Vec<_> = conversation.iter().enumerate().filter_map(|(index, item)| item.text_content().contains(note).then_some(index)).collect();
            assert_eq!(positions.len(), 1, "{note} must be delivered once");
            assert!(positions[0] > previous);
            previous = positions[0];
        }
        assert!(!conversation.iter().any(|item| item.text_content().contains("private-original-output")));
        assert!(actor.code_mode_hook_followups.borrow().is_empty());
    }).await;
}

#[tokio::test(flavor = "current_thread")]
async fn nested_hook_context_is_discarded_when_dispatch_is_cancelled_or_changes_turn() {
    tokio::task::LocalSet::new()
        .run_until(async {
            for cancel in [true, false] {
                let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;
                let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
                *actor.current_prompt_id.lock().unwrap() = Some("old-turn".into());
                *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
                actor
                    .agent
                    .borrow()
                    .tool_bridge()
                    .clone()
                    .register_mcp_tools(
                        "mock_replacement_tool".into(),
                        McpReplacementResultTool,
                        Some(serde_json::json!({"type": "object"})),
                    )
                    .await
                    .unwrap();
                install_client_hook(
                    &actor,
                    xai_grok_hooks::event::HookEventName::PostToolUse,
                    &["late-context"],
                );
                actor
                    .workspace_ops
                    .bind_local_session(
                        &actor.session_id_string(),
                        actor.tool_context.cwd.as_path().to_path_buf(),
                        actor.tool_context.hunk_tracker_handle.clone(),
                        actor.agent.borrow().tool_bridge().toolset(),
                        None,
                    )
                    .unwrap();
                let actor = Arc::new(actor);
                let token = tokio_util::sync::CancellationToken::new();
                let callback_token = token.clone();
                let callback_actor = actor.clone();
                let seen = Arc::new(std::sync::atomic::AtomicBool::new(false));
                let callback_seen = seen.clone();
                spawn_run_responder(gateway_rx, move |params| {
                    if params["hookCallbackId"] != "late-context" {
                        return serde_json::json!({});
                    }
                    callback_seen.store(true, std::sync::atomic::Ordering::SeqCst);
                    if cancel {
                        callback_token.cancel();
                    } else {
                        *callback_actor.current_prompt_id.lock().unwrap() = Some("new-turn".into());
                    }
                    serde_json::json!({"additionalContext": "stale-context"})
                });
                let (progress, _receiver) =
                    xai_grok_code_mode_protocol::nested_tool_progress_channel();
                let _ = actor
                    .dispatch_code_mode_nested_tool(
                        xai_grok_code_mode_protocol::CodeModeNestedToolCall {
                            cell_id: xai_grok_code_mode_protocol::CellId::new("late-cell".into()),
                            runtime_tool_call_id: "late-call".into(),
                            tool_name: xai_grok_code_mode_protocol::ToolName::plain(
                                "mock_replacement_tool",
                            ),
                            tool_kind: xai_grok_code_mode_protocol::CodeModeToolKind::Function,
                            input: Some(serde_json::json!({})),
                        },
                        token,
                        progress,
                    )
                    .await;
                assert!(seen.load(std::sync::atomic::Ordering::SeqCst));
                assert!(actor.code_mode_hook_followups.borrow().is_empty());
                actor.flush_code_mode_hook_followups();
                assert!(
                    !actor
                        .chat_state_handle
                        .get_conversation()
                        .await
                        .iter()
                        .any(|item| item.text_content().contains("stale-context"))
                );
            }
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn exec_only_turn_delivers_nested_hook_context_to_next_sample() {
    use xai_grok_test_support::sse::{
        responses_api_reasoning_then_tool_call_events, responses_api_script_exact,
    };
    use xai_grok_test_support::{MockInferenceServer, ScriptedResponse};

    tokio::task::LocalSet::new()
        .run_until(async {
            let server = MockInferenceServer::start().await.unwrap();
            let arguments =
                serde_json::json!({"source": "text(await tools.mock_replacement_tool({}));"})
                    .to_string();
            server.enqueue_response(
                "/v1/responses",
                ScriptedResponse::sse(responses_api_reasoning_then_tool_call_events(
                    "use tool",
                    "exec-call",
                    "exec",
                    &arguments,
                    "test",
                )),
            );
            server.enqueue_response(
                "/v1/responses",
                ScriptedResponse::sse(responses_api_script_exact("done", "test")),
            );
            let (mut actor, gateway_rx, mut persistence_rx) = test_actor().await;
            let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
            spawn_run_responder(gateway_rx, |_| serde_json::json!({}));
            tokio::task::spawn_local(async move {
                while let Some(message) = persistence_rx.recv().await {
                    if let PersistenceMsg::FlushAndAck { respond_to } = message {
                        let _ = respond_to.send(Ok(()));
                    }
                }
            });
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
            actor
                .agent
                .borrow_mut()
                .set_tool_mode(xai_grok_sampling_types::ToolMode::CodeMode);
            actor
                .agent
                .borrow()
                .tool_bridge()
                .clone()
                .register_mcp_tools(
                    "mock_replacement_tool".into(),
                    McpReplacementResultTool,
                    Some(serde_json::json!({"type": "object"})),
                )
                .await
                .unwrap();
            install_pre_tool_use_hooks(
                &mut actor,
                vec![post_tool_use_spec(
                    "post-context",
                    None,
                    r#"echo '{"hookSpecificOutput":{"additionalContext":"exec-only-hook-note"}}'"#,
                )],
            );
            actor
                .workspace_ops
                .bind_local_session(
                    &actor.session_id_string(),
                    actor.tool_context.cwd.as_path().to_path_buf(),
                    actor.tool_context.hunk_tracker_handle.clone(),
                    actor.agent.borrow().tool_bridge().toolset(),
                    None,
                )
                .unwrap();
            let sampler_config = xai_grok_sampler::SamplerConfig {
                api_key: Some("test-key".into()),
                base_url: server.url(),
                model: "test".into(),
                api_backend: xai_grok_sampler::ApiBackend::Responses,
                context_window: 256_000,
                max_retries: Some(0),
                idle_timeout_secs: Some(30),
                ..Default::default()
            };
            let (sampler_tx, mut sampler_rx) = tokio::sync::mpsc::unbounded_channel();
            actor.sampler_handle = xai_grok_sampler::SamplerActor::spawn(
                sampler_config,
                xai_grok_sampler::RetryPolicy {
                    max_retries: 0,
                    rate_limit_retry_threshold: 0,
                    ..Default::default()
                },
                sampler_tx,
            );
            let mut cfg = actor.chat_state_handle.get_sampling_config().await.unwrap();
            cfg.base_url = server.url();
            cfg.api_backend = xai_grok_sampling_types::ApiBackend::Responses;
            cfg.model = "test".into();
            actor.chat_state_handle.update_sampling_config(cfg);
            let mut creds = actor.chat_state_handle.get_credentials().await;
            creds.api_key = Some("test-key".into());
            actor.chat_state_handle.update_credentials(creds);
            actor.state.lock().await.running_task = Some(running_task_stub("exec-only"));
            *actor.current_prompt_id.lock().unwrap() = Some("exec-only".into());
            let actor = Arc::new(actor);
            actor
                .rebuild_spec
                .code_mode_runtime
                .start_dispatch_loop(Arc::downgrade(&actor))
                .await
                .unwrap();
            let turn_owner = actor.current_code_mode_turn_owner().await.unwrap();
            let drainer = actor.clone();
            tokio::task::spawn_local(async move {
                while let Some(event) = sampler_rx.recv().await {
                    drainer.handle_sampling_event(event).await;
                }
            });
            let outcome = tokio::time::timeout(
                std::time::Duration::from_secs(30),
                crate::session::code_mode::CODE_MODE_TURN_OWNER.scope(
                    turn_owner,
                    actor.handle_prompt(
                        "exec-only",
                        vec![acp::ContentBlock::Text(acp::TextContent::new(
                            "use the tool",
                        ))],
                        PromptMode::Agent,
                        None,
                        None,
                        None,
                        None,
                        true,
                        false,
                        None,
                        None,
                        None,
                    ),
                ),
            )
            .await
            .expect("turn timed out");
            assert!(outcome.is_ok(), "turn failed: {outcome:?}");
            let requests: Vec<_> = server
                .requests()
                .into_iter()
                .filter(|request| request.path == "/v1/responses")
                .collect();
            assert_eq!(
                requests.len(),
                2,
                "expected an exec round and a follow-up sample"
            );
            let second = requests[1].body.as_ref().unwrap().to_string();
            assert!(
                second.contains("private-original-output"),
                "exec result must be present: {second}"
            );
            assert_eq!(
                second.matches("exec-only-hook-note").count(),
                1,
                "hook context must reach the next sample once: {second}"
            );
            assert!(
                second.find("private-original-output").unwrap()
                    < second.find("exec-only-hook-note").unwrap()
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn delivered_nested_post_hook_notes_trigger_compaction_and_preflight() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;
            let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
            *actor.current_prompt_id.lock().unwrap() = Some("compaction-hook-test".into());
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
            actor
                .agent
                .borrow()
                .tool_bridge()
                .clone()
                .register_mcp_tools(
                    "mock_replacement_tool".into(),
                    McpReplacementResultTool,
                    Some(serde_json::json!({"type": "object"})),
                )
                .await
                .unwrap();
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PostToolUse,
                &["large-post-hook"],
            );
            spawn_run_responder(gateway_rx, |_| {
                serde_json::json!({
                    "decision": "block",
                    "reason": "large-feedback-".repeat(500),
                    "additionalContext": "large-context-".repeat(500),
                })
            });
            actor
                .workspace_ops
                .bind_local_session(
                    &actor.session_id_string(),
                    actor.tool_context.cwd.as_path().to_path_buf(),
                    actor.tool_context.hunk_tracker_handle.clone(),
                    actor.agent.borrow().tool_bridge().toolset(),
                    None,
                )
                .unwrap();

            let mut cfg = actor.chat_state_handle.get_sampling_config().await.unwrap();
            cfg.context_window = std::num::NonZeroU64::new(1_500).unwrap();
            actor.chat_state_handle.update_sampling_config(cfg);
            actor.compaction.threshold_percent.set(85);
            assert!(actor.check_auto_compact_needed().await.is_none());
            assert!(actor.check_preflight_overflow().await.is_none());

            let (progress, _receiver) = xai_grok_code_mode_protocol::nested_tool_progress_channel();
            actor
                .dispatch_code_mode_nested_tool(
                    xai_grok_code_mode_protocol::CodeModeNestedToolCall {
                        cell_id: xai_grok_code_mode_protocol::CellId::new("compact-cell".into()),
                        runtime_tool_call_id: "compact-nested-call".into(),
                        tool_name: xai_grok_code_mode_protocol::ToolName::plain(
                            "mock_replacement_tool",
                        ),
                        tool_kind: xai_grok_code_mode_protocol::CodeModeToolKind::Function,
                        input: Some(serde_json::json!({})),
                    },
                    tokio_util::sync::CancellationToken::new(),
                    progress,
                )
                .await
                .unwrap();
            actor
                .chat_state_handle
                .push_tool_result(ConversationItem::tool_result("outer-exec", "outer-result"));
            let before = actor.chat_state_handle.get_estimated_total_tokens().await;
            assert!(actor.check_auto_compact_needed().await.is_none());
            assert!(actor.check_preflight_overflow().await.is_none());

            actor.execute_tool_calls(Vec::new(), None).await.unwrap();
            let conversation = actor.chat_state_handle.get_conversation().await;
            assert!(
                conversation
                    .iter()
                    .any(|item| item.text_content().contains("large-context-"))
            );
            assert!(
                conversation
                    .iter()
                    .any(|item| item.text_content().contains("large-feedback-"))
            );
            let after = actor.chat_state_handle.get_estimated_total_tokens().await;
            assert!(
                after > before,
                "delivered hook notes must increase the estimate"
            );
            assert!(actor.check_auto_compact_needed().await.is_some());
            assert!(actor.check_preflight_overflow().await.is_some());
        })
        .await;
}

#[derive(Clone, Debug)]
struct BlockingPathTool {
    entered: tokio::sync::mpsc::UnboundedSender<String>,
    normal_release: Arc<tokio::sync::Notify>,
    nested_release: Arc<tokio::sync::Notify>,
}

impl xai_grok_tools::types::tool_metadata::ToolMetadata for BlockingPathTool {
    fn kind(&self) -> xai_grok_tools::types::tool::ToolKind {
        xai_grok_tools::types::tool::ToolKind::Other
    }
    fn tool_namespace(&self) -> xai_grok_tools::types::tool::ToolNamespace {
        xai_grok_tools::types::tool::ToolNamespace::MCP
    }
    fn description_template(&self) -> &str {
        "blocking path test tool"
    }
}

impl xai_tool_runtime::Tool for BlockingPathTool {
    type Args = serde_json::Value;
    type Output = xai_grok_tools::types::output::ToolOutput;
    fn id(&self) -> xai_tool_protocol::ToolId {
        xai_tool_protocol::ToolId::new("blocking_path_tool").unwrap()
    }
    fn description(
        &self,
        _: &xai_tool_runtime::ListToolsContext,
    ) -> xai_tool_types::ToolDescription {
        xai_tool_types::ToolDescription::new("blocking_path_tool", "blocking path test tool")
    }
    async fn run(
        &self,
        _: xai_tool_runtime::ToolCallContext,
        args: serde_json::Value,
    ) -> Result<Self::Output, xai_tool_runtime::ToolError> {
        let label = args["label"].as_str().unwrap().to_owned();
        let _ = self.entered.send(label.clone());
        match label.as_str() {
            "normal" => self.normal_release.notified().await,
            "nested-first" => self.nested_release.notified().await,
            _ => {}
        }
        Ok(xai_grok_tools::types::output::ToolOutput::MCP(
            xai_grok_tools::types::output::MCPOutput::okay_output(
                "blocking_path_tool".into(),
                "mock".into(),
                label,
            ),
        ))
    }
}

#[tokio::test(flavor = "current_thread")]
async fn nested_and_normal_dispatch_share_file_locks_and_release_on_cancel() {
    tokio::task::LocalSet::new().run_until(async {
        let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;
        let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
        *actor.current_prompt_id.lock().unwrap() = Some("nested-test".into());
        *actor.agent.borrow_mut() = test_agent_with_tools(vec![
            xai_grok_tools::registry::types::ToolConfig::from_id("Codex:apply_patch"),
        ]).await;
        let (entered_tx, mut entered_rx) = tokio::sync::mpsc::unbounded_channel();
        let normal_release = Arc::new(tokio::sync::Notify::new());
        let nested_release = Arc::new(tokio::sync::Notify::new());
        actor.agent.borrow().tool_bridge().clone().register_mcp_tools(
            "blocking_path_tool".into(),
            BlockingPathTool { entered: entered_tx, normal_release: normal_release.clone(), nested_release: nested_release.clone() },
            Some(serde_json::json!({"type":"object","properties":{"path":{"type":"string"},"label":{"type":"string"}}})),
        ).await.unwrap();
        spawn_run_responder(gateway_rx, |_| serde_json::json!({}));
        actor.workspace_ops.bind_local_session(
            &actor.session_id_string(), actor.tool_context.cwd.as_path().to_path_buf(),
            actor.tool_context.hunk_tracker_handle.clone(), actor.agent.borrow().tool_bridge().toolset(), None,
        ).unwrap();
        let actor = std::rc::Rc::new(actor);
        let normal = {
            let actor = actor.clone();
            tokio::task::spawn_local(async move {
                let call = crate::sampling::types::ToolCallResponse {
                    id: "normal-lock".into(), kind: "function".into(),
                    function: crate::sampling::types::ToolCallFunction::new(
                        "blocking_path_tool", serde_json::json!({"path":"/tmp/lock-a","label":"normal"}).to_string(),
                    ),
                };
                actor.execute_tool_calls(vec![call], None).await
            })
        };
        async fn next(rx: &mut tokio::sync::mpsc::UnboundedReceiver<String>) -> String {
            tokio::time::timeout(std::time::Duration::from_secs(2), rx.recv()).await.unwrap().unwrap()
        }
        assert_eq!(next(&mut entered_rx).await, "normal");
        let nested = |label: &'static str, path: &'static str, cancel: tokio_util::sync::CancellationToken| {
            let actor = actor.clone();
            tokio::task::spawn_local(async move {
                let (progress, _receiver) = xai_grok_code_mode_protocol::nested_tool_progress_channel();
                actor.dispatch_code_mode_nested_tool(
                    xai_grok_code_mode_protocol::CodeModeNestedToolCall {
                        cell_id: xai_grok_code_mode_protocol::CellId::new("lock-cell".into()),
                        runtime_tool_call_id: label.into(),
                        tool_name: xai_grok_code_mode_protocol::ToolName::plain("blocking_path_tool"),
                        tool_kind: xai_grok_code_mode_protocol::CodeModeToolKind::Function,
                        input: Some(serde_json::json!({"path":path,"label":label})),
                    }, cancel, progress,
                ).await
            })
        };
        let cancel = tokio_util::sync::CancellationToken::new();
        let first = nested("nested-first", "/tmp/lock-a", cancel.clone());
        let other = nested("nested-other", "/tmp/lock-b", tokio_util::sync::CancellationToken::new());
        assert_eq!(next(&mut entered_rx).await, "nested-other");
        other.await.unwrap().unwrap();
        assert!(tokio::time::timeout(std::time::Duration::from_millis(100), entered_rx.recv()).await.is_err());
        normal_release.notify_one();
        assert_eq!(next(&mut entered_rx).await, "nested-first");
        normal.await.unwrap().unwrap();
        let second = nested("nested-second", "/tmp/lock-a", tokio_util::sync::CancellationToken::new());
        assert!(tokio::time::timeout(std::time::Duration::from_millis(100), entered_rx.recv()).await.is_err());
        cancel.cancel();
        assert!(first.await.unwrap().is_err());
        assert_eq!(next(&mut entered_rx).await, "nested-second");
        second.await.unwrap().unwrap();

        // A pathless multi-file patch must wait even when its target differs
        // from a currently running path-aware edit.
        let holder = nested("nested-first", "/tmp/lock-a", tokio_util::sync::CancellationToken::new());
        assert_eq!(next(&mut entered_rx).await, "nested-first");
        let dir = tempfile::tempdir().unwrap();
        let patched_file = dir.path().join("patched.txt");
        let patch_text = format!("*** Begin Patch\n*** Add File: {}\n+created\n*** End Patch", patched_file.display());
        let patch = {
            let actor = actor.clone();
            tokio::task::spawn_local(async move {
                let (progress, _receiver) = xai_grok_code_mode_protocol::nested_tool_progress_channel();
                actor.dispatch_code_mode_nested_tool(
                    xai_grok_code_mode_protocol::CodeModeNestedToolCall {
                        cell_id: xai_grok_code_mode_protocol::CellId::new("patch-cell".into()),
                        runtime_tool_call_id: "patch".into(),
                        tool_name: xai_grok_code_mode_protocol::ToolName::plain("apply_patch"),
                        tool_kind: xai_grok_code_mode_protocol::CodeModeToolKind::Freeform,
                        input: Some(serde_json::Value::String(patch_text)),
                    }, tokio_util::sync::CancellationToken::new(), progress,
                ).await
            })
        };
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        assert!(!patch.is_finished());
        assert!(!patched_file.exists());
        nested_release.notify_one();
        holder.await.unwrap().unwrap();
        patch.await.unwrap().unwrap();
        assert_eq!(std::fs::read_to_string(patched_file).unwrap(), "created\n");
    }).await;
}

#[tokio::test(flavor = "current_thread")]
async fn hook_stop_prevents_normal_and_nested_tools_waiting_on_file_lock() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;
            let (_delivery_guard, _delivery_rx) = install_test_delivery_scope(&mut actor);
            *actor.current_prompt_id.lock().unwrap() = Some("lock-stop".into());
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
            let (entered_tx, mut entered_rx) = tokio::sync::mpsc::unbounded_channel();
            let holder_release = Arc::new(tokio::sync::Notify::new());
            actor
                .agent
                .borrow()
                .tool_bridge()
                .clone()
                .register_mcp_tools(
                    "blocking_path_tool".into(),
                    BlockingPathTool {
                        entered: entered_tx,
                        normal_release: Arc::new(tokio::sync::Notify::new()),
                        nested_release: holder_release.clone(),
                    },
                    Some(serde_json::json!({"type":"object","properties":{"path":{"type":"string"},"label":{"type":"string"}}})),
                )
                .await
                .unwrap();
            spawn_run_responder(gateway_rx, |_| serde_json::json!({}));
            actor
                .workspace_ops
                .bind_local_session(
                    &actor.session_id_string(),
                    actor.tool_context.cwd.as_path().to_path_buf(),
                    actor.tool_context.hunk_tracker_handle.clone(),
                    actor.agent.borrow().tool_bridge().toolset(),
                    None,
                )
                .unwrap();
            let actor = std::rc::Rc::new(actor);
            let nested = |label: &'static str| {
                let actor = actor.clone();
                tokio::task::spawn_local(async move {
                    let (progress, _receiver) =
                        xai_grok_code_mode_protocol::nested_tool_progress_channel();
                    actor
                        .dispatch_code_mode_nested_tool(
                            xai_grok_code_mode_protocol::CodeModeNestedToolCall {
                                cell_id: xai_grok_code_mode_protocol::CellId::new("lock-stop-cell".into()),
                                runtime_tool_call_id: label.into(),
                                tool_name: xai_grok_code_mode_protocol::ToolName::plain("blocking_path_tool"),
                                tool_kind: xai_grok_code_mode_protocol::CodeModeToolKind::Function,
                                input: Some(serde_json::json!({"path":"/tmp/stop-lock", "label":label})),
                            },
                            tokio_util::sync::CancellationToken::new(),
                            progress,
                        )
                        .await
                })
            };
            let holder = nested("nested-first");
            assert_eq!(
                tokio::time::timeout(std::time::Duration::from_secs(2), entered_rx.recv())
                    .await
                    .unwrap()
                    .unwrap(),
                "nested-first"
            );
            let queued_nested = nested("queued-nested");
            let queued_normal = {
                let actor = actor.clone();
                tokio::task::spawn_local(async move {
                    actor
                        .execute_tool_calls(
                            vec![batch_call(
                                "queued-normal",
                                "blocking_path_tool",
                                r#"{"path":"/tmp/stop-lock","label":"queued-normal"}"#,
                            )],
                            None,
                        )
                        .await
                })
            };
            assert!(tokio::time::timeout(std::time::Duration::from_millis(100), entered_rx.recv()).await.is_err());
            assert!(!queued_nested.is_finished());
            assert!(!queued_normal.is_finished());
            let identity = actor.hook_stop_identity().unwrap();
            assert!(actor.record_hook_stop_for(
                &identity,
                xai_grok_hooks::dispatcher::StopBlock {
                    hook_name: "test-stop".into(),
                    reason: "stop waiting tools".into(),
                },
            ));
            holder_release.notify_one();
            let _ = holder.await.unwrap();
            assert!(queued_nested.await.unwrap().is_err());
            let _ = queued_normal.await.unwrap();
            assert!(entered_rx.try_recv().is_err(), "waiting tools must not run after the stop");
        })
        .await;
}

impl xai_grok_tools::types::tool_metadata::ToolMetadata for McpErrorResultTool {
    fn kind(&self) -> xai_grok_tools::types::tool::ToolKind {
        xai_grok_tools::types::tool::ToolKind::Other
    }
    fn tool_namespace(&self) -> xai_grok_tools::types::tool::ToolNamespace {
        xai_grok_tools::types::tool::ToolNamespace::MCP
    }
    fn description_template(&self) -> &str {
        "stub MCP tool that returns an error result"
    }
}

impl xai_tool_runtime::Tool for McpErrorResultTool {
    type Args = serde_json::Value;
    type Output = xai_grok_tools::types::output::ToolOutput;

    fn id(&self) -> xai_tool_protocol::ToolId {
        xai_tool_protocol::ToolId::new("mock_error_tool").expect("valid tool id")
    }

    fn description(
        &self,
        _ctx: &xai_tool_runtime::ListToolsContext,
    ) -> xai_tool_types::ToolDescription {
        xai_tool_types::ToolDescription::new("mock_error_tool", "stub MCP error tool")
    }

    async fn run(
        &self,
        _ctx: xai_tool_runtime::ToolCallContext,
        _args: serde_json::Value,
    ) -> Result<Self::Output, xai_tool_runtime::ToolError> {
        Ok(xai_grok_tools::types::output::ToolOutput::MCP(
            xai_grok_tools::types::output::MCPOutput::errored(
                "mock_error_tool".into(),
                "mock".into(),
                "upstream exploded".into(),
            ),
        ))
    }
}

#[tokio::test(flavor = "current_thread")]
async fn mcp_error_result_fires_only_failure_and_delivers_original_output() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;
            let bridge = actor.agent.borrow().tool_bridge().clone();
            bridge
                .register_mcp_tools(
                    "mock_error_tool".to_string(),
                    McpErrorResultTool,
                    Some(serde_json::json!({ "type": "object" })),
                )
                .await
                .expect("stub tool registration must succeed");

            let mut client_hooks = crate::extensions::hooks::ClientHooks::new();
            for event in [
                xai_grok_hooks::event::HookEventName::PostToolUse,
                xai_grok_hooks::event::HookEventName::PostToolUseFailure,
            ] {
                client_hooks.insert(
                    event,
                    vec![crate::extensions::hooks::ClientHookGroup {
                        matcher: None,
                        callback_ids: vec!["cb".to_string()],
                        timeout: None,
                    }],
                );
            }
            *actor.client_hooks.borrow_mut() = client_hooks;

            let seen = Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
            let seen_task = seen.clone();
            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            if args.request.method.as_ref() == "x.ai/hooks/run" {
                                let params: serde_json::Value =
                                    serde_json::from_str(args.request.params.get()).unwrap();
                                if let Some(name) = params["hookEventName"].as_str() {
                                    seen_task.lock().unwrap().push(name.to_string());
                                }
                            }
                            let empty: Arc<serde_json::value::RawValue> =
                                serde_json::value::to_raw_value(&serde_json::json!({}))
                                    .unwrap()
                                    .into();
                            let _ = args.response_tx.send(Ok(acp::ExtResponse::new(empty)));
                        }
                        xai_acp_lib::AcpClientMessage::ExtNotification(args) => {
                            if args.request.method.as_ref() == "x.ai/hooks/event" {
                                let params: serde_json::Value =
                                    serde_json::from_str(args.request.params.get()).unwrap();
                                if let Some(name) = params["hookEventName"].as_str() {
                                    seen_task.lock().unwrap().push(name.to_string());
                                }
                            }
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            actor
                .workspace_ops
                .bind_local_session(
                    &actor.session_id_string(),
                    actor.tool_context.cwd.as_path().to_path_buf(),
                    actor.tool_context.hunk_tracker_handle.clone(),
                    actor.agent.borrow().tool_bridge().toolset(),
                    None,
                )
                .expect("bind_local_session must succeed");

            let call = crate::sampling::types::ToolCallResponse {
                id: "call_mcp_err".to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new("mock_error_tool", "{}"),
            };
            actor
                .execute_tool_calls(vec![call], None)
                .await
                .expect("execute_tool_calls must not error");
            for _ in 0..10 {
                if !seen.lock().unwrap().is_empty() {
                    break;
                }
                tokio::task::yield_now().await;
            }

            assert_eq!(
                *seen.lock().unwrap(),
                ["post_tool_use_failure"],
                "an MCP error result must fire only PostToolUseFailure, never PostToolUse"
            );

            let conversation = actor.chat_state_handle.get_conversation().await;
            assert!(
                conversation
                    .iter()
                    .any(|item| format!("{item:?}").contains("upstream exploded")),
                "the original MCP error output must reach the model unchanged, got: {conversation:?}"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn post_tool_use_failure_additional_context_reaches_model() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (mut actor, _gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;

            install_pre_tool_use_hooks(
                &mut actor,
                vec![post_tool_use_failure_spec(
                    "recover_hook",
                    None,
                    r#"echo '{"hookSpecificOutput":{"additionalContext":"retry after binding"}}'"#,
                )],
            );

            let failing_call = crate::sampling::types::ToolCallResponse {
                id: "call_err".to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new(
                    "todo_write",
                    r#"{"todos":[{"id":"t1","content":"do","status":"completed"}]}"#,
                ),
            };

            actor
                .execute_tool_calls(vec![failing_call], None)
                .await
                .expect("execute_tool_calls must not error");

            let conversation = actor.chat_state_handle.get_conversation().await;
            let failed_result_pos = conversation
                .iter()
                .position(|item| {
                    matches!(item, ConversationItem::ToolResult(tr) if tr.tool_call_id.as_str() == "call_err")
                })
                .expect("the failed tool result must be in the conversation");
            let note_pos = conversation
                .iter()
                .position(|item| format!("{item:?}").contains("retry after binding"))
                .expect("the failure additionalContext note must be in the conversation");
            assert!(
                note_pos > failed_result_pos,
                "the PostToolUseFailure additionalContext note must appear AFTER the failed tool result \
                 (failed_result_pos={failed_result_pos}, note_pos={note_pos}), conversation: {conversation:?}"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn pre_tool_use_deny_feeds_reason_back_and_continues_turn() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            *actor.agent.borrow_mut() = test_grok_build_agent_with_todo().await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PreToolUse,
                &["cb_0"],
            );
            spawn_deny_responder(gateway_rx, "use read_file instead");

            let call = ToolCallResponse {
                id: "call_1".to_string(),
                kind: "function".to_string(),
                function: crate::sampling::types::ToolCallFunction::new(
                    "todo_write",
                    r#"{"todos":[{"id":"t1","content":"do","status":"completed"}]}"#,
                ),
            };

            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.execute_tool_calls(vec![call], None),
            )
            .await
            .expect("execute_tool_calls must not hang")
            .expect("execute_tool_calls must not error");

            assert!(
                matches!(result, ToolLoop::Continue),
                "a pre_tool_use deny must continue the turn, got {result:?}"
            );

            let conv = actor.chat_state_handle.get_conversation().await;
            assert!(
                conv.iter()
                    .any(|c| c.text_content().contains("use read_file instead")),
                "the deny reason must be fed back as the tool_result"
            );
        })
        .await;
}

/// The Stop client gate collects every deny as a block (no short-circuit).
#[tokio::test(flavor = "current_thread")]
async fn stop_client_gate_maps_deny_continue_false_and_context() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_block", "cb_stop", "cb_ctx"],
            );

            spawn_run_responder(gateway_rx, |params| {
                match params["hookCallbackId"].as_str() {
                    Some("cb_block") => serde_json::json!({
                        "decision": "deny",
                        "systemMessage": "finish the tests first",
                    }),
                    Some("cb_stop") => {
                        serde_json::json!({ "continue": false, "stopReason": "budget" })
                    }
                    _ => serde_json::json!({ "additionalContext": "run the linter" }),
                }
            });

            let envelope = actor.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::Stop,
                Some("prompt-1".to_string()),
                xai_grok_hooks::event::HookPayload::Stop {
                    reason: "end_turn".to_string(),
                    stop_hook_active: false,
                    last_assistant_message: None,
                    background_tasks: None,
                    session_crons: None,
                },
            );
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_client_hooks(&envelope),
            )
            .await
            .expect("the stop gate must not hang");

            assert_eq!(result.blocks.len(), 1, "only the deny becomes a block");
            assert_eq!(result.blocks[0].hook_name, "client:cb_block");
            assert_eq!(result.blocks[0].reason, "finish the tests first");
            let prevent = result
                .prevent_continuation
                .expect("continue:false becomes prevent_continuation");
            assert_eq!(prevent.hook_name, "client:cb_stop");
            assert_eq!(prevent.reason, "budget");
            assert_eq!(result.additional_context, ["run the linter"]);
        })
        .await;
}

/// `continue: false` becomes a force-stop (with `stopReason`) and `additionalContext`
/// becomes non-error feedback, matching what file hooks express.
#[tokio::test(flavor = "current_thread")]
async fn post_tool_use_client_gate_contributes_block_and_context() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PostToolUse,
                &["cb_block", "cb_ctx"],
            );

            spawn_run_responder(gateway_rx, |params| {
                if params["hookCallbackId"] == "cb_block" {
                    serde_json::json!({ "decision": "block", "reason": "revert that edit" })
                } else {
                    serde_json::json!({ "additionalContext": "run the linter" })
                }
            });

            let envelope = post_tool_use_envelope(&actor);
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_post_tool_use_client_hooks(&envelope),
            )
            .await
            .expect("the post_tool_use gate must not hang");

            assert_eq!(result.blocks.len(), 1, "only the denying callback blocks");
            assert_eq!(result.blocks[0].hook_name, "client:cb_block");
            assert_eq!(result.blocks[0].reason, "revert that edit");
            assert_eq!(result.additional_context.len(), 1);
            assert_eq!(result.additional_context[0].hook_name, "client:cb_ctx");
            assert_eq!(result.additional_context[0].text, "run the linter");
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn post_tool_use_client_gate_records_failure_and_orders_contributions() {
    use xai_grok_hooks::result::HookRunResult;

    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PostToolUse,
                &["cb_ctx_a", "cb_fail", "cb_ctx_b"],
            );

            tokio::task::spawn_local(async move {
                let mut buffered = Vec::new();
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            buffered.push(args);
                            if buffered.len() == 3 {
                                while let Some(args) = buffered.pop() {
                                    let params: serde_json::Value =
                                        serde_json::from_str(args.request.params.get()).unwrap();
                                    let reply: Result<acp::ExtResponse, acp::Error> =
                                        if params["hookCallbackId"] == "cb_fail" {
                                            Err(acp::Error::internal_error())
                                        } else {
                                            let value = if params["hookCallbackId"] == "cb_ctx_a" {
                                                serde_json::json!({ "additionalContext": "first" })
                                            } else {
                                                serde_json::json!({ "additionalContext": "second" })
                                            };
                                            let response_params: Arc<serde_json::value::RawValue> =
                                                serde_json::value::to_raw_value(&value)
                                                    .unwrap()
                                                    .into();
                                            Ok(acp::ExtResponse::new(response_params))
                                        };
                                    let _ = args.response_tx.send(reply);
                                }
                            }
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let envelope = post_tool_use_envelope(&actor);
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_post_tool_use_client_hooks(&envelope),
            )
            .await
            .expect("the post_tool_use gate must not hang");

            let context: Vec<(&str, &str)> = result
                .additional_context
                .iter()
                .map(|c| (c.hook_name.as_str(), c.text.as_str()))
                .collect();
            assert_eq!(
                context,
                [("client:cb_ctx_a", "first"), ("client:cb_ctx_b", "second")]
            );

            let results: Vec<(&str, bool)> = result
                .results
                .iter()
                .map(|r| match r {
                    HookRunResult::Failed { hook_name, .. } => (hook_name.as_str(), true),
                    HookRunResult::Success { hook_name, .. } => (hook_name.as_str(), false),
                    _ => panic!("post_tool_use client results are only Success or Failed"),
                })
                .collect();
            assert_eq!(
                results,
                [
                    ("client:cb_ctx_a", false),
                    ("client:cb_fail", true),
                    ("client:cb_ctx_b", false),
                ]
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn post_tool_use_dispatch_merges_file_then_client_contributions() {
    use xai_grok_tools::types::output::{MCPOutput, ToolOutput, ToolRunResult};

    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (mut actor, gateway_rx, _persistence_rx) = test_actor().await;

            install_pre_tool_use_hooks(
                &mut actor,
                vec![post_tool_use_spec(
                    "file_hook",
                    None,
                    r#"echo '{"decision":"block","reason":"file block","hookSpecificOutput":{"additionalContext":"file context"}}'"#,
                )],
            );
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::PostToolUse,
                &["cb_client"],
            );

            spawn_run_responder(gateway_rx, |_| {
                serde_json::json!({
                    "decision": "block",
                    "reason": "client block",
                    "additionalContext": "client context",
                })
            });

            let run = ToolRunResult {
                output: ToolOutput::MCP(MCPOutput::okay_output(
                    "search".into(),
                    "memory".into(),
                    "original".into(),
                )),
                prompt_text: "original".into(),
                effective_tool_name: None,
            };
            let drained = DrainedToolSuccess::new(run);
            let prepared = PreparedToolCall {
        mcp_binding: None,
                mcp_request_meta: None,
                call_id: "call_1".to_string(),
                tool_call_id: acp::ToolCallId::new("call_1"),
                tool_name: "search__memory".to_string(),
                raw_arguments: "{}".to_string(),
                mcp_file: None,
                plan_file: None,
                parsed_args: serde_json::json!({}),
                model_id: Some("test-model".to_string()),
                invocation_id: "018f6b6c-7b3a-7c3a-8c3a-000000000001".to_string(),
                tool_id: "opaque".to_string(),
                tool_version: None,
                concatenated_json_count: 0,
                coercion_note: None,
                dispatch_target_name: None,
                is_read_only: false,
                is_edit: false,
                rewriting_hook: None,
                additional_context: Vec::new(),
            };

            let (delivery, _scrollback) = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.dispatch_post_tool_use_hook(&prepared, drained.output(), None),
            )
            .await
            .expect("the post_tool_use dispatch must not hang");

            let blocks: Vec<(&str, &str)> = delivery
                .blocks
                .iter()
                .map(|b| (b.hook_name.as_str(), b.reason.as_str()))
                .collect();
            assert_eq!(
                blocks,
                [
                    ("file_hook", "file block"),
                    ("client:cb_client", "client block"),
                ],
                "file block precedes the client block in the merged result"
            );

            let context: Vec<(&str, &str)> = delivery
                .additional_context
                .iter()
                .map(|c| (c.hook_name.as_str(), c.text.as_str()))
                .collect();
            assert_eq!(
                context,
                [
                    ("file_hook", "file context"),
                    ("client:cb_client", "client context"),
                ],
                "file context precedes the client context in the merged result"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn run_stop_gate_keep_working_and_cap() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;

            let decision = actor.run_stop_gate("prompt-1", 0).await;
            assert!(matches!(decision, StopGateDecision::AllowStop));

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_0"],
            );

            spawn_deny_responder(gateway_rx, "keep working");

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", 0),
            )
            .await
            .expect("the stop gate must not hang");
            match decision {
                StopGateDecision::KeepWorking { feedback } => {
                    assert!(
                        feedback.contains("Stop hook feedback:")
                            && feedback.contains("keep working"),
                        "feedback must carry the deny message, got: {feedback}"
                    );
                }
                _ => panic!("a client deny must keep the agent working"),
            }

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", MAX_STOP_HOOK_CONTINUATIONS_PER_TURN),
            )
            .await
            .expect("the capped gate must not hang");
            assert!(matches!(decision, StopGateDecision::AllowStop));
        })
        .await;
}

pub(super) fn file_registry_with_spec(
    event: xai_grok_hooks::event::HookEventName,
    script: &str,
) -> xai_grok_hooks::discovery::HookRegistry {
    file_registry(event, script, true)
}

fn file_registry(
    event: xai_grok_hooks::event::HookEventName,
    script: &str,
    enabled: bool,
) -> xai_grok_hooks::discovery::HookRegistry {
    let (mut registry, _) = xai_grok_hooks::discovery::load_hooks(None, None);
    registry.append_specs(vec![xai_grok_hooks::config::HookSpec {
        name: "test/stop-hook".into(),
        event,
        handler_type: xai_grok_hooks::config::HandlerType::Command,
        configured_matcher: None,
        matcher: None,
        enabled,
        command: Some(std::path::PathBuf::from(script)),
        command_raw: Some(script.to_string()),
        url: None,
        url_raw: None,
        timeout_ms: 5000,
        source_dir: std::path::PathBuf::from("/tmp"),
        extra_env: std::collections::HashMap::new(),
        layer: xai_grok_hooks::config::HookProvenance::File,
    }]);
    registry
}

/// A file-hook force-stop skips the client run gate (its signals would be discarded)
/// but still delivers the observe `x.ai/hooks/event` notification.
#[tokio::test(flavor = "current_thread")]
async fn file_force_stop_skips_client_gate_but_notifies() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (mut actor, mut gateway_rx, _persistence_rx) = test_actor().await;
            actor.hook_resolved_workspace_root = "/tmp".to_string();

            *actor.hook_registry.borrow_mut() = Some(std::sync::Arc::new(file_registry_with_spec(
                xai_grok_hooks::event::HookEventName::Stop,
                r#"echo '{"continue":false,"stopReason":"budget exhausted"}'"#,
            )));
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_observer"],
            );

            let run_requests = std::rc::Rc::new(std::cell::Cell::new(0u32));
            let observe_events = std::rc::Rc::new(std::cell::Cell::new(0u32));
            let (runs, observes) = (run_requests.clone(), observe_events.clone());
            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            if args.request.method.as_ref() == "x.ai/hooks/run" {
                                runs.set(runs.get() + 1);
                            }
                            let empty: Arc<serde_json::value::RawValue> =
                                serde_json::value::to_raw_value(&serde_json::json!({}))
                                    .unwrap()
                                    .into();
                            let _ = args.response_tx.send(Ok(acp::ExtResponse::new(empty)));
                        }
                        xai_acp_lib::AcpClientMessage::ExtNotification(args) => {
                            if args.request.method.as_ref() == "x.ai/hooks/event" {
                                observes.set(observes.get() + 1);
                            }
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", 0),
            )
            .await
            .expect("the stop gate must not hang");
            assert!(
                matches!(decision, StopGateDecision::AllowStop),
                "a file force-stop must end the turn"
            );
            // Yield so the fire-and-forget notification lands.
            tokio::task::yield_now().await;
            assert_eq!(run_requests.get(), 0, "the client run gate must be skipped");
            assert_eq!(
                observe_events.get(),
                1,
                "client callbacks must still see the turn end as an observe event"
            );
            // A forced stop still ran its hooks, so it spends the report: dropping the commit
            // here would let a cancel during teardown file a second one.
            assert!(actor.turn_report.claim_for_gate().is_none());
        })
        .await;
}

/// Two client callbacks both force-stop; attribution follows registration order even
/// when that callback responds last (completion order must not decide it).
#[tokio::test(flavor = "current_thread")]
async fn client_force_stop_attribution_is_registration_ordered() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_first", "cb_second"],
            );

            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            let params: serde_json::Value =
                                serde_json::from_str(args.request.params.get()).unwrap();
                            let is_first = params["hookCallbackId"] == "cb_first";
                            tokio::task::spawn_local(async move {
                                if is_first {
                                    // The registration-order winner replies last.
                                    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
                                }
                                let reason = if is_first {
                                    "from-first"
                                } else {
                                    "from-second"
                                };
                                let body: Arc<serde_json::value::RawValue> =
                                    serde_json::value::to_raw_value(&serde_json::json!({
                                        "continue": false,
                                        "stopReason": reason,
                                    }))
                                    .unwrap()
                                    .into();
                                let _ = args.response_tx.send(Ok(acp::ExtResponse::new(body)));
                            });
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let envelope = actor.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::Stop,
                Some("prompt-1".to_string()),
                xai_grok_hooks::event::HookPayload::Stop {
                    reason: "end_turn".to_string(),
                    stop_hook_active: false,
                    last_assistant_message: None,
                    background_tasks: None,
                    session_crons: None,
                },
            );
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_client_hooks(&envelope),
            )
            .await
            .expect("the stop gate must not hang");

            let prevent = result.prevent_continuation.expect("force-stop captured");
            assert_eq!(
                prevent.hook_name, "client:cb_first",
                "attribution must follow registration order, not completion order"
            );
            assert_eq!(prevent.reason, "from-first");
        })
        .await;
}

/// A subagent session gates on `SubagentStop` specs (not `Stop`), with the gate-phase
/// payload.
#[tokio::test(flavor = "current_thread")]
async fn subagent_session_gates_on_subagent_stop() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (mut actor, mut gateway_rx, _persistence_rx) = test_actor().await;
            actor.startup_hints.is_subagent = true;
            actor.hook_resolved_workspace_root = "/tmp".to_string();

            *actor.hook_registry.borrow_mut() = Some(std::sync::Arc::new(file_registry_with_spec(
                xai_grok_hooks::event::HookEventName::SubagentStop,
                r#"echo '{"decision":"block","reason":"verify the summary"}'"#,
            )));

            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    if let xai_acp_lib::AcpClientMessage::SessionNotification(args) = msg {
                        let _ = args.response_tx.send(Ok(()));
                    }
                }
            });

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", 0),
            )
            .await
            .expect("the subagent stop gate must not hang");
            match decision {
                StopGateDecision::KeepWorking { feedback } => {
                    assert!(
                        feedback.contains("verify the summary"),
                        "the SubagentStop block reason must become feedback, got: {feedback}"
                    );
                }
                other => {
                    panic!("a SubagentStop block must keep the subagent working, got {other:?}")
                }
            }
        })
        .await;
}

/// Alias fire sites serialize the canonical event name: a `SubagentEnd` envelope reads
/// `"subagent_stop"` on the wire, matching `GROK_HOOK_EVENT`.
#[tokio::test(flavor = "current_thread")]
async fn alias_envelope_serializes_canonical_event_name() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _gateway_rx, _persistence_rx) = test_actor().await;

            let envelope = actor.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::SubagentEnd,
                None,
                xai_grok_hooks::event::HookPayload::SubagentStop {
                    phase: xai_grok_hooks::event::SubagentStopPhase::Observe,
                    subagent_id: "sub-1".into(),
                    subagent_type: "explore".into(),
                    stop_hook_active: None,
                    last_assistant_message: None,
                },
            );
            let value = serde_json::to_value(&envelope).expect("envelope serializes");
            assert_eq!(value["hookEventName"], "subagent_stop");
            // The test actor runs yolo, so permissionMode pins that state.
            assert_eq!(value["permissionMode"], "bypassPermissions");
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn agent_swarm_batch_rejects_mixed_calls_and_records_error_for_each_tool() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (gateway_tx, _gateway_rx) =
                tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpClientMessage>();
            let (persistence_tx, _persistence_rx) =
                tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
            let actor = create_test_actor(0, 256_000, 85, gateway_tx, persistence_tx).await;

            let calls = vec![
                batch_call(
                    "call_swarm",
                    "agent_swarm",
                    r#"{"description":"parallel checks","subagent_type":"general-purpose","prompt_template":"{{item}}","items":["a","b"]}"#,
                ),
                batch_call("call_bash", "run_terminal_command", r#"{"command":"echo done"}"#),
            ];

            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.execute_tool_calls(calls, None),
            )
            .await
            .expect("execute_tool_calls should not hang")
            .expect("execute_tool_calls must return a ToolLoop");

            assert!(matches!(result, ToolLoop::Continue));

            let conv = actor.chat_state_handle.get_conversation().await;
            assert_eq!(
                conv.iter()
                    .filter(|item| matches!(item, ConversationItem::ToolResult(_)))
                    .count(),
                2,
                "an exclusivity failure must post one error tool_result per call",
            );

            let swarm_result = tool_result_for_call(&conv, "call_swarm")
                .expect("agent_swarm call should have a result entry with exclusivity error");
            let bash_result = tool_result_for_call(&conv, "call_bash")
                .expect("non-agent_swarm call should also have an exclusivity result");
            assert!(
                swarm_result.contains(AGENT_SWARM_EXCLUSIVITY_ERROR),
                "swarm batch violation must cite the exclusivity message: {swarm_result}"
            );
            assert!(
                bash_result.contains(AGENT_SWARM_EXCLUSIVITY_ERROR),
                "swarm batch violation must cite the exclusivity message for every call: {bash_result}"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn agent_swarm_parent_bypasses_permission_and_child_tool_still_prompts() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (gateway_tx, _gateway_rx) =
                tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpClientMessage>();
            let (persistence_tx, _persistence_rx) =
                tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
            let mut actor =
                create_test_actor(0, 256_000, 85, gateway_tx, persistence_tx).await;
            let mut bash = xai_grok_tools::registry::types::ToolConfig::for_tool::<
                xai_grok_tools::implementations::grok_build::bash::BashTool,
            >();
            bash.params = Some(
                serde_json::json!({ "enabled_background": false })
                    .as_object()
                    .expect("bash params object")
                    .clone(),
            );
            *actor.agent.borrow_mut() = test_agent_with_tools(vec![
                xai_grok_tools::registry::types::ToolConfig::for_tool::<
                    xai_grok_tools::implementations::grok_build::task_output::TaskOutputTool,
                >(),
                xai_grok_tools::registry::types::ToolConfig::for_tool::<
                    xai_grok_tools::implementations::grok_build::kill_task::KillTaskTool,
                >(),
                xai_grok_tools::registry::types::ToolConfig::for_tool::<
                    xai_grok_tools::implementations::grok_build::task::TaskTool,
                >(),
                xai_grok_tools::registry::types::ToolConfig::for_tool::<
                    xai_grok_tools::implementations::grok_build::agent_swarm::AgentSwarmTool,
                >(),
                bash,
            ])
            .await;

            let permission_prompts = install_recording_permissions(&mut actor);

            let swarm_call = batch_call(
                "parent_swarm",
                "agent_swarm",
                r#"{"description":"parallel work","subagent_type":"general-purpose","prompt_template":"{{item}}","items":["first","second"]}"#,
            );
            let mut deferred = Vec::new();
            let parent_result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.prepare_tool_call(swarm_call, &mut deferred, None),
            )
            .await
            .expect("prepare_tool_call should not hang")
            .expect("prepare_tool_call should not error");

            assert!(parent_result.is_ok(), "agent_swarm parent call should prepare successfully");
            assert!(
                permission_prompts.borrow().is_empty(),
                "single parent agent_swarm call should bypass permission check",
            );

            let child_call = batch_call(
                "child_bash",
                "run_terminal_cmd",
                r#"{"command":"rm -rf /","description":"destructive test"}"#,
            );
            let mut child_deferred = Vec::new();
            let child_result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.prepare_tool_call(child_call, &mut child_deferred, None),
            )
            .await
            .expect("prepare_tool_call should not hang")
            .expect("prepare_tool_call should return a ToolLoop decision");

            assert!(
                matches!(child_result, Err(ToolLoop::PermissionReject { .. })),
                "child non-swarm tool should still be permission-checked; got {child_result:?}",
            );
            assert_eq!(
                permission_prompts.borrow().len(),
                1,
                "exactly one permission prompt should occur for the child call",
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn client_force_stop_reports_the_turn() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_stop"],
            );

            spawn_run_responder(
                gateway_rx,
                |_| serde_json::json!({ "continue": false, "stopReason": "budget" }),
            );

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", 0),
            )
            .await
            .expect("the stop gate must not hang");
            assert!(matches!(decision, StopGateDecision::AllowStop));
            assert!(actor.turn_report.claim_for_gate().is_none());
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn an_unanswered_client_gate_leaves_the_report_unspent() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, mut gateway_rx, _persistence_rx) = test_actor().await;
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_stop"],
            );

            // Drop the response channel unanswered: a transport error, not a decision.
            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    if let xai_acp_lib::AcpClientMessage::ExtMethod(args) = msg {
                        drop(args.response_tx);
                    }
                }
            });

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", 0),
            )
            .await
            .expect("the stop gate must not hang");

            assert!(matches!(decision, StopGateDecision::AllowStop));
            assert!(
                actor.turn_report.claim_for_gate().is_some(),
                "a gate whose only hook never answered must leave the turn reportable"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn a_reported_turn_skips_the_gate_entirely() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (mut actor, _gateway_rx, _persistence_rx) = test_actor().await;
            actor.hook_resolved_workspace_root = "/tmp".to_string();
            *actor.hook_registry.borrow_mut() = Some(std::sync::Arc::new(file_registry_with_spec(
                xai_grok_hooks::event::HookEventName::Stop,
                "exit 2",
            )));

            let claim = actor
                .turn_report
                .claim_for_gate()
                .expect("the turn is fresh");
            assert_eq!(
                claim.commit(),
                super::turn_report_slot::CommitOutcome::Reported,
                "an interrupt reported this turn"
            );

            let decision = actor.run_stop_gate("prompt-1", 0).await;
            assert!(
                matches!(decision, StopGateDecision::AllowStop),
                "a hook that ran would have blocked the stop"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn only_a_completed_stop_hook_is_the_turns_report() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (mut actor, _gateway_rx, _persistence_rx) = test_actor().await;
            actor.hook_resolved_workspace_root = "/tmp".to_string();
            // A report only counts once it reaches the queue, so the turn needs one.
            let actor = std::sync::Arc::new(actor);
            let _queue = super::turn_end_hooks::TurnEndQueue::spawn(actor.clone());
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::StopFailure,
                &["cb_fail"],
            );
            // Registered with nothing to deliver, so only the file hook can produce a result.
            actor.client_hooks.borrow_mut().insert(
                xai_grok_hooks::event::HookEventName::Stop,
                vec![crate::extensions::hooks::ClientHookGroup {
                    matcher: None,
                    callback_ids: vec![],
                    timeout: None,
                }],
            );

            use super::turn_end_hooks::ReportOutcome;
            for (case, script, enabled, reports_later) in [
                (
                    "disabled, so skipped",
                    "exit 0",
                    false,
                    ReportOutcome::Queued,
                ),
                ("crashed", "exit 1", true, ReportOutcome::Queued),
                ("ran", "exit 0", true, ReportOutcome::AlreadyReported),
            ] {
                actor.turn_report.start_next_turn();
                *actor.hook_registry.borrow_mut() = Some(std::sync::Arc::new(file_registry(
                    xai_grok_hooks::event::HookEventName::Stop,
                    script,
                    enabled,
                )));

                let decision = actor.run_stop_gate(case, 0).await;
                assert!(matches!(decision, StopGateDecision::AllowStop), "{case}");
                let reported = actor.claim_and_queue(
                    case,
                    actor.turn_report.epoch(),
                    super::turn_end_hooks::TurnEnd::Failed {
                        error: xai_grok_hooks::event::StopFailureKind::Unknown,
                        error_details: None,
                        last_assistant_message: None,
                    },
                );
                assert_eq!(reported, reports_later, "{case}");
            }
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn a_gate_that_keeps_working_releases_the_report() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, gateway_rx, _persistence_rx) = test_actor().await;
            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_block"],
            );
            spawn_deny_responder(gateway_rx, "keep working");

            let decision = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_gate("prompt-1", 0),
            )
            .await
            .expect("the stop gate must not hang");
            assert!(matches!(decision, StopGateDecision::KeepWorking { .. }));
            assert!(
                actor.turn_report.claim_for_gate().is_some(),
                "a gate that kept the agent working must leave the report slot free"
            );
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn stop_client_gate_collects_denies() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (gateway_tx, mut gateway_rx) =
                tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpClientMessage>();
            let (persistence_tx, _persistence_rx) =
                tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
            let actor = create_test_actor(0, 256_000, 85, gateway_tx, persistence_tx).await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_block", "cb_allow"],
            );

            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            let params: serde_json::Value =
                                serde_json::from_str(args.request.params.get()).unwrap();
                            let response = if params["hookCallbackId"] == "cb_block" {
                                serde_json::json!({
                                    "decision": "deny",
                                    "systemMessage": "finish the tests first",
                                })
                            } else {
                                serde_json::json!({})
                            };
                            let response_params: Arc<serde_json::value::RawValue> =
                                serde_json::value::to_raw_value(&response).unwrap().into();
                            let _ = args
                                .response_tx
                                .send(Ok(acp::ExtResponse::new(response_params)));
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let envelope = actor.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::Stop,
                Some("prompt-1".to_string()),
                xai_grok_hooks::event::HookPayload::Stop {
                    reason: "end_turn".to_string(),
                    stop_hook_active: true,
                    last_assistant_message: Some("I'm done".to_string()),
                    background_tasks: None,
                    session_crons: None,
                },
            );
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_client_hooks(&envelope),
            )
            .await
            .expect("the stop gate must not hang");

            assert_eq!(result.blocks.len(), 1, "only the denying callback blocks");
            assert_eq!(result.blocks[0].hook_name, "client:cb_block");
            assert_eq!(result.blocks[0].reason, "finish the tests first");
            assert!(result.prevent_continuation.is_none());
            assert!(result.additional_context.is_empty());
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn stop_client_gate_carries_continue_false_and_context() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (gateway_tx, mut gateway_rx) =
                tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpClientMessage>();
            let (persistence_tx, _persistence_rx) =
                tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
            let actor = create_test_actor(0, 256_000, 85, gateway_tx, persistence_tx).await;

            install_client_hook(
                &actor,
                xai_grok_hooks::event::HookEventName::Stop,
                &["cb_stop", "cb_ctx"],
            );

            tokio::task::spawn_local(async move {
                while let Some(msg) = gateway_rx.recv().await {
                    match msg {
                        xai_acp_lib::AcpClientMessage::ExtMethod(args) => {
                            let params: serde_json::Value =
                                serde_json::from_str(args.request.params.get()).unwrap();
                            let response = if params["hookCallbackId"] == "cb_stop" {
                                serde_json::json!({ "continue": false, "stopReason": "budget" })
                            } else {
                                serde_json::json!({ "additionalContext": "run the linter" })
                            };
                            let response_params: Arc<serde_json::value::RawValue> =
                                serde_json::value::to_raw_value(&response).unwrap().into();
                            let _ = args
                                .response_tx
                                .send(Ok(acp::ExtResponse::new(response_params)));
                        }
                        xai_acp_lib::AcpClientMessage::SessionNotification(args) => {
                            let _ = args.response_tx.send(Ok(()));
                        }
                        _ => {}
                    }
                }
            });

            let envelope = actor.make_hook_envelope(
                xai_grok_hooks::event::HookEventName::Stop,
                Some("prompt-1".to_string()),
                xai_grok_hooks::event::HookPayload::Stop {
                    reason: "end_turn".to_string(),
                    stop_hook_active: false,
                    last_assistant_message: None,
                    background_tasks: None,
                    session_crons: None,
                },
            );
            let result = tokio::time::timeout(
                std::time::Duration::from_secs(5),
                actor.run_stop_client_hooks(&envelope),
            )
            .await
            .expect("the stop gate must not hang");

            assert!(result.blocks.is_empty());
            let prevent = result
                .prevent_continuation
                .expect("continue:false captured");
            assert_eq!(prevent.hook_name, "client:cb_stop");
            assert_eq!(prevent.reason, "budget");
            assert_eq!(result.additional_context, ["run the linter"]);
        })
        .await;
}

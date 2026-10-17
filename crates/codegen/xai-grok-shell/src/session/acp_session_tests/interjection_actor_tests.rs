//! Mid-turn interjection images: queue-row harvest and the
//! `drain_pending_interjections` image pipeline.
use super::support::*;
use super::*;

#[tokio::test]
async fn unprefixed_child_prompt_id_keeps_explicit_model_origin_in_queue() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let (actor, _) = build_actor().await;
            let (respond_to, _) = tokio::sync::oneshot::channel();
            let origin = crate::session::PromptOrigin::ParentAgentMessage {
                message_id: "original-message".into(),
                sender_session_id: "parent".into(),
            };
            let _ = actor
                .queue_input_with_origin(
                    queue_input_request(
                        vec![acp::ContentBlock::Text(acp::TextContent::new(
                            "/always-approve on",
                        ))],
                        "unprefixed-uuid",
                        respond_to,
                    ),
                    origin.clone(),
                )
                .await;
            let state = actor.state.lock().await;
            let item = state.pending_inputs.front().unwrap();
            assert_eq!(item.prompt_id, "unprefixed-uuid");
            assert_eq!(item.origin, origin);
            assert_eq!(
                item.origin.policy().authority,
                crate::session::InputAuthority::ModelAuthoredUntrusted
            );
        })
        .await;
}

#[test]
fn stop_preserves_agent_mail_and_holds_it_until_reengagement() {
    run_on_session_sized_stack(|| {
        Box::pin(async {
            for trigger in [Some(crate::session::CancelTrigger::CtrlC), None] {
                let (actor, _) = build_actor().await;
                {
                    let mut state = actor.state.lock().await;
                    state
                        .pending_inputs
                        .push_back(user_item("running", "owner"));
                    state.running_task = Some(running_task_stub("running"));
                }
                for (origin, text) in [
                    (
                        crate::session::PromptOrigin::AgentMessage {
                            message_id: "agent".into(),
                        },
                        "agent",
                    ),
                    (crate::session::PromptOrigin::User, "human"),
                    (
                        crate::session::PromptOrigin::PeerSessionMessage {
                            message_id: "peer".into(),
                        },
                        "peer",
                    ),
                ] {
                    actor.pending_interjections.push(PendingInterjection {
                        text: text.into(),
                        attachments: vec![],
                        origin,
                    });
                }
                let outcome = actor
                    .cancel_running_task(crate::session::CancelOptions {
                        trigger,
                        user_initiated: true,
                        ..Default::default()
                    })
                    .await;
                assert!(outcome.settled);
                assert!(actor.pending_interjections.is_empty());
                let (completion_tx, _) = tokio::sync::mpsc::unbounded_channel();
                actor
                    .clone()
                    .maybe_start_running_task(completion_tx.clone())
                    .await;
                {
                    let state = actor.state.lock().await;
                    assert!(state.notifications_suppressed);
                    assert!(state.running_task.is_none());
                    assert_eq!(
                        state
                            .pending_inputs
                            .iter()
                            .map(|item| item.prompt_id.as_str())
                            .collect::<Vec<_>>(),
                        ["agent-message-agent", "peer-message-peer"]
                    );
                }
                // Prompt intake clears the barrier when the human re-engages.
                actor.state.lock().await.notifications_suppressed = false;
                actor.clone().maybe_start_running_task(completion_tx).await;
                let mut state = actor.state.lock().await;
                assert_eq!(state.running_prompt_id(), Some("agent-message-agent"));
                state.running_task.take().unwrap().abort();
            }
        })
    });
}

#[test]
fn pristine_rewind_holds_buffered_agent_mail_until_reengagement() {
    run_on_session_sized_stack(|| {
        Box::pin(async {
            let (actor, _) = build_actor().await;
            let (input, response) = user_item_with_rx("running", "owner");
            {
                let mut state = actor.state.lock().await;
                state.pending_inputs.push_back(input);
                state.running_task = Some(running_task_stub("running"));
                state.rewindable = true;
            }
            actor.pending_interjections.push(PendingInterjection {
                text: "mail".into(),
                attachments: vec![],
                origin: crate::session::PromptOrigin::AgentMessage {
                    message_id: "m".into(),
                },
            });
            let outcome = actor
                .cancel_running_task(crate::session::CancelOptions {
                    history: crate::session::commands::CancelHistoryDisposition::RewindIfNoOutput {
                        prompt_id: Some("running".into()),
                    },
                    trigger: Some(crate::session::CancelTrigger::Esc),
                    user_initiated: true,
                    ..Default::default()
                })
                .await;
            assert!(outcome.settled);
            assert!(matches!(
                response.await.unwrap().unwrap().completion_kind,
                PromptCompletionKind::Rewound
            ));
            assert!(actor.pending_interjections.is_empty());
            let (completion_tx, _) = tokio::sync::mpsc::unbounded_channel();
            actor
                .clone()
                .maybe_start_running_task(completion_tx.clone())
                .await;
            {
                let state = actor.state.lock().await;
                assert!(state.notifications_suppressed);
                assert!(state.running_task.is_none());
                assert_eq!(
                    state.pending_inputs.front().unwrap().prompt_id,
                    "agent-message-m"
                );
            }
            // The real prompt-intake handler clears this barrier on user re-engagement.
            actor.state.lock().await.notifications_suppressed = false;
            actor.clone().maybe_start_running_task(completion_tx).await;
            let mut state = actor.state.lock().await;
            assert_eq!(state.running_prompt_id(), Some("agent-message-m"));
            state.running_task.take().unwrap().abort();
        })
    });
}

#[test]
fn real_user_prompt_releases_stopped_mail_barrier() {
    run_on_session_sized_stack(|| {
        Box::pin(async {
            let (gateway_tx, mut gateway_rx) = tokio::sync::mpsc::unbounded_channel();
            let (persistence_tx, _) = tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
            let (actor, events) =
                create_test_actor_ex(0, 256_000, 85, gateway_tx, persistence_tx).await;
            let actor = Arc::new(actor);
            {
                let mut state = actor.state.lock().await;
                state.notifications_suppressed = true;
                let mut mail = user_item("agent-message-m", "owner");
                mail.origin = crate::session::PromptOrigin::AgentMessage {
                    message_id: "m".into(),
                };
                state.pending_inputs.push_back(mail);
            }
            tokio::task::spawn_local(async move {
                while let Some(message) = gateway_rx.recv().await {
                    if let xai_acp_lib::AcpClientMessage::SessionNotification(args) = message {
                        let _ = args.response_tx.send(Ok(()));
                    }
                }
            });
            let (cmd_tx, cmd_rx) = tokio::sync::mpsc::unbounded_channel();
            let (_chat_tx, chat_rx) = tokio::sync::mpsc::unbounded_channel();
            let loop_handle = tokio::task::spawn_local(super::run_session(
                actor.clone(),
                cmd_rx,
                chat_rx,
                events,
                None,
                Arc::new(parking_lot::Mutex::new(
                    xai_grok_workspace::file_system::CodebaseIndexManager::new(),
                )),
                std::path::PathBuf::from("/tmp"),
                crate::session::fs_watch::FsWatchCapabilities::none(),
            ));
            let (respond_to, _response) = tokio::sync::oneshot::channel();
            cmd_tx
                .send(SessionCommand::Prompt {
                    prompt_id: "human".into(),
                    origin: None,
                    prompt_blocks: vec![acp::ContentBlock::Text(acp::TextContent::new("continue"))],
                    prompt_mode: PromptMode::Agent,
                    artifact_upload_ctx: None,
                    client_identifier: None,
                    screen_mode: None,
                    verbatim: false,
                    traceparent: None,
                    json_schema: None,
                    send_now: false,
                    admission: None,
                    tool_overrides_update: None,
                    respond_to,
                    persist_ack: None,
                    parsed_prompt_tx: None,
                    initial_child_prompt_ready: None,
                })
                .unwrap();
            let (respond_to, response) = tokio::sync::oneshot::channel();
            cmd_tx
                .send(SessionCommand::GetToolOverrides { respond_to })
                .unwrap();
            let _ = tokio::time::timeout(std::time::Duration::from_secs(5), response)
                .await
                .unwrap()
                .unwrap();
            let mut state = actor.state.lock().await;
            assert!(!state.notifications_suppressed);
            if let Some(task) = state.running_task.take() {
                task.abort();
            }
            loop_handle.abort();
        })
    });
}

#[test]
fn stale_targeted_cancel_leaves_the_new_turns_interjections_intact() {
    run_on_session_sized_stack(|| {
        Box::pin(async {
            let (actor, _) = build_actor().await;
            {
                let mut state = actor.state.lock().await;
                state.pending_inputs.push_back(user_item("new", "owner"));
                state.running_task = Some(running_task_stub("new"));
            }
            actor.pending_interjections.push(PendingInterjection {
                text: "mail".into(),
                attachments: vec![],
                origin: crate::session::PromptOrigin::AgentMessage {
                    message_id: "m".into(),
                },
            });
            let outcome = actor
                .cancel_running_task(crate::session::CancelOptions {
                    history: crate::session::commands::CancelHistoryDisposition::RewindIfNoOutput {
                        prompt_id: Some("old".into()),
                    },
                    trigger: Some(crate::session::CancelTrigger::Esc),
                    user_initiated: true,
                    ..Default::default()
                })
                .await;
            assert!(!outcome.settled);
            assert_eq!(actor.pending_interjections.snapshot().len(), 1);
            let mut state = actor.state.lock().await;
            assert_eq!(state.running_prompt_id(), Some("new"));
            state.running_task.take().unwrap().abort();
        })
    });
}

#[test]
fn finalizing_turn_keeps_its_front_when_agent_mail_arrives() {
    run_on_session_sized_stack(|| {
        Box::pin(async {
            use xai_grok_tools::implementations::grok_build::task::types::{
                AgentMailboxMessage, AgentMailboxMessageKind, NativeAgentMessage,
            };
            let (gateway_tx, mut gateway_rx) = tokio::sync::mpsc::unbounded_channel();
            let (persistence_tx, _) = tokio::sync::mpsc::unbounded_channel::<PersistenceMsg>();
            let (actor, events) =
                create_test_actor_ex(0, 256_000, 85, gateway_tx, persistence_tx).await;
            let actor = Arc::new(actor);
            {
                let mut state = actor.state.lock().await;
                state
                    .pending_inputs
                    .push_back(user_item("running", "owner"));
                state.running_task = Some(running_task_stub("running"));
            }
            // The turn scope guard has cleared its prompt pin, but completion
            // bookkeeping still owns the task slot and its front row.
            assert!(actor.current_prompt_id.lock().unwrap().is_none());
            tokio::task::spawn_local(async move {
                while let Some(message) = gateway_rx.recv().await {
                    if let xai_acp_lib::AcpClientMessage::SessionNotification(args) = message {
                        let _ = args.response_tx.send(Ok(()));
                    }
                }
            });
            let (cmd_tx, cmd_rx) = tokio::sync::mpsc::unbounded_channel();
            let (_chat_tx, chat_rx) = tokio::sync::mpsc::unbounded_channel();
            let loop_handle = tokio::task::spawn_local(super::run_session(
                actor.clone(),
                cmd_rx,
                chat_rx,
                events,
                None,
                Arc::new(parking_lot::Mutex::new(
                    xai_grok_workspace::file_system::CodebaseIndexManager::new(),
                )),
                std::path::PathBuf::from("/tmp"),
                crate::session::fs_watch::FsWatchCapabilities::none(),
            ));
            let mut message = AgentMailboxMessage {
                message_id: "team".into(),
                team_scope_id: "root".into(),
                from_agent_id: "worker".into(),
                to_agent_id: "root".into(),
                kind: AgentMailboxMessageKind::Message,
                body: "team mail".into(),
                created_at_ms: 0,
                native: None,
            };
            cmd_tx
                .send(SessionCommand::AgentMessage {
                    message: message.clone(),
                })
                .unwrap();
            cmd_tx
                .send(SessionCommand::PeerSessionMessage {
                    message: crate::session_bus::PeerSessionMessage {
                        message_id: "peer".into(),
                        target_session: "root".into(),
                        source_session: "other".into(),
                        source_project: "project".into(),
                        body: "peer mail".into(),
                    },
                })
                .unwrap();
            message.message_id = "native".into();
            message.kind = AgentMailboxMessageKind::NativeFollowup;
            message.native = Some(NativeAgentMessage {
                author: "worker".into(),
                recipient: "root".into(),
                encrypted: false,
                trigger_prompt_id: None,
            });
            cmd_tx
                .send(SessionCommand::AgentMessage { message })
                .unwrap();
            let (respond_to, response) = tokio::sync::oneshot::channel();
            cmd_tx
                .send(SessionCommand::GetToolOverrides { respond_to })
                .unwrap();
            tokio::time::timeout(std::time::Duration::from_secs(5), response)
                .await
                .unwrap()
                .unwrap();
            let entries = actor.pending_interjections.snapshot();
            assert!(entries.iter().any(|entry| matches!(&entry.origin, crate::session::PromptOrigin::AgentMessage { message_id } if message_id == "team")));
            assert!(entries.iter().any(|entry| matches!(&entry.origin, crate::session::PromptOrigin::PeerSessionMessage { message_id } if message_id == "peer")));
            assert!(entries.iter().any(|entry| matches!(&entry.origin, crate::session::PromptOrigin::AgentMessage { message_id } if message_id == "native")));
            assert_eq!(actor.pending_native_agent_messages.lock().len(), 1);
            let mut state = actor.state.lock().await;
            assert_eq!(state.pending_inputs.front().unwrap().prompt_id, "running");
            assert_eq!(state.running_prompt_id(), Some("running"));
            state.running_task.take().unwrap().abort();
            loop_handle.abort();
        })
    });
}

#[tokio::test]
async fn stranded_model_messages_keep_origin_and_fifo() {
    tokio::task::LocalSet::new().run_until(async {
        let (actor, _) = build_actor().await;
        for (origin, text) in [
            (crate::session::PromptOrigin::AgentMessage { message_id: "team".into() }, "team"),
            (crate::session::PromptOrigin::User, "human"),
            (crate::session::PromptOrigin::PeerSessionMessage { message_id: "peer".into() }, "peer"),
        ] {
            actor.pending_interjections.push(PendingInterjection { text: text.into(), attachments: vec![], origin });
        }
        assert_eq!(actor.flush_stranded_interjections().await, 3);
        let state = actor.state.lock().await;
        let rows: Vec<_> = state.pending_inputs.iter().collect();
        assert_eq!(rows.len(), 3);
        assert!(matches!(&rows[0].origin, crate::session::PromptOrigin::AgentMessage { message_id } if message_id == "team"));
        assert!(matches!(&rows[1].origin, crate::session::PromptOrigin::User));
        assert!(matches!(&rows[2].origin, crate::session::PromptOrigin::PeerSessionMessage { message_id } if message_id == "peer"));
        assert!(!rows[0].is_queue_editable() && !rows[2].is_queue_editable());
        assert_eq!(rows[0].prompt_id, "agent-message-team");
        assert_eq!(rows[2].prompt_id, "peer-message-peer");
    }).await;
}

/// Send-now of an image-bearing queued prompt keeps its `ContentBlock::Image`s on the promoted row.
#[tokio::test]
async fn queue_send_now_keeps_prompt_block_images_on_promoted_row() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            {
                let mut state = actor.state.lock().await;
                state.pending_inputs.push_back(user_item("running", "A"));
                state.running_task = Some(running_task_stub("running"));
                state.front_message_committed = true;
                let mut item = user_item("p1", "A");
                item.prompt_blocks
                    .push(acp::ContentBlock::Image(test_image_content()));
                state.pending_inputs.push_back(item);
            }
            *actor
                .current_prompt_id
                .lock()
                .expect("current_prompt_id mutex poisoned") = Some("running".into());

            let cancel = actor
                .handle_interject_queued_prompt("p1", 0, None, None)
                .await;
            assert!(cancel, "promotion behind a running turn requests cancel");

            let state = actor.state.lock().await;
            let promoted = state
                .pending_inputs
                .iter()
                .find(|i| i.prompt_id == "p1")
                .expect("promoted row stays queued to run next");
            assert_eq!(
                promoted
                    .prompt_blocks
                    .iter()
                    .filter(|b| matches!(b, acp::ContentBlock::Image(_)))
                    .count(),
                1,
                "image blocks must survive promotion"
            );
            assert!(
                actor.pending_interjections.is_empty(),
                "send-now never buffers into the running turn"
            );
        })
        .await;
}

#[tokio::test]
async fn goal_send_now_routes_text_and_image_as_planner_steering_and_interjection() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            {
                let mut state = actor.state.lock().await;
                state.pending_inputs.push_back(user_item("running", "A"));
                state.running_task = Some(running_task_stub("running"));
            }
            actor.goal_tracker.lock().create_goal(
                "goal".into(),
                "objective".into(),
                None,
                0,
                "2026-01-01T00:00:00Z".into(),
                None,
            );
            let cancel = tokio_util::sync::CancellationToken::new();
            actor.goal_tracker.lock().start_planner_run(cancel.clone());

            let (respond_to, response_rx) = tokio::sync::oneshot::channel();
            let cancelled = actor
                .queue_input(QueueInputRequest {
                    send_now: true,
                    ..queue_input_request(
                        vec![
                            acp::ContentBlock::Text(acp::TextContent::new("steer")),
                            acp::ContentBlock::Image(test_image_content()),
                        ],
                        "steer-image",
                        respond_to,
                    )
                })
                .await;

            assert!(!cancelled);
            assert!(cancel.is_cancelled());
            assert!(matches!(
                response_rx.await.unwrap().unwrap().completion_kind,
                PromptCompletionKind::RemovedFromQueue
            ));
            let run = actor.goal_tracker.lock().take_planner_run().unwrap();
            assert_eq!(run.steering, ["steer"]);
            let interjections = actor.pending_interjections.drain_all();
            assert_eq!(interjections.len(), 1);
            assert_eq!(interjections[0].text, "steer");
            assert_eq!(interjections[0].attachments.len(), 1);
        })
        .await;
}

/// Draining an image-bearing interjection injects structured
/// `ContentPart::Image` parts (base64 data URL) on the synthetic user
/// message, preserving `SyntheticReason::Interjection`.
#[tokio::test]
async fn drain_interjection_with_images_attaches_image_parts() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _gateway_rx) = build_actor().await;
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "look at [Image #1]".to_string(),
                attachments: vec![test_image_content()],
            });

            assert!(actor.drain_pending_interjections().await);

            let conversation = actor.chat_state_handle.get_conversation().await;
            let user_item = match conversation.last() {
                Some(ConversationItem::User(u)) => u,
                other => panic!("conversation tail must be a user item, got: {other:?}"),
            };
            assert_eq!(
                user_item.synthetic_reason,
                Some(SyntheticReason::Interjection)
            );
            let image_urls: Vec<&str> = user_item
                .content
                .iter()
                .filter_map(|p| match p {
                    xai_grok_sampling_types::ContentPart::Image { url } => Some(url.as_ref()),
                    _ => None,
                })
                .collect();
            assert_eq!(image_urls.len(), 1, "image part must be attached");
            assert!(
                image_urls[0].starts_with("data:image/"),
                "inline base64 data URL expected, got {}",
                &image_urls[0][..image_urls[0].len().min(32)]
            );
            let text = conversation.last().unwrap().text_content();
            assert!(
                text.contains("[Image #1]") && text.contains("<user_query>"),
                "placeholder text must survive in the wrapped query, got: {text}"
            );
        })
        .await;
}

/// A grok-build interjection persists its images under the session `assets/` dir and leads with the
/// `<image_files>` block, like a prompt turn, so compaction can later list the paths.
#[tokio::test]
async fn grok_build_interjection_with_images_gets_image_files_block() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _gateway_rx) = build_actor().await;
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "look at [Image #1]".to_string(),
                attachments: vec![test_image_content()],
            });

            assert!(actor.drain_pending_interjections().await);

            let conversation = actor.chat_state_handle.get_conversation().await;
            let user_item = match conversation.last() {
                Some(ConversationItem::User(u)) => u,
                other => panic!("conversation tail must be a user item, got: {other:?}"),
            };
            assert_eq!(
                user_item.synthetic_reason,
                Some(SyntheticReason::Interjection)
            );
            let text = conversation.last().unwrap().text_content();
            assert!(text.starts_with("<image_files>\n"), "got: {text}");
            let path = text
                .lines()
                .find_map(|line| line.strip_prefix("1. "))
                .unwrap_or_else(|| panic!("no persisted path line, got: {text}"));
            assert!(
                path.contains("/assets/image-") && std::path::Path::new(path).is_file(),
                "path must point at the persisted asset, got: {path}"
            );
            assert!(
                text.find("</image_files>") < text.find("<user_query>"),
                "block must precede the wrapped query, got: {text}"
            );
            std::fs::remove_file(path).unwrap();
        })
        .await;
}

/// The drain strips `[Image #N: <path>]` down to `[Image #N]` before the text reaches the model, the same gate as the prompt path.
/// Covers raw text from legacy clients and text harvested from a queued row (raw `queue_meta.text`).
#[tokio::test]
async fn drain_interjection_strips_placeholder_paths_from_text() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _gateway_rx) = build_actor().await;
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "look at [Image #1: /tmp/secret/x.png] please".to_string(),
                attachments: vec![test_image_content()],
            });

            assert!(actor.drain_pending_interjections().await);

            let conversation = actor.chat_state_handle.get_conversation().await;
            let text = conversation.last().expect("user item").text_content();
            assert!(
                text.contains("[Image #1]"),
                "bare placeholder must survive, got: {text}"
            );
            assert!(
                !text.contains("/tmp/secret/x.png"),
                "path must be stripped from the model-visible text, got: {text}"
            );
        })
        .await;
}

/// Draining an interjection whose text is a skill slash invocation appends
/// the loaded `<skill_information>` envelope after the wrapped
/// `<user_query>` — send-now of a queued `/skill` row (and a typed `/skill`
/// interjection) must not reach the model unexpanded.
#[tokio::test]
async fn drain_interjection_expands_skill_slash_reference() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;

            let dir = tempfile::tempdir().unwrap();
            let path = dir.path().join("SKILL.md");
            std::fs::write(&path, "Find sessions matching $ARGUMENTS").unwrap();
            let skill = xai_grok_tools::implementations::skills::types::SkillInfo {
                name: "find-session".to_owned(),
                description: "Find past sessions".to_owned(),
                path: path.to_string_lossy().into_owned(),
                ..Default::default()
            };
            actor
                .agent
                .borrow()
                .tool_bridge()
                .clone()
                .seed_skill_discovery(
                    Some(std::path::PathBuf::from("/tmp")),
                    None,
                    vec![skill],
                    None,
                    Some(256_000),
                    None,
                    xai_grok_tools::types::compat::CompatConfig::default(),
                )
                .await;

            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "/find-session foo".to_string(),
                attachments: vec![],
            });
            assert!(actor.drain_pending_interjections().await);

            let conversation = actor.chat_state_handle.get_conversation().await;
            let text = conversation.last().expect("user item").text_content();
            assert!(
                text.contains("<user_query>\n/find-session foo\n</user_query>"),
                "raw slash text stays the visible query, got: {text}"
            );
            let query_end = text.find("</user_query>").expect("wrapped query");
            let envelope = text
                .find("<skill_information>")
                .unwrap_or_else(|| panic!("skill envelope must be appended, got: {text}"));
            assert!(
                query_end < envelope,
                "envelope must follow the query, got: {text}"
            );
            assert!(
                text.contains("Find sessions matching foo"),
                "SKILL.md body with substituted args must ride along, got: {text}"
            );

            // A steering interjection that only MENTIONS the skill mid-text
            // (no leading slash) stays untouched — mirrors turn-start
            // gating, where "don't run /commit yet" is not an invocation.
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "don't run /find-session yet".to_string(),
                attachments: vec![],
            });
            assert!(actor.drain_pending_interjections().await);
            let conversation = actor.chat_state_handle.get_conversation().await;
            let text = conversation.last().expect("user item").text_content();
            assert!(
                !text.contains("<skill_information>"),
                "non-leading slash mentions must not grow an envelope, got: {text}"
            );
        })
        .await;
}

/// `format_interjection`'s large-prompt truncation applies to the TEXT only —
/// image data rides structurally and is never truncated or inlined.
#[tokio::test]
async fn drain_interjection_truncation_never_touches_image_data() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _gateway_rx) = build_actor().await;
            let original_image = test_image_content();
            // Way over LARGE_PROMPT_THRESHOLD so the text path truncates.
            let huge_text = "x".repeat(3_000_000);
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: huge_text,
                attachments: vec![original_image.clone()],
            });

            assert!(actor.drain_pending_interjections().await);

            let conversation = actor.chat_state_handle.get_conversation().await;
            let user_item = match conversation.last() {
                Some(ConversationItem::User(u)) => u,
                other => panic!("conversation tail must be a user item, got: {other:?}"),
            };
            let text = conversation.last().unwrap().text_content();
            assert!(text.contains("[truncated]"), "oversized text must truncate");
            let image_url = user_item
                .content
                .iter()
                .find_map(|p| match p {
                    xai_grok_sampling_types::ContentPart::Image { url } => Some(url.as_ref()),
                    _ => None,
                })
                .expect("image part must survive truncation");
            assert!(
                image_url.ends_with(&original_image.data),
                "image payload must be byte-identical (never truncated)"
            );
        })
        .await;
}

/// An interjection converted to a fallback prompt turn lands FRONT of the
/// queue (send-now beats queued-for-later), carries the text + image blocks,
/// and uses the persist-only `interject-fallback-` prompt-id prefix.
#[tokio::test]
async fn interjection_fallback_prompt_queues_front_with_prefix() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            {
                let mut state = actor.state.lock().await;
                state
                    .pending_inputs
                    .push_back(user_item("queued-later", "A"));
            }

            actor
                .queue_interjection_fallback_prompt(
                    "steer now".to_string(),
                    vec![test_image_content()],
                    true,
                )
                .await;

            let state = actor.state.lock().await;
            assert_eq!(state.pending_inputs.len(), 2);
            let front = state.pending_inputs.front().expect("front item");
            assert!(
                front.prompt_id.starts_with("interject-fallback-"),
                "fallback prompt id must carry the persist-only prefix, got {}",
                front.prompt_id
            );
            assert!(
                matches!(
                    front.prompt_blocks.first(),
                    Some(acp::ContentBlock::Text(t)) if t.text == "steer now"
                ),
                "text block first"
            );
            assert!(
                matches!(
                    front.prompt_blocks.get(1),
                    Some(acp::ContentBlock::Image(_))
                ),
                "image blocks ride along"
            );
            assert!(front.queue_meta.is_none(), "not a shared-queue row");
            assert_eq!(
                state.pending_inputs[1].prompt_id, "queued-later",
                "previously queued prompt stays behind the send-now text"
            );
        })
        .await;
}

/// Interjections that miss the completed turn's final drain are flushed into
/// fallback prompt turns — front of the queue, original order — instead of
/// stranding in `pending_interjections` (the queue-jam: pager said
/// "Interjection sent" but the message was never sent).
#[tokio::test]
async fn flush_stranded_interjections_converts_to_front_prompts_in_order() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            {
                let mut state = actor.state.lock().await;
                state
                    .pending_inputs
                    .push_back(user_item("queued-later", "A"));
            }
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "first steer".to_string(),
                attachments: vec![],
            });
            actor.pending_interjections.push(PendingInterjection {
                origin: crate::session::PromptOrigin::User,
                text: "second steer".to_string(),
                attachments: vec![],
            });

            assert_eq!(actor.flush_stranded_interjections().await, 2);
            assert!(
                actor.pending_interjections.is_empty(),
                "flush must drain the buffer"
            );

            let state = actor.state.lock().await;
            let texts: Vec<String> = state
                .pending_inputs
                .iter()
                .map(|i| match i.prompt_blocks.first() {
                    Some(acp::ContentBlock::Text(t)) => t.text.clone(),
                    other => panic!("expected text block, got {other:?}"),
                })
                .collect();
            assert_eq!(
                texts,
                vec![
                    "first steer".to_string(),
                    "second steer".to_string(),
                    "text for queued-later".to_string()
                ],
                "stranded interjections run next, in arrival order"
            );
        })
        .await;
}

/// An empty buffer flushes to nothing (no phantom turns).
#[tokio::test]
async fn flush_stranded_interjections_noop_when_empty() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            assert_eq!(actor.flush_stranded_interjections().await, 0);
            assert!(actor.state.lock().await.pending_inputs.is_empty());
        })
        .await;
}

/// Review fix: front placement never displaces a pinned running front — the
/// fallback item lands right behind it when a promotion raced the check.
#[tokio::test]
async fn fallback_prompt_lands_behind_running_front() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            {
                let mut state = actor.state.lock().await;
                state.pending_inputs.push_back(user_item("running", "A"));
                state.running_task = Some(running_task_stub("running"));
                state.pending_inputs.push_back(user_item("later", "A"));
            }
            *actor
                .current_prompt_id
                .lock()
                .expect("current_prompt_id mutex poisoned") = Some("running".into());

            actor
                .queue_interjection_fallback_prompt("urgent".to_string(), vec![], true)
                .await;

            let state = actor.state.lock().await;
            let ids: Vec<&str> = state
                .pending_inputs
                .iter()
                .map(|i| i.prompt_id.as_str())
                .collect();
            assert_eq!(ids[0], "running", "running front stays pinned");
            assert!(
                ids[1].starts_with("interject-fallback-"),
                "fallback lands right behind the running front, got {ids:?}"
            );
            assert_eq!(ids[2], "later");
        })
        .await;
}

/// A fallback prompt turn created while plan mode is active must not escape
/// the plan gate: it carries `PromptMode::Plan`.
#[tokio::test]
async fn fallback_prompt_respects_active_plan_mode() {
    let local = tokio::task::LocalSet::new();
    local
        .run_until(async {
            let (actor, _rx) = build_actor().await;
            {
                let mut tracker = actor.plan_mode.lock();
                tracker.enter_pending();
                tracker.activate();
            }

            actor
                .queue_interjection_fallback_prompt("plan steer".to_string(), vec![], true)
                .await;

            let state = actor.state.lock().await;
            let front = state.pending_inputs.front().expect("fallback queued");
            assert_eq!(
                front.prompt_mode,
                crate::session::plan_mode::PromptMode::Plan,
                "fallback turn must stay inside plan mode"
            );
        })
        .await;
}

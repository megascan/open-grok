use super::*;

#[test]
fn child_task_and_wake_keep_model_authorship_and_sender() {
    use xai_grok_tools::implementations::grok_build::task::types::AgentMailboxMessageKind;

    let (initial, origin) = child_prompt("/always-approve on", "root", None);
    assert_eq!(initial, "/always-approve on");
    assert!(
        matches!(origin, crate::session::PromptOrigin::ParentAgentMessage { sender_session_id, .. } if sender_session_id == "root")
    );
    let wake = WakeOrigin {
        agent_id: "recipient".into(),
        message_id: "original-message".into(),
        source: WakeSource::Agent {
            agent_id: "sender".into(),
            kind: AgentMailboxMessageKind::Message,
        },
    };
    let (text, origin) = child_prompt("/always-approve on", "root", Some(&wake));
    assert!(text.contains("sender=\"sender\""));
    assert!(text.contains("message_id=\"original-message\""));
    assert!(text.contains("/always-approve on"));
    assert!(
        matches!(origin, crate::session::PromptOrigin::AgentMessage { message_id } if message_id == "original-message")
    );
    let parent_wake = WakeOrigin {
        source: WakeSource::Parent {
            session_id: "root".into(),
        },
        ..wake
    };
    let (_, origin) = child_prompt("follow up", "root", Some(&parent_wake));
    assert!(
        matches!(origin, crate::session::PromptOrigin::ParentAgentMessage { message_id, sender_session_id } if message_id == "original-message" && sender_session_id == "root")
    );
}

#[tokio::test]
async fn cancelled_readiness_wins_over_ready_ack_and_result() {
    let (ready_tx, ready_rx) = oneshot::channel();
    ready_tx.send(()).unwrap();
    let mut attempt = std::future::ready(7);
    let readiness = wait_initial_child_prompt_readiness(
        std::future::ready(()),
        ready_rx,
        &mut attempt,
        std::time::Duration::ZERO,
    )
    .await;
    assert!(matches!(readiness, InitialChildPromptReadiness::Cancelled));
}

#[tokio::test]
async fn admitted_readiness_wins_over_completed_attempt() {
    let (ready_tx, ready_rx) = oneshot::channel();
    ready_tx.send(()).unwrap();
    let mut attempt = std::future::ready(7);
    let readiness = wait_initial_child_prompt_readiness(
        std::future::pending(),
        ready_rx,
        &mut attempt,
        std::time::Duration::ZERO,
    )
    .await;
    assert!(matches!(readiness, InitialChildPromptReadiness::Admitted));
}

#[tokio::test]
async fn closed_readiness_does_not_hide_attempt_failure() {
    let (ready_tx, ready_rx) = oneshot::channel::<()>();
    drop(ready_tx);
    let mut attempt = std::future::ready("failed before parsing");
    let readiness = wait_initial_child_prompt_readiness(
        std::future::pending(),
        ready_rx,
        &mut attempt,
        std::time::Duration::ZERO,
    )
    .await;
    assert!(matches!(
        readiness,
        InitialChildPromptReadiness::AttemptCompleted("failed before parsing"),
    ));
}

#[tokio::test(start_paused = true)]
async fn unresponsive_initial_prompt_has_bounded_admission() {
    let (_ready_tx, ready_rx) = oneshot::channel::<()>();
    let mut attempt = std::future::pending::<()>();
    let readiness = wait_initial_child_prompt_readiness(
        std::future::pending(),
        ready_rx,
        &mut attempt,
        std::time::Duration::from_secs(30),
    )
    .await;
    assert!(matches!(readiness, InitialChildPromptReadiness::TimedOut));
}

#[tokio::test(start_paused = true)]
async fn unanswered_child_query_returns_conservative_fallback() {
    let value = child_actor_query("test", std::future::pending::<u64>(), 9).await;
    assert_eq!(value, 9);
}

#[tokio::test(start_paused = true)]
async fn usage_fold_cannot_wait_forever_for_parent_ack() {
    let (command_tx, _command_rx) = mpsc::unbounded_channel();
    let acknowledged = record_subagent_usage(
        Some(&command_tx),
        Some(vec![(
            "model".into(),
            xai_chat_state::UsageTotals {
                input_tokens: 10,
                ..Default::default()
            },
        )]),
        Some("parent-prompt".into()),
        false,
    )
    .await;
    assert!(!acknowledged);
}

#[test]
fn resume_limit_reserves_prelude_and_respects_effective_compaction_threshold() {
    assert_eq!(resume_token_limit(100_000, 80), 80_000);
    assert_eq!(resume_token_limit(100_000, 90), 90_000);
    assert_eq!(resume_token_limit(100_000, 100), 95_000);
    assert_eq!(resume_token_limit(0, 80), 0);
}

#[tokio::test]
async fn missing_child_history_is_empty() {
    let directory = tempfile::tempdir().unwrap();
    let history = load_subagent_history(directory.path()).await;
    assert!(history.is_ok());
    assert!(history.unwrap().is_empty());
}

#[tokio::test]
async fn child_history_load_round_trips_provider_tagged_items() {
    let directory = tempfile::tempdir().unwrap();
    let original = [
        ConversationItem::system("system"),
        ConversationItem::user("task"),
        ConversationItem::assistant("answer"),
    ];
    let serialized = original
        .iter()
        .map(|item| serde_json::to_string(item).unwrap())
        .collect::<Vec<_>>()
        .join("\n");
    std::fs::write(directory.path().join("chat_history.jsonl"), serialized).unwrap();
    let restored = load_subagent_history(directory.path()).await.unwrap();
    assert_eq!(
        serde_json::to_value(restored).unwrap(),
        serde_json::to_value(original).unwrap(),
    );
}

use super::*;
use crate::permission::types::{PatternMode, PermissionConfig, RuleAction, ToolFilter};

fn rule(action: RuleAction, tool: ToolFilter) -> PermissionRule {
    PermissionRule {
        action,
        tool,
        pattern: Some("restricted*".to_owned()),
        pattern_mode: PatternMode::Glob,
    }
}

#[tokio::test]
async fn unrestricted_lease_requires_actor_yolo_and_no_restrictions() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let tmp = tempfile::tempdir().unwrap();
            let cwd = AbsPathBuf::new(tmp.path().to_path_buf()).unwrap();
            assert!(
                PermissionHandle::allow_all()
                    .acquire_unrestricted_lease()
                    .await
                    .is_none()
            );
            assert!(
                test_manager(&cwd, false, None)
                    .0
                    .acquire_unrestricted_lease()
                    .await
                    .is_none()
            );
            assert!(
                test_manager(&cwd, true, Some(PIN))
                    .0
                    .acquire_unrestricted_lease()
                    .await
                    .is_none()
            );
            assert!(
                test_manager(&cwd, true, None)
                    .0
                    .acquire_unrestricted_lease()
                    .await
                    .is_some()
            );
            for tool in [
                ToolFilter::Any,
                ToolFilter::Bash,
                ToolFilter::Edit,
                ToolFilter::Read,
                ToolFilter::Grep,
                ToolFilter::Mcp,
                ToolFilter::WebFetch,
                ToolFilter::WebSearch,
                ToolFilter::AgentMessage,
            ] {
                for action in [RuleAction::Deny, RuleAction::Ask] {
                    let config = PermissionConfig::new(vec![rule(action, tool.clone())]);
                    assert!(
                        test_manager_with_config(&cwd, config, true)
                            .0
                            .acquire_unrestricted_lease()
                            .await
                            .is_none()
                    );
                }
            }
            let allow = PermissionConfig::new(vec![rule(RuleAction::Allow, ToolFilter::Read)]);
            assert!(
                test_manager_with_config(&cwd, allow, true)
                    .0
                    .acquire_unrestricted_lease()
                    .await
                    .is_some()
            );
            let mut denied_mode = PermissionConfig::new(vec![]);
            denied_mode.prompt_policy = PromptPolicy::Deny;
            assert!(
                test_manager_with_config(&cwd, denied_mode, true)
                    .0
                    .acquire_unrestricted_lease()
                    .await
                    .is_none()
            );
            let (tx, _rx) = mpsc::unbounded_channel();
            let (read_denied, _) = spawn_permission_manager_with_pin(
                acp::SessionId::new(Arc::from("read-denied")),
                GatewaySender::new(tx),
                cwd.clone(),
                ClientType::Generic,
                None,
                vec!["secret/**".to_owned()],
                vec![],
                true,
                None,
                true,
                None,
                None,
            );
            assert!(read_denied.acquire_unrestricted_lease().await.is_none());
        })
        .await;
}

#[tokio::test]
async fn unrestricted_lease_refuses_persisted_denials() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let tmp = tempfile::tempdir().unwrap();
            for kind in 0..3 {
                let dir = tmp.path().join(format!("{kind}"));
                std::fs::create_dir(&dir).unwrap();
                let cwd = AbsPathBuf::new(dir).unwrap();
                let mut state = PermissionState::default();
                match kind {
                    0 => {
                        state.disallowed_bash_commands.insert("restricted".into());
                    }
                    1 => {
                        state.disallowed_mcp_tools.insert("restricted".into());
                    }
                    _ => {
                        state
                            .disallowed_web_fetch_domains
                            .insert("restricted.example".into());
                    }
                }
                persist_state(&cwd, &state, None).await;
                assert!(
                    test_manager(&cwd, true, None)
                        .0
                        .acquire_unrestricted_lease()
                        .await
                        .is_none()
                );
            }
        })
        .await;
}

#[tokio::test]
async fn lease_revokes_on_mode_and_rule_changes_and_actor_shutdown() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let tmp = tempfile::tempdir().unwrap();
            let cwd = AbsPathBuf::new(tmp.path().to_path_buf()).unwrap();
            let (handle, _) = test_manager(&cwd, true, None);
            let lease = handle.acquire_unrestricted_lease().await.unwrap();
            handle.set_yolo_mode(false);
            assert!(lease.is_revoked());
            assert!(handle.acquire_unrestricted_lease().await.is_none());
            handle.set_yolo_mode(true);
            let lease = handle.acquire_unrestricted_lease().await.unwrap();
            handle.add_session_rules(vec![rule(RuleAction::Deny, ToolFilter::Mcp)]);
            assert!(lease.is_revoked());
            assert!(handle.acquire_unrestricted_lease().await.is_none());
            handle.reset_session_rules();
            let lease = handle.acquire_unrestricted_lease().await.unwrap();
            let PermissionHandle::Actor { cmd_tx, .. } = &handle else {
                unreachable!()
            };
            cmd_tx.send(PermissionCommand::Shutdown).unwrap();
            tokio::time::timeout(std::time::Duration::from_secs(5), lease.revoked())
                .await
                .unwrap();
            assert!(handle.acquire_unrestricted_lease().await.is_none());
        })
        .await;
}

#[cfg(unix)]
#[tokio::test]
async fn spawn_registration_and_mutation_serialize_and_kill_the_process() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let tmp = tempfile::tempdir().unwrap();
            let cwd = AbsPathBuf::new(tmp.path().to_path_buf()).unwrap();
            let (handle, _) = test_manager(&cwd, true, None);
            let lease = handle.acquire_unrestricted_lease().await.unwrap();
            let scope = xai_tty_utils::ProcessScope::new();
            let (start_tx, start_rx) = std::sync::mpsc::channel();
            let (entered_tx, entered_rx) = std::sync::mpsc::channel();
            let (finished_tx, finished_rx) = std::sync::mpsc::channel();
            let other_handle = handle.clone();
            let worker = std::thread::spawn(move || {
                start_rx.recv().unwrap();
                entered_tx.send(()).unwrap();
                other_handle.set_yolo_mode(false);
                finished_tx.send(()).unwrap();
            });
            let (mut child, _group) = lease
                .with_active_process(|| -> std::io::Result<_> {
                    let mut command = tokio::process::Command::new("/bin/sleep");
                    command.arg("30").kill_on_drop(true);
                    scope.prepare(&mut command);
                    let child = command.spawn()?;
                    let group = scope.enroll(&child)?;
                    start_tx.send(()).unwrap();
                    entered_rx.recv().unwrap();
                    assert!(
                        finished_rx.try_recv().is_err(),
                        "transition must wait for enrollment"
                    );
                    Ok(((child, group.clone()), group))
                })
                .unwrap()
                .unwrap();
            worker.join().unwrap();
            assert!(lease.is_revoked());
            assert!(
                lease
                    .with_active_process(
                        || -> std::io::Result<((), Arc<xai_tty_utils::ProcessGroup>)> {
                            panic!("revoked lease must not run closure")
                        }
                    )
                    .is_err()
            );
            let status = tokio::time::timeout(std::time::Duration::from_secs(5), child.wait())
                .await
                .unwrap()
                .unwrap();
            assert!(!status.success());
        })
        .await;
}

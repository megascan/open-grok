//! Tests for session-related modals (extensions, /new worktree question)
//! and session close helpers shared with the dashboard.

use super::*;

#[test]
fn failed_title_reset_cannot_undo_newer_rename_or_binding() {
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    app.agents.get_mut(&id).unwrap().display_name = Some("old".into());
    let effects = dispatch(Action::ResetSessionTitleToAuto, &mut app);
    let [
        Effect::ResetSessionTitle {
            generation,
            binding_epoch,
            previous_display_name,
            previous_generated_title,
            ..
        },
    ] = effects.as_slice()
    else {
        panic!("expected title reset");
    };
    let failure = TaskResult::ResetSessionTitleFailed {
        agent_id: id,
        generation: *generation,
        binding_epoch: *binding_epoch,
        previous_display_name: previous_display_name.clone(),
        previous_generated_title: previous_generated_title.clone(),
        error: "reset failed".into(),
    };
    dispatch(
        Action::RenameSession {
            title: "new".into(),
        },
        &mut app,
    );
    dispatch(Action::TaskComplete(failure), &mut app);
    assert_eq!(app.agents[&id].display_name.as_deref(), Some("new"));

    let effects = dispatch(Action::ResetSessionTitleToAuto, &mut app);
    let [
        Effect::ResetSessionTitle {
            generation,
            binding_epoch,
            previous_display_name,
            previous_generated_title,
            ..
        },
    ] = effects.as_slice()
    else {
        panic!("expected title reset");
    };
    let failure = TaskResult::ResetSessionTitleFailed {
        agent_id: id,
        generation: *generation,
        binding_epoch: *binding_epoch,
        previous_display_name: previous_display_name.clone(),
        previous_generated_title: previous_generated_title.clone(),
        error: "reset failed".into(),
    };
    let agent = app.agents.get_mut(&id).unwrap();
    agent.bind_session_id(acp::SessionId::new("different-session"));
    agent.display_name = Some("different title".into());
    dispatch(Action::TaskComplete(failure), &mut app);
    assert_eq!(
        app.agents[&id].display_name.as_deref(),
        Some("different title")
    );
}

#[test]
fn consecutive_failed_title_resets_keep_original_pin() {
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    app.agents.get_mut(&id).unwrap().display_name = Some("old".into());
    let first = dispatch(Action::ResetSessionTitleToAuto, &mut app);
    let effects = dispatch(Action::ResetSessionTitleToAuto, &mut app);
    let [
        Effect::ResetSessionTitle {
            generation,
            binding_epoch,
            previous_display_name,
            previous_generated_title,
            ..
        },
    ] = effects.as_slice()
    else {
        panic!("expected second reset");
    };
    let [
        Effect::ResetSessionTitle {
            generation: first_generation,
            previous_display_name: first_display,
            previous_generated_title: first_generated,
            ..
        },
    ] = first.as_slice()
    else {
        panic!("expected first reset");
    };
    dispatch(
        Action::TaskComplete(TaskResult::ResetSessionTitleFailed {
            agent_id: id,
            generation: *generation,
            binding_epoch: *binding_epoch,
            previous_display_name: previous_display_name.clone(),
            previous_generated_title: previous_generated_title.clone(),
            error: "reset failed".into(),
        }),
        &mut app,
    );
    // Results can reach the UI out of order even though the RPCs are FIFO.
    dispatch(
        Action::TaskComplete(TaskResult::ResetSessionTitleFailed {
            agent_id: id,
            generation: *first_generation,
            binding_epoch: *binding_epoch,
            previous_display_name: first_display.clone(),
            previous_generated_title: first_generated.clone(),
            error: "first reset failed".into(),
        }),
        &mut app,
    );
    assert_eq!(app.agents[&id].display_name.as_deref(), Some("old"));
}

#[test]
fn earlier_successful_reset_prevents_later_failure_restoring_the_pin() {
    for completion_in_order in [true, false] {
        let mut app = test_app_with_agent();
        let id = AgentId(0);
        app.agents.get_mut(&id).unwrap().display_name = Some("old".into());
        let first = dispatch(Action::ResetSessionTitleToAuto, &mut app);
        let [
            Effect::ResetSessionTitle {
                generation: first_generation,
                binding_epoch,
                ..
            },
        ] = first.as_slice()
        else {
            panic!("expected first reset");
        };
        let second = dispatch(Action::ResetSessionTitleToAuto, &mut app);
        let [
            Effect::ResetSessionTitle {
                generation,
                previous_display_name,
                previous_generated_title,
                ..
            },
        ] = second.as_slice()
        else {
            panic!("expected second reset");
        };
        let success = TaskResult::ResetSessionTitleComplete {
            agent_id: id,
            generation: *first_generation,
            binding_epoch: *binding_epoch,
        };
        let failure = TaskResult::ResetSessionTitleFailed {
            agent_id: id,
            generation: *generation,
            binding_epoch: *binding_epoch,
            previous_display_name: previous_display_name.clone(),
            previous_generated_title: previous_generated_title.clone(),
            error: "reset failed".into(),
        };
        let outcomes = if completion_in_order {
            [success, failure]
        } else {
            [failure, success]
        };
        for outcome in outcomes {
            dispatch(Action::TaskComplete(outcome), &mut app);
        }
        assert_eq!(app.agents[&id].display_name, None);
    }
}

#[test]
fn older_reset_success_cannot_cross_an_intervening_rename() {
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    app.agents.get_mut(&id).unwrap().display_name = Some("old".into());
    let first = dispatch(Action::ResetSessionTitleToAuto, &mut app);
    let [
        Effect::ResetSessionTitle {
            generation: first_generation,
            binding_epoch,
            ..
        },
    ] = first.as_slice()
    else {
        panic!("expected first reset");
    };
    let rename = dispatch(
        Action::RenameSession {
            title: "new".into(),
        },
        &mut app,
    );
    let [
        Effect::RenameSession {
            generation: rename_generation,
            ..
        },
    ] = rename.as_slice()
    else {
        panic!("expected rename");
    };
    let second = dispatch(Action::ResetSessionTitleToAuto, &mut app);
    let [
        Effect::ResetSessionTitle {
            generation,
            previous_display_name,
            previous_generated_title,
            ..
        },
    ] = second.as_slice()
    else {
        panic!("expected second reset");
    };
    dispatch(
        Action::TaskComplete(TaskResult::ResetSessionTitleComplete {
            agent_id: id,
            generation: *first_generation,
            binding_epoch: *binding_epoch,
        }),
        &mut app,
    );
    dispatch(
        Action::TaskComplete(TaskResult::RenameSessionComplete {
            agent_id: id,
            title: "new".into(),
            generation: *rename_generation,
            binding_epoch: *binding_epoch,
        }),
        &mut app,
    );
    dispatch(
        Action::TaskComplete(TaskResult::ResetSessionTitleFailed {
            agent_id: id,
            generation: *generation,
            binding_epoch: *binding_epoch,
            previous_display_name: previous_display_name.clone(),
            previous_generated_title: previous_generated_title.clone(),
            error: "failed".into(),
        }),
        &mut app,
    );
    assert_eq!(app.agents[&id].display_name.as_deref(), Some("new"));
}

#[test]
fn failed_renames_then_failed_reset_restore_confirmed_predecessor() {
    for first_rename_succeeds in [false, true] {
        for reverse_results in [false, true] {
            let mut app = test_app_with_agent();
            let id = AgentId(0);
            let agent = app.agents.get_mut(&id).unwrap();
            agent.display_name = Some("old".into());
            agent.generated_session_title = Some("old".into());
            let first = dispatch(
                Action::RenameSession {
                    title: "first".into(),
                },
                &mut app,
            );
            let second = dispatch(
                Action::RenameSession {
                    title: "invalid title".into(),
                },
                &mut app,
            );
            let reset = dispatch(Action::ResetSessionTitleToAuto, &mut app);
            let [
                Effect::RenameSession {
                    generation: first_generation,
                    binding_epoch,
                    ..
                },
            ] = first.as_slice()
            else {
                panic!("expected first rename");
            };
            let [
                Effect::RenameSession {
                    generation: second_generation,
                    ..
                },
            ] = second.as_slice()
            else {
                panic!("expected second rename");
            };
            let [
                Effect::ResetSessionTitle {
                    generation: reset_generation,
                    previous_display_name,
                    previous_generated_title,
                    ..
                },
            ] = reset.as_slice()
            else {
                panic!("expected reset");
            };
            let first_result = if first_rename_succeeds {
                TaskResult::RenameSessionComplete {
                    agent_id: id,
                    title: "first".into(),
                    generation: *first_generation,
                    binding_epoch: *binding_epoch,
                }
            } else {
                TaskResult::RenameSessionFailed {
                    agent_id: id,
                    error: "first rename failed".into(),
                    generation: *first_generation,
                    binding_epoch: *binding_epoch,
                }
            };
            let mut outcomes = vec![
                first_result,
                TaskResult::RenameSessionFailed {
                    agent_id: id,
                    error: "title rejected".into(),
                    generation: *second_generation,
                    binding_epoch: *binding_epoch,
                },
                TaskResult::ResetSessionTitleFailed {
                    agent_id: id,
                    error: "reset failed".into(),
                    generation: *reset_generation,
                    binding_epoch: *binding_epoch,
                    previous_display_name: previous_display_name.clone(),
                    previous_generated_title: previous_generated_title.clone(),
                },
            ];
            if reverse_results {
                outcomes.reverse();
            }
            for outcome in outcomes {
                dispatch(Action::TaskComplete(outcome), &mut app);
            }
            let expected = if first_rename_succeeds {
                "first"
            } else {
                "old"
            };
            assert_eq!(app.agents[&id].display_name.as_deref(), Some(expected));
            assert_eq!(
                app.agents[&id].generated_session_title.as_deref(),
                Some(expected)
            );
            assert!(app.agents[&id].title_mutations.is_none());
        }
    }
}

#[test]
fn open_extensions_modal_no_session_sets_flag_no_fetches() {
    use crate::views::extensions_modal::ExtensionsTab;
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    app.agents.get_mut(&id).unwrap().session.session_id = None;
    let effects = dispatch(
        Action::OpenExtensionsModal {
            tab: ExtensionsTab::Hooks,
            trigger: xai_grok_telemetry::events::ExtensionsModalTrigger::SlashCommand,
        },
        &mut app,
    );
    assert_eq!(count_extension_fetches(&effects), 0);
    assert!(app.agents[&id].pending_extensions_fetch);
    assert!(app.agents[&id].extensions_modal.is_some());
    assert!(
        !effects
            .iter()
            .any(|e| matches!(e, Effect::CreateSession { .. })),
        "opening the modal must not create a session, got {effects:?}"
    );
}

#[test]
fn open_extensions_modal_with_session_emits_fetches_no_flag() {
    use crate::views::extensions_modal::ExtensionsTab;
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    let effects = dispatch(
        Action::OpenExtensionsModal {
            tab: ExtensionsTab::Hooks,
            trigger: xai_grok_telemetry::events::ExtensionsModalTrigger::SlashCommand,
        },
        &mut app,
    );
    assert_eq!(count_extension_fetches(&effects), 5);
    assert!(!app.agents[&id].pending_extensions_fetch);
}

#[test]
fn open_extensions_modal_with_session_resets_stale_flag() {
    use crate::views::extensions_modal::ExtensionsTab;
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    app.agents.get_mut(&id).unwrap().pending_extensions_fetch = true;
    let effects = dispatch(
        Action::OpenExtensionsModal {
            tab: ExtensionsTab::Hooks,
            trigger: xai_grok_telemetry::events::ExtensionsModalTrigger::SlashCommand,
        },
        &mut app,
    );
    assert_eq!(count_extension_fetches(&effects), 5);
    assert!(!app.agents[&id].pending_extensions_fetch);
}

#[test]
fn session_created_with_flag_but_modal_closed_clears_flag_no_fetches() {
    let mut app = test_app_with_agent();
    let id = AgentId(0);
    {
        let a = app.agents.get_mut(&id).unwrap();
        a.session.session_id = None;
        a.pending_extensions_fetch = true;
        a.extensions_modal = None;
    }
    let effects = dispatch(
        Action::TaskComplete(TaskResult::SessionCreated {
            agent_id: id,
            session_id: acp::SessionId::new("s"),
            models: None,
            scheduler_background_loops: None,
        }),
        &mut app,
    );
    assert_eq!(count_extension_fetches(&effects), 0);
    assert!(!app.agents[&id].pending_extensions_fetch);
}

#[test]
fn dispatch_new_session_opens_question_modal_in_git_repo() {
    let mut app = new_session_test_app();
    app.new_session_worktree_mode = crate::app::app_view::WorktreeMode::Ask;
    let effects = dispatch(Action::NewSession, &mut app);
    assert!(effects.is_empty(), "no effects until modal answered");
    // No new agent yet (creation is deferred until modal answered).
    assert_eq!(app.agents.len(), 1);
    let qv = app.agents[&AgentId(0)]
        .question_view
        .as_ref()
        .expect("modal must be open");
    match qv.local_kind.as_ref().expect("local_kind must be set") {
        crate::views::question_view::LocalQuestionKind::NewSession => {}
        other => panic!("expected NewSession, got {other:?}"),
    }
    assert_eq!(
        qv.questions[0].options.len(),
        4,
        "modal must offer exactly 4 options (Yes/No/Always/Never)"
    );
    let labels: Vec<&str> = qv.questions[0]
        .options
        .iter()
        .map(|o| o.label.as_str())
        .collect();
    assert_eq!(
        labels,
        vec!["Yes", "No", "Always worktree", "Never worktree"]
    );
}

#[test]
fn dispatch_new_session_skips_modal_in_non_git_repo() {
    // current_branch stays None (no git repo) → no modal, straight
    // to dispatch_new_session_inner.
    let mut app = test_app_with_agent();
    let effects = dispatch(Action::NewSession, &mut app);
    assert!(
        effects
            .iter()
            .any(|e| matches!(e, Effect::CreateSession { .. })),
        "non-git path must emit CreateSession, got {effects:?}"
    );
    assert!(
        app.agents.values().all(|a| a.question_view.is_none()),
        "non-git path must not open the modal"
    );
}

#[test]
fn close_inactive_agent_drops_it() {
    let mut app = three_agent_app();
    let effects = dispatch_sessions_confirm_close(&mut app, AgentId(2));
    assert!(
        effects
            .iter()
            .all(|e| matches!(e, Effect::UnregisterActiveSession { .. }))
    );
    assert!(!app.agents.contains_key(&AgentId(2)));
    assert_eq!(app.agents.len(), 2);
}

#[test]
fn close_agent_releases_retained_memory() {
    use crate::memory_release::test_support;
    test_support::install_counting_hook();

    let mut app = three_agent_app();

    // Dropping a real AgentView (scrollback + caches + child views) → purge.
    let before = test_support::calls();
    dispatch_sessions_confirm_close(&mut app, AgentId(2));
    assert!(!app.agents.contains_key(&AgentId(2)));
    assert_eq!(
        test_support::calls(),
        before + 1,
        "dropping the closed AgentView must purge retained pages"
    );

    // Closing an unknown agent drops nothing → no purge.
    let before = test_support::calls();
    dispatch_sessions_confirm_close(&mut app, AgentId(999));
    assert_eq!(
        test_support::calls(),
        before,
        "a no-op close must not purge"
    );
}

#[test]
fn close_clears_forked_from_on_surviving_children() {
    let mut app = three_agent_app();
    set_forked_from(&mut app, AgentId(2), AgentId(1));
    dispatch_sessions_confirm_close(&mut app, AgentId(1));
    assert!(
        app.agents[&AgentId(2)].session.forked_from.is_none(),
        "stale forked_from pointer must be cleared after parent close"
    );
}

#[test]
fn close_only_agent_is_refused_with_toast() {
    let mut app = test_app_with_agent();
    let agents_before = app.agents.len();
    dispatch_sessions_confirm_close(&mut app, AgentId(0));
    assert_eq!(
        app.agents.len(),
        agents_before,
        "the only agent must NOT be closed"
    );
}

#[test]
fn close_unknown_agent_is_silent_noop() {
    let mut app = three_agent_app();
    let agents_before = app.agents.len();
    dispatch_sessions_confirm_close(&mut app, AgentId(999));
    assert_eq!(app.agents.len(), agents_before);
}

#[test]
fn close_only_agent_short_circuits_before_reaching_welcome_fallback() {
    let mut app = test_app_with_agent();
    assert!(matches!(app.active_view, ActiveView::Agent(id) if id == AgentId(0)));
    dispatch_sessions_confirm_close(&mut app, AgentId(0));
    assert!(matches!(app.active_view, ActiveView::Agent(id) if id == AgentId(0)));
    assert!(app.agents.contains_key(&AgentId(0)));
}

#[test]
fn close_does_not_disturb_unrelated_forked_from_pointers() {
    let mut app = three_agent_app();
    set_forked_from(&mut app, AgentId(1), AgentId(0));
    set_forked_from(&mut app, AgentId(2), AgentId(0));
    dispatch_sessions_confirm_close(&mut app, AgentId(1));
    assert_eq!(
        app.agents[&AgentId(2)].session.forked_from,
        Some(AgentId(0)),
        "unrelated forked_from must NOT be cleared"
    );
}

fn count_marketplace_fetches(effects: &[Effect]) -> usize {
    effects
        .iter()
        .filter(|e| matches!(e, Effect::FetchMarketplaceList { .. }))
        .count()
}

fn success_outcome() -> xai_hooks_plugins_types::ActionOutcome {
    xai_hooks_plugins_types::ActionOutcome {
        status: xai_hooks_plugins_types::OutcomeStatus::Success,
        message: "ok".into(),
        requires_reload: false,
        requires_restart: false,
    }
}

fn empty_marketplace_response() -> xai_hooks_plugins_types::MarketplaceListResponse {
    xai_hooks_plugins_types::MarketplaceListResponse { sources: vec![] }
}

#[test]
fn marketplace_fetch_coalesces_while_inflight() {
    use crate::views::extensions_modal::ExtensionsTab;
    let mut app = test_app_with_agent();
    let id = AgentId(0);

    let effects = dispatch(
        Action::OpenExtensionsModal {
            tab: ExtensionsTab::Marketplace,
            trigger: xai_grok_telemetry::events::ExtensionsModalTrigger::SlashCommand,
        },
        &mut app,
    );
    assert_eq!(count_marketplace_fetches(&effects), 1);

    // A successful action while the open-fetch is still in flight must not
    // stack a second scan; it queues one refetch instead.
    let effects = dispatch(
        Action::TaskComplete(TaskResult::PluginsActionResult {
            agent_id: id,
            result: Ok(success_outcome()),
        }),
        &mut app,
    );
    assert_eq!(count_marketplace_fetches(&effects), 0);
    assert!(
        effects
            .iter()
            .any(|e| matches!(e, Effect::FetchHooksList { .. })),
        "non-marketplace refetches still fire"
    );

    // When the in-flight fetch lands, the queued refetch fires exactly once.
    let effects = dispatch(
        Action::TaskComplete(TaskResult::MarketplaceListLoaded {
            agent_id: id,
            result: Ok(empty_marketplace_response()),
        }),
        &mut app,
    );
    assert_eq!(count_marketplace_fetches(&effects), 1);

    // And the queue drains: the refetch landing issues nothing further.
    let effects = dispatch(
        Action::TaskComplete(TaskResult::MarketplaceListLoaded {
            agent_id: id,
            result: Ok(empty_marketplace_response()),
        }),
        &mut app,
    );
    assert_eq!(count_marketplace_fetches(&effects), 0);
}

#[test]
fn marketplace_fetch_fires_immediately_when_idle() {
    use crate::views::extensions_modal::ExtensionsTab;
    let mut app = test_app_with_agent();
    let id = AgentId(0);

    dispatch(
        Action::OpenExtensionsModal {
            tab: ExtensionsTab::Marketplace,
            trigger: xai_grok_telemetry::events::ExtensionsModalTrigger::SlashCommand,
        },
        &mut app,
    );
    dispatch(
        Action::TaskComplete(TaskResult::MarketplaceListLoaded {
            agent_id: id,
            result: Ok(empty_marketplace_response()),
        }),
        &mut app,
    );

    // Nothing in flight: an action-triggered refetch goes out immediately.
    let effects = dispatch(
        Action::TaskComplete(TaskResult::PluginsActionResult {
            agent_id: id,
            result: Ok(success_outcome()),
        }),
        &mut app,
    );
    assert_eq!(count_marketplace_fetches(&effects), 1);
}

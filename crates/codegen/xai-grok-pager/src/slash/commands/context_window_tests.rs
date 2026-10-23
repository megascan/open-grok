use super::*;
use crate::acp::model_state::ModelState;
use crate::slash::commands::tests::make_ctx;
use agent_client_protocol as acp;

#[test]
fn supported_window_switches_the_current_model() {
    let state = model_with_windows(serde_json::json!([256_000, 500_000]));
    let sid = acp::SessionId::new("sess");
    let mut ctx = exec_ctx(&state, Some(&sid));

    match ContextWindowCommand.run(&mut ctx, "500k") {
        CommandResult::Action(Action::SwitchContextWindow { window }) => {
            assert_eq!(window.get(), 500_000);
        }
        other => panic!("expected SwitchContextWindow, got {other:?}"),
    }
}

#[test]
fn unsupported_window_and_empty_args_error_with_supported_list() {
    let state = model_with_windows(serde_json::json!([256_000, 500_000]));
    let sid = acp::SessionId::new("sess");
    let mut ctx = exec_ctx(&state, Some(&sid));

    match ContextWindowCommand.run(&mut ctx, "1m") {
        CommandResult::Error(msg) => {
            assert_eq!("unknown context window '1m'; use one of: 256k, 500k", msg);
        }
        other => panic!("expected Error, got {other:?}"),
    }

    match ContextWindowCommand.run(&mut ctx, "") {
        CommandResult::Error(msg) => {
            assert_eq!("Usage: /context-window <256k|500k> (current: 256k)", msg);
        }
        other => panic!("expected Error, got {other:?}"),
    }
}

#[test]
fn single_window_model_is_hidden_and_errors() {
    for state in [
        model_with_windows(serde_json::json!([])),
        model_with_windows(serde_json::json!([256_000])),
    ] {
        assert!(!ContextWindowCommand.visible(&app_ctx(&state)));
        assert!(
            ContextWindowCommand
                .suggest_args(&app_ctx(&state), "")
                .is_none()
        );

        let sid = acp::SessionId::new("sess");
        let mut ctx = exec_ctx(&state, Some(&sid));
        assert!(matches!(
            ContextWindowCommand.run(&mut ctx, "256k"),
            CommandResult::Error(msg) if msg.contains("no selectable context windows")
        ));
    }
}

#[test]
fn suggest_marks_default_and_active_rows() {
    let mut state = model_with_windows(serde_json::json!([256_000, 500_000]));
    state.context_window_selection = Some(500_000);

    let items = ContextWindowCommand
        .suggest_args(&app_ctx(&state), "")
        .expect("a two-window model supports suggestions");

    let [first, second] = items.as_slice() else {
        panic!("expected 2 items: {items:?}");
    };
    assert_eq!(first.display, "256k");
    assert_eq!(first.description, "Default • 256000 tokens");
    assert_eq!(second.display, "500k (active)");
    assert_eq!(second.insert_text, "500k");
}

#[test]
fn missing_model_or_session_errors() {
    let empty = ModelState::default();
    let sid = acp::SessionId::new("sess");
    let mut ctx = exec_ctx(&empty, Some(&sid));

    assert!(matches!(
        ContextWindowCommand.run(&mut ctx, "256k"),
        CommandResult::Error(msg) if msg.contains("No active model")
    ));

    let state = model_with_windows(serde_json::json!([256_000, 500_000]));
    let mut ctx = exec_ctx(&state, None);

    assert!(matches!(
        ContextWindowCommand.run(&mut ctx, "256k"),
        CommandResult::Error(msg) if msg.contains("No active session")
    ));
}

#[test]
fn format_and_parse_round_trip() {
    for (window, label) in [(256_000, "256k"), (500_000, "500k"), (1_000_000, "1m")] {
        assert_eq!(format_window(window), label);
        assert_eq!(parse_window_token(label), Some(window));
    }

    assert_eq!(parse_window_token("500000"), Some(500_000));
    assert_eq!(parse_window_token("500K"), Some(500_000));
    assert_eq!(parse_window_token("junk"), None);
}

fn model_with_windows(windows: serde_json::Value) -> ModelState {
    let id = acp::ModelId::new("grok-4.7");
    let info = acp::ModelInfo::new(id.clone(), "Grok 4.7".to_owned()).meta(
        serde_json::json!({ "totalContextTokens": 256_000, "contextWindows": windows })
            .as_object()
            .cloned(),
    );
    let mut state = ModelState::default();
    state.available.insert(id.clone(), info);
    state.current = Some(id);
    state
}

fn exec_ctx<'a>(
    models: &'a ModelState,
    session_id: Option<&'a acp::SessionId>,
) -> CommandExecCtx<'a> {
    CommandExecCtx {
        session_id,
        ..make_ctx(models)
    }
}

fn app_ctx(models: &ModelState) -> AppCtx<'_> {
    AppCtx {
        models,
        cwd: std::path::Path::new("."),
        has_session_announcements: false,
        billing_surface_visible: true,
        workflows_available: true,
        saved_workflows: &[],
        workflow_runs: &[],
        screen_mode: crate::app::ScreenMode::Fullscreen,
        current_title: None,
    }
}

use super::*;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt};

struct TestHost {
    updates: Vec<Value>,
    permissions: usize,
    questions: usize,
    urls: Vec<String>,
}

#[async_trait]
impl Host for TestHost {
    async fn update(&mut self, _method: &str, params: &Value) {
        self.updates.push(params.clone());
    }
    async fn request(&mut self, _method: &str, _params: &Value) -> Option<Value> {
        self.permissions += 1;
        None
    }
    async fn question(&mut self, _params: &Value) -> Option<Value> {
        self.questions += 1;
        None
    }
    async fn authentication_url(&mut self, url: &str) {
        self.urls.push(url.to_owned());
    }
}

impl TestHost {
    fn new() -> Self {
        Self {
            updates: Vec::new(),
            permissions: 0,
            questions: 0,
            urls: Vec::new(),
        }
    }
}

fn mock_client() -> (Client, tokio::io::DuplexStream) {
    let (client, server) = tokio::io::duplex(64 * 1024);
    let (read, write) = tokio::io::split(client);
    let (_tx, rx) = mpsc::channel(8);
    (
        Client::from_pipes(Box::new(read), Box::new(write), None, rx),
        server,
    )
}

#[test]
fn user_login_future_can_run_on_pager_task_executor() {
    fn assert_send<T: Send>(_: T) {}
    let launch = Launch {
        binary: PathBuf::from("/bin/false"),
        args: Vec::new(),
        opengrok_home: PathBuf::from("/tmp"),
        cwd: PathBuf::from("/tmp"),
    };
    let cancel = CancellationToken::new();
    assert_send(login_user(&launch, |_| {}, &cancel));
}

async fn recv<R: AsyncRead + Unpin>(reader: &mut BufReader<R>) -> Value {
    let mut line = String::new();
    reader.read_line(&mut line).await.unwrap();
    serde_json::from_str(&line).unwrap()
}
async fn send<W: AsyncWrite + Unpin>(writer: &mut W, value: Value) {
    writer
        .write_all(format!("{value}\n").as_bytes())
        .await
        .unwrap();
}
fn response(id: &Value, result: Value) -> Value {
    json!({"jsonrpc":"2.0", "id":id, "result":result})
}

#[tokio::test]
async fn oauth_model_streaming_and_questions_use_acp() {
    let (mut client, server) = mock_client();
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let init = recv(&mut read).await;
        assert_eq!(init["method"], "initialize");
        assert_eq!(init["params"]["clientInfo"]["name"], "zed");
        send(&mut write, response(&init["id"], json!({"protocolVersion":1,"authMethods":[{"id":"oauth-personal","name":"Google"}]}))).await;
        let auth = recv(&mut read).await;
        assert_eq!(auth["method"], "authenticate");
        assert_eq!(auth["params"]["methodId"], "oauth-personal");
        send(&mut write, response(&auth["id"], json!({}))).await;
        let session = recv(&mut read).await;
        assert_eq!(session["method"], "session/new");
        send(&mut write, response(&session["id"], json!({"sessionId":"s", "configOptions":[{"id":"model", "type":"select", "category":"model", "currentValue":"a", "options":[{"group":"models","name":"Models","options":[{"value":"a","name":"A"},{"value":"b","name":"B"}]}]}]}))).await;
        let select = recv(&mut read).await;
        assert_eq!(select["method"], "session/set_config_option");
        assert_eq!(select["params"]["value"], "b");
        send(&mut write, response(&select["id"], json!({"configOptions":[{"id":"model", "type":"select", "category":"model", "currentValue":"b", "options":[{"value":"b","name":"B"}]}]}))).await;
        let prompt = recv(&mut read).await;
        assert_eq!(prompt["method"], "session/prompt");
        send(&mut write, json!({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}})).await;
        send(&mut write, json!({"jsonrpc":"2.0","id":10,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"interaction_1"},"options":[]}})).await;
        let denied = recv(&mut read).await;
        assert_eq!(denied["result"]["outcome"]["outcome"], "cancelled");
        send(&mut write, json!({"jsonrpc":"2.0","id":11,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"tool_1"},"options":[]}})).await;
        let denied = recv(&mut read).await;
        assert_eq!(denied["result"]["outcome"]["outcome"], "cancelled");
        send(
            &mut write,
            response(&prompt["id"], json!({"stopReason":"end_turn"})),
        )
        .await;
    });
    let mut host = TestHost::new();
    let cancel = CancellationToken::new();
    client
        .initialize(&mut host, json!({}), &cancel)
        .await
        .unwrap();
    client.authenticate(&mut host, &cancel).await.unwrap();
    client
        .new_session(&mut host, Path::new("/tmp"), &cancel)
        .await
        .unwrap();
    assert!(matches!(
        client
            .select_model(&mut host, "not-advertised", &cancel)
            .await,
        Err(Error::ModelUnavailable)
    ));
    client.select_model(&mut host, "b", &cancel).await.unwrap();
    assert_eq!(
        client.prompt(&mut host, "hello", &cancel).await.unwrap()["stopReason"],
        "end_turn"
    );
    assert_eq!(host.updates.len(), 1);
    assert_eq!(host.questions, 1);
    assert_eq!(host.permissions, 1);
    server.await.unwrap();
}

#[tokio::test]
async fn cancellation_sends_session_cancel_and_ignores_late_success() {
    let (mut client, server) = mock_client();
    client.session_id = Some("s".into());
    client.selected_model = Some("a".into());
    let cancel = CancellationToken::new();
    let signal = cancel.clone();
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let prompt = recv(&mut read).await;
        signal.cancel();
        let notification = recv(&mut read).await;
        assert_eq!(notification["method"], "session/cancel");
        assert_eq!(notification["params"]["sessionId"], "s");
        send(
            &mut write,
            response(&prompt["id"], json!({"stopReason":"end_turn"})),
        )
        .await;
    });
    let result = client.prompt(&mut TestHost::new(), "x", &cancel).await;
    assert!(matches!(result, Err(Error::Cancelled)));
    server.await.unwrap();
}

#[tokio::test]
async fn cancellation_preserves_a_partially_read_response() {
    let (mut client, server) = mock_client();
    client.session_id = Some("s".into());
    client.selected_model = Some("a".into());
    let cancel = CancellationToken::new();
    let signal = cancel.clone();
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let prompt = recv(&mut read).await;
        let serialized = format!(
            "{}\n",
            response(&prompt["id"], json!({"stopReason":"end_turn"}))
        );
        let split = serialized.len() / 2;
        write
            .write_all(&serialized.as_bytes()[..split])
            .await
            .unwrap();
        // Give the client time to consume the first half before cancellation.
        tokio::task::yield_now().await;
        signal.cancel();
        assert_eq!(recv(&mut read).await["method"], "session/cancel");
        write
            .write_all(&serialized.as_bytes()[split..])
            .await
            .unwrap();
    });
    let result = client.prompt(&mut TestHost::new(), "x", &cancel).await;
    assert!(matches!(result, Err(Error::Cancelled)));
    server.await.unwrap();
}

#[cfg(unix)]
#[tokio::test]
async fn shutdown_reaps_the_owned_server_process() {
    let root = tempfile::tempdir().unwrap();
    let mut client = Client::spawn_inner(
        &Launch {
            binary: PathBuf::from("/bin/sh"),
            args: vec!["-c".into(), "exec sleep 30".into()],
            opengrok_home: root.path().to_path_buf(),
            cwd: root.path().to_path_buf(),
        },
        Scope::Agent,
        None,
        None,
        |_| Ok(()), // This fixture only tests ownership/reaping.
    )
    .await
    .unwrap();
    client.shutdown().await;
    assert!(client.child.as_mut().unwrap().try_wait().unwrap().is_some());
}

#[cfg(unix)]
#[tokio::test]
async fn external_server_is_enrolled_in_the_root_process_scope() {
    let root = tempfile::tempdir().unwrap();
    let scope = xai_tty_utils::ProcessScope::new();
    let mut client = Client::spawn_inner(
        &Launch {
            binary: PathBuf::from("/bin/sh"),
            args: vec!["-c".into(), "sleep 30 & wait".into()],
            opengrok_home: root.path().to_path_buf(),
            cwd: root.path().to_path_buf(),
        },
        Scope::Agent,
        Some(&scope),
        None,
        |_| Ok(()),
    )
    .await
    .unwrap();
    scope.kill_all();
    client.shutdown().await;
    assert!(client.child.as_mut().unwrap().try_wait().unwrap().is_some());
}

#[test]
fn restricted_or_unresolved_parent_cannot_spawn() {
    let full = ParentAccess {
        yolo: true,
        no_policy_restrictions: true,
        no_sandbox: true,
        no_hooks: true,
        child_full_access: true,
    };
    assert!(full.authorize().is_ok());
    assert!(ParentAccess::default().authorize().is_err());
    assert!(
        ParentAccess {
            yolo: false,
            ..full
        }
        .authorize()
        .is_err()
    );
    assert!(
        ParentAccess {
            no_policy_restrictions: false,
            ..full
        }
        .authorize()
        .is_err()
    );
    assert!(
        ParentAccess {
            no_sandbox: false,
            ..full
        }
        .authorize()
        .is_err()
    );
    assert!(
        ParentAccess {
            no_hooks: false,
            ..full
        }
        .authorize()
        .is_err()
    );
    assert!(
        ParentAccess {
            child_full_access: false,
            ..full
        }
        .authorize()
        .is_err()
    );
}

#[tokio::test]
async fn login_only_client_cannot_start_or_resume_agent_work() {
    let (mut client, _server) = mock_client();
    client.scope = Scope::Login;
    client.can_load_session = true;
    client.session_id = Some("s".into());
    client.selected_model = Some("model".into());
    let mut host = TestHost::new();
    let cancel = CancellationToken::new();
    assert!(matches!(
        client
            .new_session(&mut host, Path::new("/tmp"), &cancel)
            .await,
        Err(Error::RestrictedParent)
    ));
    assert!(matches!(
        client
            .load_session(&mut host, "s", Path::new("/tmp"), &cancel)
            .await,
        Err(Error::RestrictedParent)
    ));
    assert!(matches!(
        client.select_model(&mut host, "model", &cancel).await,
        Err(Error::RestrictedParent)
    ));
    assert!(matches!(
        client.prompt(&mut host, "x", &cancel).await,
        Err(Error::RestrictedParent)
    ));
}

#[tokio::test]
async fn yolo_permission_host_cannot_answer_a_question_implicitly() {
    struct AutoAllow;
    #[async_trait]
    impl Host for AutoAllow {
        async fn request(&mut self, _method: &str, _params: &Value) -> Option<Value> {
            Some(json!({"outcome":{"outcome":"selected","optionId":"allow"}}))
        }
    }
    let (mut client, server) = mock_client();
    client.session_id = Some("s".into());
    client.selected_model = Some("a".into());
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let prompt = recv(&mut read).await;
        send(&mut write, json!({"jsonrpc":"2.0", "id":7, "method":"session/request_permission", "params":{"toolCall":{"toolCallId":"interaction_question"}}})).await;
        assert_eq!(
            recv(&mut read).await["result"]["outcome"]["outcome"],
            "cancelled"
        );
        send(&mut write, json!({"jsonrpc":"2.0", "id":8, "method":"session/request_permission", "params":{"toolCall":{"toolCallId":"tool_normal"}}})).await;
        assert_eq!(
            recv(&mut read).await["result"]["outcome"]["outcome"],
            "selected"
        );
        send(
            &mut write,
            response(&prompt["id"], json!({"stopReason":"end_turn"})),
        )
        .await;
    });
    client
        .prompt(&mut AutoAllow, "x", &CancellationToken::new())
        .await
        .unwrap();
    server.await.unwrap();
}

#[tokio::test]
async fn model_switch_notification_fails_closed() {
    let (mut client, server) = mock_client();
    client.session_id = Some("s".into());
    client.selected_model = Some("a".into());
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let _prompt = recv(&mut read).await;
        send(&mut write, json!({"jsonrpc":"2.0","method":"session/update", "params":{"sessionId":"s", "update":{"sessionUpdate":"config_option_update", "configOptions":[{"id":"model", "type":"select", "category":"model", "currentValue":"b", "options":[{"value":"b","name":"B"}]}]}}})).await;
        let cancel = recv(&mut read).await;
        assert_eq!(cancel["method"], "session/cancel");
    });
    let result = client
        .prompt(&mut TestHost::new(), "x", &CancellationToken::new())
        .await;
    assert!(matches!(result, Err(Error::ModelUnavailable)));
    server.await.unwrap();
}

#[tokio::test]
async fn load_and_logout_follow_advertised_capabilities() {
    let (mut client, server) = mock_client();
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let init = recv(&mut read).await;
        send(
            &mut write,
            response(
                &init["id"],
                json!({"protocolVersion":1,
            "agentCapabilities":{"loadSession":true,"auth":{"logout":{}}},"authMethods":[]}),
            ),
        )
        .await;
        let load = recv(&mut read).await;
        assert_eq!(load["method"], "session/load");
        assert_eq!(load["params"]["sessionId"], "persisted");
        send(
            &mut write,
            response(&load["id"], json!({"configOptions":[]})),
        )
        .await;
        let logout = recv(&mut read).await;
        assert_eq!(logout["method"], "logout");
        send(&mut write, response(&logout["id"], json!({}))).await;
    });
    let cancel = CancellationToken::new();
    let mut host = TestHost::new();
    assert!(matches!(
        client
            .load_session(&mut host, "persisted", Path::new("/tmp"), &cancel)
            .await,
        Err(Error::Unsupported)
    ));
    client
        .initialize(&mut host, json!({}), &cancel)
        .await
        .unwrap();
    client
        .load_session(&mut host, "persisted", Path::new("/tmp"), &cancel)
        .await
        .unwrap();
    assert_eq!(client.session_id.as_deref(), Some("persisted"));
    client.logout(&mut host, &cancel).await.unwrap();
    assert!(client.session_id.is_none());
    server.await.unwrap();
}

#[tokio::test]
async fn rpc_error_does_not_expose_server_supplied_message() {
    let (mut client, server) = mock_client();
    let server = tokio::spawn(async move {
        let (read, mut write) = tokio::io::split(server);
        let mut read = BufReader::new(read);
        let init = recv(&mut read).await;
        send(&mut write, json!({"jsonrpc":"2.0","id":init["id"],"error":{"code":-32000,"message":"sensitive-agent-output"}})).await;
    });
    let error = client
        .initialize(&mut TestHost::new(), json!({}), &CancellationToken::new())
        .await
        .unwrap_err();
    assert!(!error.to_string().contains("sensitive-agent-output"));
    server.await.unwrap();
}

#[tokio::test]
async fn oauth_stderr_only_forwards_valid_google_authorization_urls() {
    let (mut write, read) = tokio::io::duplex(4096);
    let (tx, mut rx) = mpsc::channel(8);
    let task = tokio::spawn(read_oauth_urls(read, tx));
    write.write_all(b"other output with secrets\nOpen the following link to authenticate the ACP server: https://accounts.google.com.evil.example/x\nOpen the following link to authenticate the ACP server: https://accounts.google.com/o/oauth2/auth?state=test\n").await.unwrap();
    drop(write);
    task.await.unwrap();
    assert_eq!(
        rx.recv().await.as_deref(),
        Some("https://accounts.google.com/o/oauth2/auth?state=test")
    );
    assert!(rx.recv().await.is_none());
}

#[test]
fn isolated_profile_preserves_existing_configuration() {
    let root = tempfile::tempdir().unwrap();
    let profile = prepare_profile(root.path()).unwrap();
    assert!(profile.starts_with(root.path()));
    let settings = profile.join("antigravity-acp/settings.json");
    assert_eq!(
        std::fs::read_to_string(&settings).unwrap(),
        "{\"auth\":{\"type\":\"oauth-personal\"}}\n"
    );
    std::fs::write(&settings, "existing config").unwrap();
    prepare_profile(root.path()).unwrap();
    assert_eq!(
        std::fs::read_to_string(&settings).unwrap(),
        "existing config"
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            std::fs::metadata(settings).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }
}

use super::*;
use crate::session::acp_session::support::plain_actor;
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, AtomicUsize, Ordering},
};
use std::time::Duration;
use xai_grok_mcp::acp_transport::AcpReverseInvoker;
use xai_grok_mcp::servers::{McpClient, McpClientEvent};

struct ChangingServer {
    tools: Mutex<Vec<serde_json::Value>>,
    fail_list: AtomicBool,
    list_count: AtomicUsize,
    list_gate: Mutex<Option<Arc<tokio::sync::Semaphore>>>,
    calls: Mutex<Vec<String>>,
}

#[async_trait::async_trait]
impl AcpReverseInvoker for ChangingServer {
    async fn invoke(
        &self,
        _server_id: &str,
        message: serde_json::Value,
        _timeout: Duration,
    ) -> Result<serde_json::Value, String> {
        let result = match message["method"].as_str().unwrap_or_default() {
            "initialize" => serde_json::json!({
                "protocolVersion": message["params"]["protocolVersion"],
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "changing", "version": "1"}
            }),
            "tools/list" => {
                let tools = self.tools.lock().unwrap().clone();
                self.list_count.fetch_add(1, Ordering::SeqCst);
                let gate = self.list_gate.lock().unwrap().take();
                if let Some(gate) = gate {
                    gate.acquire().await.unwrap().forget();
                }
                if self.fail_list.load(Ordering::SeqCst) {
                    return Err("list failed".to_string());
                }
                serde_json::json!({"tools": tools})
            }
            "tools/call" => {
                self.calls
                    .lock()
                    .unwrap()
                    .push(message["params"]["name"].as_str().unwrap().to_string());
                serde_json::json!({"content": [{"type": "text", "text": "ok"}], "isError": false})
            }
            method => return Err(format!("unsupported method {method}")),
        };
        Ok(serde_json::json!({"jsonrpc": "2.0", "id": message["id"], "result": result}))
    }
}

fn tool(name: &str, parameter: &str) -> serde_json::Value {
    serde_json::json!({
        "name": name,
        "description": format!("{name} tool"),
        "inputSchema": {"type": "object", "properties": {parameter: {"type": "string"}}}
    })
}

async fn wait_until(mut condition: impl FnMut() -> bool) {
    tokio::time::timeout(Duration::from_secs(3), async {
        while !condition() {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .expect("catalog refresh completed");
}

#[tokio::test(flavor = "current_thread")]
async fn list_changed_reconciles_definitions_snapshot_and_disabled_tools() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let server = Arc::new(ChangingServer {
                tools: Mutex::new(vec![tool("a", "old"), tool("removed", "x")]),
                fail_list: AtomicBool::new(false),
                list_count: AtomicUsize::new(0),
                list_gate: Mutex::new(None),
                calls: Mutex::new(Vec::new()),
            });
            let actor = Arc::new(plain_actor().await);
            let client = Arc::new(McpClient::new_acp(
                "demo".into(),
                "test-server".into(),
                server.clone(),
                None,
                None,
            ));
            let (tx, rx) = tokio::sync::mpsc::unbounded_channel();
            {
                let mut state = actor.mcp_state.lock().await;
                state
                    .disabled_tools
                    .entry("demo".into())
                    .or_default()
                    .insert("hidden".into());
                state.owned_clients.insert("demo".into(), client.clone());
                state.set_client_event_tx(Some(tx.clone()));
            }
            let callback: std::rc::Rc<dyn crate::session::mcp_dispatcher::McpCatalogRefresh> =
                std::rc::Rc::new(Arc::downgrade(&actor));
            let dispatcher =
                tokio::task::spawn_local(crate::session::mcp_dispatcher::run_dispatcher(
                    "test-actor".into(),
                    rx,
                    actor.notifications.gateway.clone(),
                    actor.mcp_state.clone(),
                    crate::session::mcp_dispatcher::new_shutdown_state(),
                    None,
                    Some(callback),
                    std::path::PathBuf::from("."),
                ));
            let bridge = actor.agent.borrow().tool_bridge().clone();
            tx.send(McpClientEvent::ToolsChanged {
                server: "demo".into(),
            })
            .unwrap();
            wait_until(|| {
                bridge
                    .toolset()
                    .tool_definitions()
                    .iter()
                    .any(|d| d.function.name == "demo__removed")
            })
            .await;
            // A live child has its own bridge but shares this client.
            let child = Arc::new(plain_actor().await);
            {
                let mut state = child.mcp_state.lock().await;
                state.shared_clients.insert("demo".into(), client.clone());
                state
                    .disabled_tools
                    .entry("demo".into())
                    .or_default()
                    .insert("hidden".into());
            }
            let child_listeners = child.start_shared_mcp_catalog_listeners().await;
            child.register_shared_client_tools().await;
            let child_bridge = child.agent.borrow().tool_bridge().clone();
            assert!(
                child_bridge
                    .toolset()
                    .tool_definitions()
                    .iter()
                    .any(|d| d.function.name == "demo__removed")
            );
            *server.tools.lock().unwrap() =
                vec![tool("a", "new"), tool("b", "y"), tool("hidden", "z")];
            // The handler sends this revision on notifications/tools/list_changed.
            client.notify_catalog_changed();
            tx.send(McpClientEvent::ToolsChanged {
                server: "demo".into(),
            })
            .unwrap();
            wait_until(|| {
                let snapshot = child.tool_metadata_snapshot.lock().unwrap();
                snapshot.tools.iter().any(|t| t.qualified_name == "demo__b")
            })
            .await;
            let child_definitions = child_bridge.toolset().tool_definitions();
            let child_a = child_definitions
                .iter()
                .find(|d| d.function.name == "demo__a")
                .unwrap();
            assert!(
                child_a.function.parameters["properties"]
                    .get("new")
                    .is_some()
            );
            assert!(
                !child_definitions.iter().any(
                    |d| d.function.name == "demo__removed" || d.function.name == "demo__hidden"
                )
            );
            assert!(
                child
                    .mcp_state
                    .lock()
                    .await
                    .disabled_tool_registrations
                    .contains_key("demo__hidden")
            );
            wait_until(|| {
                let snapshot = actor.tool_metadata_snapshot.lock().unwrap();
                snapshot.tools.iter().any(|t| t.qualified_name == "demo__b")
            })
            .await;
            let definitions = bridge.toolset().tool_definitions();
            let a = definitions
                .iter()
                .find(|d| d.function.name == "demo__a")
                .unwrap();
            assert!(a.function.parameters["properties"].get("new").is_some());
            assert!(a.function.parameters["properties"].get("old").is_none());
            assert!(definitions.iter().any(|d| d.function.name == "demo__b"));
            assert!(
                !definitions.iter().any(
                    |d| d.function.name == "demo__removed" || d.function.name == "demo__hidden"
                )
            );
            assert!(
                actor
                    .mcp_state
                    .lock()
                    .await
                    .disabled_tool_registrations
                    .contains_key("demo__hidden")
            );
            assert!(
                actor
                    .tool_metadata_snapshot
                    .lock()
                    .unwrap()
                    .tools
                    .iter()
                    .all(|t| t.qualified_name != "demo__removed")
            );
            bridge
                .call("demo__b", serde_json::json!({"y": "ok"}), "call-1")
                .await
                .expect("new tool dispatches");
            assert_eq!(server.calls.lock().unwrap().as_slice(), &["b"]);

            server.fail_list.store(true, Ordering::SeqCst);
            actor.refresh_changed_mcp_catalog("demo").await;
            assert!(
                bridge
                    .toolset()
                    .tool_definitions()
                    .iter()
                    .any(|d| d.function.name == "demo__b"),
                "a failed list keeps the last complete catalog"
            );
            // An old client's delayed list must not overwrite the replacement.
            server.fail_list.store(false, Ordering::SeqCst);
            *server.tools.lock().unwrap() = vec![tool("stale", "x")];
            let gate = Arc::new(tokio::sync::Semaphore::new(0));
            *server.list_gate.lock().unwrap() = Some(gate.clone());
            let count = server.list_count.load(Ordering::SeqCst);
            let old_actor = actor.clone();
            let old_refresh = tokio::task::spawn_local(async move {
                old_actor.refresh_changed_mcp_catalog("demo").await;
            });
            wait_until(|| server.list_count.load(Ordering::SeqCst) > count).await;
            let replacement_server = Arc::new(ChangingServer {
                tools: Mutex::new(vec![tool("replacement", "value")]),
                fail_list: AtomicBool::new(false),
                list_count: AtomicUsize::new(0),
                list_gate: Mutex::new(None),
                calls: Mutex::new(Vec::new()),
            });
            let replacement = Arc::new(McpClient::new_acp(
                "demo".into(),
                "replacement".into(),
                replacement_server,
                None,
                None,
            ));
            actor
                .mcp_state
                .lock()
                .await
                .owned_clients
                .insert("demo".into(), replacement);
            actor.refresh_changed_mcp_catalog("demo").await;
            gate.add_permits(1);
            old_refresh.await.unwrap();
            let definitions = bridge.toolset().tool_definitions();
            assert!(
                definitions
                    .iter()
                    .any(|d| d.function.name == "demo__replacement")
            );
            assert!(!definitions.iter().any(|d| d.function.name == "demo__stale"));
            drop(child_listeners);
            actor.mcp_state.lock().await.set_client_event_tx(None);
            drop(tx);
            dispatcher.abort();
        })
        .await;
}

#[tokio::test(flavor = "current_thread")]
async fn in_place_recovery_retries_a_delayed_catalog_without_another_notification() {
    tokio::task::LocalSet::new()
        .run_until(async {
            let server = Arc::new(ChangingServer {
                tools: Mutex::new(vec![tool("old", "x")]),
                fail_list: AtomicBool::new(false),
                list_count: AtomicUsize::new(0),
                list_gate: Mutex::new(None),
                calls: Mutex::new(Vec::new()),
            });
            let actor = Arc::new(plain_actor().await);
            let client = Arc::new(McpClient::new_acp(
                "demo".into(),
                "test-server".into(),
                server.clone(),
                None,
                None,
            ));
            actor
                .mcp_state
                .lock()
                .await
                .owned_clients
                .insert("demo".into(), client.clone());
            actor.refresh_changed_mcp_catalog("demo").await;
            let gate = Arc::new(tokio::sync::Semaphore::new(0));
            *server.list_gate.lock().unwrap() = Some(gate.clone());
            let count = server.list_count.load(Ordering::SeqCst);
            let old_actor = actor.clone();
            let listing = tokio::task::spawn_local(async move {
                old_actor.refresh_changed_mcp_catalog("demo").await;
            });
            wait_until(|| server.list_count.load(Ordering::SeqCst) > count).await;
            *server.tools.lock().unwrap() = vec![tool("new", "y")];
            client.recover().await.expect("replacement service");
            gate.add_permits(1);
            listing.await.unwrap();
            let bridge = actor.agent.borrow().tool_bridge().clone();
            let definitions = bridge.toolset().tool_definitions();
            assert!(definitions.iter().any(|d| d.function.name == "demo__new"));
            assert!(!definitions.iter().any(|d| d.function.name == "demo__old"));
            assert!(server.list_count.load(Ordering::SeqCst) >= count + 2);
        })
        .await;
}

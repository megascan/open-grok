//! Explicit, fail-closed rendering and decisions for sandbox violation cards.
use agent_client_protocol as acp;
use serde_json::{Map, Value, json};

pub(crate) fn is_sandbox(req: &acp::RequestPermissionRequest) -> bool {
    req.meta
        .as_ref()
        .is_some_and(|m| m.contains_key("sandbox_violation"))
}

#[derive(Debug)]
struct Card {
    kind: String,
    value: Option<String>,
    description: Vec<String>,
    scopes: Vec<String>,
    ttl: Option<u64>,
}

// Reject controls, Unicode directionality and unbounded text rather than letting
// an untrusted peer alter the visible target or terminal. The original value
// is sent only when exactly the same bytes are safe to display.
fn safe(s: &str) -> Option<&str> {
    (!s.is_empty() && s.len() <= 1024 && s.bytes().all(|b| (b' '..=b'~').contains(&b))).then_some(s)
}

fn parse(req: &acp::RequestPermissionRequest) -> Option<Card> {
    let v = req.meta.as_ref()?.get("sandbox_violation")?;
    if v.get("schema_version")?.as_u64()? != 1
        || v.get("kind")?.as_str()? != "sandbox_violation"
        || v.pointer("/disposition/kind")?.as_str()? != "grantable"
    {
        return None;
    }
    let command = safe(v.get("bash_command")?.as_str()?)?;
    let blocked_kind = v.pointer("/blocked/kind")?.as_str()?;
    let subject = v.pointer("/proposed_grant/subject")?;
    let kind = subject.get("kind")?.as_str()?;
    let (value, target, blocked) = match (kind, blocked_kind) {
        ("fs_write_root", "fs_write") | ("fs_read", "fs_read") => {
            let root = safe(subject.get("root")?.as_str()?)?;
            let path = safe(v.pointer("/blocked/path")?.as_str()?)?;
            if !root.starts_with('/') || !path.starts_with('/') {
                return None;
            }
            (
                Some(root.to_owned()),
                format!(
                    "{} folder: {root:?}",
                    if kind == "fs_read" { "Read" } else { "Write" }
                ),
                format!("Blocked path: {path:?}"),
            )
        }
        ("net_host", "net") => {
            let host = safe(subject.get("host")?.as_str()?)?;
            let held = safe(v.pointer("/blocked/host")?.as_str()?)?;
            let port = v.pointer("/blocked/port").and_then(Value::as_u64);
            if port.is_some_and(|p| p > u16::MAX as u64) {
                return None;
            }
            (
                Some(host.to_owned()),
                format!(
                    "Network host: {host:?}, {}",
                    port.map(|p| format!("port {p}"))
                        .unwrap_or_else(|| "any port".to_owned())
                ),
                format!(
                    "Blocked host: {held:?}{}",
                    port.map(|p| format!(":{p}")).unwrap_or_default()
                ),
            )
        }
        ("build_caches", "fs_write") => {
            let path = safe(v.pointer("/blocked/path")?.as_str()?)?;
            if !path.starts_with('/') {
                return None;
            }
            (
                None,
                "Write to build caches under the user home:".to_owned(),
                format!("Blocked path: {path:?}"),
            )
        }
        _ => return None,
    };
    let confirm = v.get("proposal_confirm")?.as_bool()?;
    let replay = match v.get("replay")?.as_str()? {
        "rerun" => "Approval reruns the command",
        "resume" => "Approval resumes the blocked operation",
        _ => return None,
    };
    let scopes = v
        .get("offered_scopes")?
        .as_array()?
        .iter()
        .map(|v| v.as_str().map(str::to_owned))
        .collect::<Option<Vec<_>>>()?;
    if scopes
        .iter()
        .any(|s| !["call", "session", "workspace", "global"].contains(&s.as_str()))
    {
        return None;
    }
    let ttl = v
        .get("offered_expiries")?
        .as_array()?
        .iter()
        .filter_map(|e| {
            (e.get("kind")?.as_str()? == "ttl")
                .then(|| e.get("seconds")?.as_u64())
                .flatten()
                .filter(|n| *n > 0)
        })
        .min();
    let mut description = vec![format!("Command: {command:?}"), blocked, target];
    if kind == "build_caches" {
        description.extend(
            xai_grok_sandbox::command::BUILD_CACHE_TREES
                .iter()
                .map(|tree| format!("  ~/{tree}")),
        );
    }
    if confirm {
        description.push("Verify the proposed target; it was inferred.".to_owned());
    }
    description.push(replay.to_owned());
    Some(Card {
        kind: kind.to_owned(),
        value,
        description,
        scopes,
        ttl,
    })
}

pub(crate) fn display(req: &acp::RequestPermissionRequest) -> (String, Vec<String>) {
    match parse(req) {
        Some(card) => ("Sandbox blocked a command".into(), card.description),
        None => (
            "Sandbox request cannot be verified".into(),
            vec!["Only denial is available.".into()],
        ),
    }
}

pub(crate) fn options(req: &acp::RequestPermissionRequest) -> Vec<acp::PermissionOption> {
    let card = parse(req);
    let mut result = Vec::new();
    for id in [
        "sandbox:deny",
        "sandbox:call",
        "sandbox:session",
        "sandbox:workspace",
        "sandbox:global",
    ] {
        let Some(original) = req.options.iter().find(|o| o.option_id.0.as_ref() == id) else {
            continue;
        };
        let label = if id == "sandbox:deny" {
            "Deny".to_owned()
        } else {
            let Some(card) = card.as_ref() else { continue };
            let scope = id.trim_start_matches("sandbox:");
            if !card.scopes.iter().any(|s| s == scope) {
                continue;
            }
            match scope {
                "call" => "Allow this call".to_owned(),
                "session" => "Allow this session".to_owned(),
                "workspace" | "global" => {
                    let Some(ttl) = card.ttl else { continue };
                    format!("Allow {scope} for {ttl} seconds")
                }
                _ => continue,
            }
        };
        let expected = if id == "sandbox:deny" {
            acp::PermissionOptionKind::RejectOnce
        } else {
            acp::PermissionOptionKind::AllowOnce
        };
        if original.kind != expected {
            continue;
        }
        result.push(acp::PermissionOption::new(
            original.option_id.clone(),
            label,
            expected,
        ));
    }
    result
}

pub(crate) fn decision(
    req: &acp::RequestPermissionRequest,
    option: &acp::PermissionOptionId,
    shown: &[acp::PermissionOption],
) -> Map<String, Value> {
    let reject = |reason: bool| {
        if reason {
            json!({"outcome":"reject", "reason":"decode_failed"})
        } else {
            json!({"outcome":"reject"})
        }
    };
    let card = parse(req);
    let offered = shown.iter().any(|o| o.option_id == *option)
        && options(req).iter().any(|o| o.option_id == *option);
    let choice = option.0.as_ref();
    let answer = if choice == "sandbox:deny" && offered {
        reject(card.is_none())
    } else if let Some(card) = card.filter(|_| offered) {
        let duration = choice.trim_start_matches("sandbox:");
        if !choice.starts_with("sandbox:") || !card.scopes.iter().any(|s| s == duration) {
            reject(true)
        } else {
            let mut scope = json!({"kind":card.kind});
            if let Some(value) = card.value {
                scope["value"] = json!(value);
            }
            let mut answer =
                json!({"outcome":"approve", "scope":scope, "duration":{"kind":duration}});
            if matches!(duration, "workspace" | "global") {
                if let Some(ttl) = card.ttl {
                    answer["expires"] = json!({"kind":"ttl", "seconds":ttl});
                } else {
                    return json!({"sandbox_decision":reject(true)})
                        .as_object()
                        .unwrap()
                        .clone();
                }
            }
            answer
        }
    } else {
        reject(true)
    };
    json!({"sandbox_decision":answer})
        .as_object()
        .unwrap()
        .clone()
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::sync::Arc;

    pub(crate) fn request() -> acp::RequestPermissionRequest {
        let options = ["deny", "call", "session", "workspace", "global"]
            .into_iter()
            .map(|part| {
                acp::PermissionOption::new(
                    acp::PermissionOptionId::new(Arc::from(format!("sandbox:{part}"))),
                    "untrusted label",
                    if part == "deny" {
                        acp::PermissionOptionKind::RejectOnce
                    } else {
                        acp::PermissionOptionKind::AllowOnce
                    },
                )
            })
            .collect();
        let mut meta = Map::new();
        meta.insert("sandbox_violation".into(), json!({
            "schema_version":1, "kind":"sandbox_violation", "bash_command":"npm ci",
            "blocked":{"kind":"fs_write", "path":"/tmp/cache/package"},
            "disposition":{"kind":"grantable"},
            "proposed_grant":{"subject":{"kind":"fs_write_root", "root":"/tmp/cache"}},
            "proposal_confirm":true, "offered_scopes":["call","session","workspace"],
            "offered_expiries":[{"kind":"ttl","seconds":86400},{"kind":"ttl","seconds":3600}], "replay":"rerun"
        }));
        acp::RequestPermissionRequest::new(
            acp::SessionId::new(Arc::from("test-sess")),
            acp::ToolCallUpdate::new(
                acp::ToolCallId::new(Arc::from("call-1")),
                acp::ToolCallUpdateFields::default(),
            ),
            options,
        )
        .meta(Some(meta))
    }

    fn id(id: &str) -> acp::PermissionOptionId {
        acp::PermissionOptionId::new(Arc::from(id))
    }

    #[test]
    fn sandbox_explicit_scope_and_shortest_offered_ttl() {
        let req = request();
        let options = options(&req);
        assert_eq!(
            options
                .iter()
                .map(|o| o.option_id.0.as_ref())
                .collect::<Vec<_>>(),
            [
                "sandbox:deny",
                "sandbox:call",
                "sandbox:session",
                "sandbox:workspace"
            ]
        );
        assert!(
            display(&req)
                .1
                .join("\n")
                .contains("Write folder: \"/tmp/cache\"")
        );
        assert_eq!(
            decision(&req, &id("sandbox:workspace"), &options)["sandbox_decision"],
            json!({"outcome":"approve", "scope":{"kind":"fs_write_root", "value":"/tmp/cache"}, "duration":{"kind":"workspace"}, "expires":{"kind":"ttl", "seconds":3600}})
        );
        assert_eq!(
            decision(&req, &id("sandbox:deny"), &options)["sandbox_decision"],
            json!({"outcome":"reject"})
        );
        assert_eq!(
            decision(&req, &id("sandbox:global"), &options)["sandbox_decision"]["outcome"],
            "reject"
        );
    }

    #[test]
    fn sandbox_bad_or_unsafe_cards_are_deny_only() {
        for mutation in [
            json!({"schema_version":2}),
            json!({"bash_command":"printf \u{001b}[2J"}),
            json!({"proposed_grant":null}),
            json!({"blocked":{"kind":"net", "host":"elsewhere"}}),
        ] {
            let mut req = request();
            let card = req
                .meta
                .as_mut()
                .unwrap()
                .get_mut("sandbox_violation")
                .unwrap()
                .as_object_mut()
                .unwrap();
            for (k, v) in mutation.as_object().unwrap() {
                card.insert(k.clone(), v.clone());
            }
            let shown = options(&req);
            assert_eq!(shown.len(), 1);
            assert_eq!(shown[0].option_id.0.as_ref(), "sandbox:deny");
            assert_eq!(
                decision(&req, &id("sandbox:deny"), &shown)["sandbox_decision"],
                json!({"outcome":"reject", "reason":"decode_failed"})
            );
        }
    }

    #[test]
    fn sandbox_never_approves_unshown_or_unoffered_selection() {
        let req = request();
        assert_eq!(
            decision(&req, &id("sandbox:call"), &[])["sandbox_decision"]["outcome"],
            "reject"
        );
        let mut req = req;
        req.meta
            .as_mut()
            .unwrap()
            .get_mut("sandbox_violation")
            .unwrap()["offered_expiries"] = json!([]);
        assert!(
            !options(&req)
                .iter()
                .any(|o| o.option_id.0.as_ref() == "sandbox:workspace")
        );
    }
}

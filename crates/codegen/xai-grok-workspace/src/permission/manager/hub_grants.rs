//! Shared grant decisions for hub-mediated sandbox calls.
use super::*;

pub(crate) fn protected_target(
    access: &AccessKind,
    bash: Option<&BashEvaluation>,
    cwd: &std::path::Path,
) -> bool {
    match access {
        AccessKind::Edit(path) => {
            edit_target_protection(&resolve_model_path(cwd, None, path)).is_some()
        }
        AccessKind::Bash(_) => bash.is_some_and(|e| {
            (e.has_cwd_change
                && e.creation_paths
                    .iter()
                    .any(|p| !std::path::Path::new(p).is_absolute()))
                || e.creation_paths
                    .iter()
                    .any(|p| edit_target_protection(&resolve_model_path(cwd, None, p)).is_some())
        }),
        _ => false,
    }
}

pub(crate) fn session_grant_for_hub(
    access: &AccessKind,
    bash: Option<&BashEvaluation>,
    state: &PermissionState,
    allow_edits_for_session: bool,
    yolo_pin: Option<&'static str>,
) -> Option<(Decision, &'static str)> {
    if let AccessKind::Bash(cmd) = access {
        return bash_grant_pre_decision(
            cmd,
            bash?,
            state,
            yolo_pin,
            BashGrantOpts {
                // A hub approval ceiling permits remembered grants, not the
                // local classifier's built-in list of safe commands.
                honor_safe_lists: false,
                allow_blanket: true,
                conservative_blanket: true,
            },
        );
    }
    let matcher = DomainMatcher::new(&[]);
    session_grant_pre_decision(
        access,
        bash,
        state,
        allow_edits_for_session,
        &matcher,
        false,
        yolo_pin,
    )
}

pub(crate) fn record_prompt_outcome(
    state: &mut PermissionState,
    access: &AccessKind,
    outcome: &PromptOutcome,
) -> Option<String> {
    match (access, outcome) {
        (AccessKind::Bash(cmd), PromptOutcome::AllowAlways) => {
            state.allowed_bash_commands.insert(cmd.clone());
            state.allowed_bash_commands.extend(bash_grant_segments(cmd));
            Some(cmd.clone())
        }
        (AccessKind::Bash(cmd), PromptOutcome::AllowAlwaysBashCommand(prefix)) => {
            persist_bash_always_allow(state, cmd, prefix);
            Some(prefix.clone())
        }
        (AccessKind::Bash(cmd), PromptOutcome::AllowAlwaysBashGlob(pattern)) => {
            if !bash_glob_covers_script(cmd, pattern) {
                tracing::warn!(glob = %pattern, "always-allow glob does not match the prompted script; not persisted");
                return None;
            }
            state.allowed_bash_globs.insert(pattern.clone());
            Some(pattern.clone())
        }
        (AccessKind::Bash(_), PromptOutcome::RejectAlwaysBashCommand(prefix)) => {
            state.disallowed_bash_commands.insert(prefix.clone());
            Some(prefix.clone())
        }
        (AccessKind::MCPTool { name, .. }, PromptOutcome::AllowAlways) => {
            state.allowed_mcp_tools.insert(name.clone());
            Some(name.clone())
        }
        (AccessKind::MCPTool { name, .. }, PromptOutcome::AllowAlwaysMcpTool(client_name)) => {
            if client_name != name {
                tracing::warn!(client_supplied = %client_name, access_name = %name, "AllowAlwaysMcpTool tool_name mismatch; persisting access-kind name");
            }
            state.allowed_mcp_tools.insert(name.clone());
            Some(name.clone())
        }
        (AccessKind::MCPTool { name, .. }, PromptOutcome::AllowAlwaysMcpServer(server_prefix)) => {
            match parse_mcp_qualified_name(name).map(|(_, server, _)| server) {
                Some(server) if server == server_prefix => {
                    state.allowed_mcp_servers.insert(server.to_owned());
                    tracing::info!(%server, count = state.allowed_mcp_servers.len(), "added MCP server to session allowlist");
                    Some(server.to_owned())
                }
                _ => {
                    tracing::warn!(client_supplied = %server_prefix, access_name = %name, "AllowAlwaysMcpServer prefix mismatch; downgrading to tool-scope");
                    state.allowed_mcp_tools.insert(name.clone());
                    Some(name.clone())
                }
            }
        }
        (AccessKind::MCPTool { name, .. }, PromptOutcome::RejectAlwaysMcpTool(client_name)) => {
            if client_name != name {
                tracing::warn!(client_supplied = %client_name, access_name = %name, "RejectAlwaysMcpTool tool_name mismatch; persisting access-kind name");
            }
            state.disallowed_mcp_tools.insert(name.clone());
            Some(name.clone())
        }
        (AccessKind::WebFetch(url), PromptOutcome::AllowAlwaysDomain(client_domain)) => {
            let domain = url::Url::parse(url)
                .ok()
                .and_then(|parsed| parsed.host_str().map(normalize_domain))?;
            if domain != *client_domain {
                tracing::warn!(client_supplied = %client_domain, access_domain = %domain, "AllowAlwaysDomain mismatch; persisting access-URL domain");
            }
            state.allowed_web_fetch_domains.insert(domain.clone());
            Some(domain)
        }
        (AccessKind::WebFetch(url), PromptOutcome::RejectAlwaysDomain(client_domain)) => {
            let domain = web_fetch_deny_key_from_url(url)?;
            if domain != *client_domain {
                tracing::warn!(client_supplied = %client_domain, access_domain = %domain, "RejectAlwaysDomain mismatch; persisting access-URL domain");
            }
            state.disallowed_web_fetch_domains.insert(domain.clone());
            Some(domain)
        }
        _ => None,
    }
}

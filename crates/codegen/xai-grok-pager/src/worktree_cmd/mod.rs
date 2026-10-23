mod display;

use std::io::Write;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail};
use clap::Subcommand;
use tokio_util::sync::CancellationToken;
use xai_fast_worktree::WorktreeRecord;

use agent_client_protocol as acp;
use xai_acp_lib::acp_send;
use xai_grok_shell::agent::config::Config as AgentConfig;
use xai_tty_utils::{cli_eprintln as eprintln, cli_println as println};

/// Local response types matching the ACP response shapes.
#[derive(Debug, serde::Deserialize)]
pub struct GcReport {
    pub dead_removed: u64,
    pub expired_removed: u64,
    pub skipped_alive: u64,
    // serde(default) so reports from agents predating this field still parse.
    #[serde(default)]
    pub remove_failed: u64,
}

#[derive(Debug, serde::Deserialize)]
pub struct DbStats {
    pub total_records: u64,
    pub alive_count: u64,
    pub dead_count: u64,
    pub db_file_bytes: u64,
}

#[derive(Debug, serde::Deserialize)]
pub struct RebuildReport {
    pub discovered: u64,
    pub registered: u64,
    pub already_tracked: u64,
}

#[derive(Debug, clap::Args, Clone)]
pub struct WorktreeArgs {
    #[command(subcommand)]
    command: WorktreeCommand,
}

#[derive(Debug, Subcommand, Clone)]
enum WorktreeCommand {
    /// Create a worktree without starting a session
    Create {
        /// Worktree name; generated when omitted
        name: Option<String>,
        /// Branch, tag, or commit to base a clean worktree on
        #[arg(long = "ref", value_name = "REF")]
        git_ref: Option<String>,
    },
    /// List tracked worktrees
    #[command(visible_alias = "ls")]
    List {
        #[arg(long)]
        repo: Option<String>,
        #[arg(long, value_delimiter = ',')]
        r#type: Vec<String>,
        #[arg(long)]
        json: bool,
        #[arg(long)]
        all: bool,
    },
    /// Show details for a specific worktree
    Show { id_or_path: String },
    /// Remove worktrees
    Rm {
        #[arg(required = true)]
        ids: Vec<String>,
        #[arg(short, long)]
        force: bool,
        #[arg(long)]
        dry_run: bool,
    },
    /// Garbage-collect orphaned/stale worktrees
    #[command(alias = "prune")]
    Gc {
        #[arg(long)]
        dry_run: bool,
        #[arg(long)]
        max_age: Option<String>,
        #[arg(short, long)]
        force: bool,
    },
    /// Database maintenance
    Db {
        #[command(subcommand)]
        command: WorktreeDbCommand,
    },
}

#[derive(Debug, Subcommand, Clone)]
enum WorktreeDbCommand {
    /// Rebuild DB from filesystem scan
    Rebuild,
    /// Show DB statistics
    Stats,
    /// Print DB file path
    Path,
}

pub async fn run(args: WorktreeArgs, agent_config: &AgentConfig) -> Result<()> {
    let command = args.command;
    let cancel = CancellationToken::new();
    xai_grok_telemetry::startup::mark_utility_process();
    let spawned = crate::acp::spawn::spawn_grok_shell(agent_config.clone(), &cancel, None).await?;
    // Cancel + join on every return path, including the `?` below.
    let _agent_guard =
        crate::acp::spawn::AgentShutdownGuard::new(cancel.clone(), Some(spawned.thread_handle));

    let _init: acp::InitializeResponse = acp_send(
        acp::InitializeRequest::new(acp::ProtocolVersion::V1)
            .client_capabilities(
                acp::ClientCapabilities::new()
                    .fs(acp::FileSystemCapabilities::new())
                    .terminal(false),
            )
            .meta(
                serde_json::json!({
                    "clientType": crate::client_identity::HEADLESS_CLIENT_TYPE,
                    "clientVersion": crate::client_identity::pager_client_version()
                })
                .as_object()
                .cloned(),
            ),
        &spawned.channel.tx,
    )
    .await?;
    dispatch(command, &spawned.channel.tx).await
}

async fn dispatch(command: WorktreeCommand, tx: &xai_acp_lib::AcpAgentTx) -> Result<()> {
    match command {
        WorktreeCommand::Create { name, git_ref } => {
            let cwd = std::env::current_dir().context("couldn't read the current directory")?;
            cmd_create(
                tx,
                &cwd,
                name.as_deref(),
                git_ref.as_deref(),
                &mut std::io::stdout(),
            )
            .await
        }
        WorktreeCommand::List {
            repo,
            r#type,
            json,
            all,
        } => cmd_list(tx, repo, r#type, json, all).await,
        WorktreeCommand::Show { id_or_path } => cmd_show(tx, &id_or_path).await,
        WorktreeCommand::Rm {
            ids,
            force,
            dry_run,
        } => cmd_rm(tx, ids, force, dry_run).await,
        WorktreeCommand::Gc {
            dry_run,
            max_age,
            force,
        } => cmd_gc(tx, dry_run, max_age, force).await,
        WorktreeCommand::Db { command } => cmd_db(tx, command).await,
    }
}

fn ext_request<T: serde::Serialize>(
    method: &str,
    params: &T,
) -> Result<acp::ExtRequest, serde_json::Error> {
    let params = serde_json::value::to_raw_value(params)?;
    Ok(acp::ExtRequest::new(method, params.into()))
}

/// ACP extension responses are wrapped in `{ "result": T, "error": ... }`.
#[derive(serde::Deserialize)]
struct ExtEnvelope<T> {
    result: Option<T>,
    error: Option<serde_json::Value>,
}

async fn ext_call<T: serde::de::DeserializeOwned>(
    tx: &xai_acp_lib::AcpAgentTx,
    method: &str,
    params: &impl serde::Serialize,
) -> Result<T> {
    let req =
        ext_request(method, params).map_err(|e| anyhow::anyhow!("failed to build request: {e}"))?;
    let resp = acp_send(req, tx)
        .await
        .map_err(|e| anyhow::anyhow!("{e}"))?;
    let envelope: ExtEnvelope<T> = serde_json::from_str(resp.0.get())
        .map_err(|e| anyhow::anyhow!("response parse error: {e}"))?;
    if let Some(err) = envelope.error {
        bail!("ACP error: {err}");
    }
    envelope
        .result
        .ok_or_else(|| anyhow::anyhow!("ACP response missing result field"))
}

/// Print only the created directory to stdout so it can be passed to `cd`.
async fn cmd_create(
    tx: &xai_acp_lib::AcpAgentTx,
    cwd: &Path,
    name: Option<&str>,
    git_ref: Option<&str>,
    out: &mut impl Write,
) -> Result<()> {
    let id = uuid::Uuid::new_v4().simple().to_string();
    let mut params = serde_json::json!({
        "sourceWorktreePath": cwd.to_string_lossy(),
        "newSessionId": format!("pager-{}", &id[..12]),
        "copyMode": if git_ref.is_some() { "clean" } else { "dirty" },
    });
    if let Some(name) = name {
        params["label"] = name.into();
    }
    if let Some(git_ref) = git_ref {
        params["gitRef"] = git_ref.into();
    }
    let result: serde_json::Value =
        ext_call(tx, "x.ai/git/worktree/create_from_worktree_sync", &params)
            .await
            .context("couldn't create worktree")?;
    let root = result
        .get("worktreePath")
        .and_then(serde_json::Value::as_str)
        .map(PathBuf::from)
        .context("couldn't create worktree: response missing worktreePath")?;
    let session_cwd = result
        .get("sourceGitRoot")
        .and_then(serde_json::Value::as_str)
        .and_then(|source| cwd.strip_prefix(source).ok())
        .map(|relative| root.join(relative))
        .unwrap_or_else(|| root.clone());
    let target = if session_cwd.is_dir() {
        &session_cwd
    } else {
        xai_tty_utils::cli_output::ignore_broken_pipe(writeln!(
            std::io::stderr(),
            "{} is not in the new worktree; printing the worktree root",
            session_cwd.display()
        ))?;
        &root
    };
    Ok(crate::util::ignore_broken_pipe(writeln!(
        out,
        "{}",
        target.display()
    ))?)
}

async fn cmd_list(
    tx: &xai_acp_lib::AcpAgentTx,
    repo: Option<String>,
    types: Vec<String>,
    json: bool,
    all: bool,
) -> Result<()> {
    let records: Vec<WorktreeRecord> = ext_call(
        tx,
        "x.ai/git/worktree/list",
        &serde_json::json!({
            "repo": repo,
            "type": types,
            "includeAll": all,
        }),
    )
    .await?;

    let mut out = std::io::stdout().lock();
    let written = if json {
        display::print_json(&records, &mut out)
    } else {
        display::print_table(&records, &mut out)
    };
    Ok(crate::util::ignore_broken_pipe(written)?)
}

async fn cmd_show(tx: &xai_acp_lib::AcpAgentTx, id_or_path: &str) -> Result<()> {
    let result: Result<Option<WorktreeRecord>> = ext_call(
        tx,
        "x.ai/git/worktree/show",
        &serde_json::json!({ "idOrPath": id_or_path }),
    )
    .await;
    let rec = result?;
    match rec {
        Some(r) => {
            let redirections_bytes = None;
            let written =
                display::print_show(&r, redirections_bytes, &mut std::io::stdout().lock());
            Ok(crate::util::ignore_broken_pipe(written)?)
        }
        None => bail!("worktree not found: {id_or_path}"),
    }
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
struct RemoveResponse {
    removed: bool,
    #[serde(default)]
    resolved_path: Option<String>,
}

async fn cmd_rm(
    tx: &xai_acp_lib::AcpAgentTx,
    ids: Vec<String>,
    force: bool,
    dry_run: bool,
) -> Result<()> {
    let mut failed = false;
    for id_or_path in &ids {
        let resp: Result<RemoveResponse> = ext_call(
            tx,
            "x.ai/git/worktree/remove",
            &serde_json::json!({
                "idOrPath": id_or_path,
                "force": force,
                "dryRun": dry_run,
            }),
        )
        .await;

        match resp {
            Ok(r) => {
                let path = r.resolved_path.as_deref().unwrap_or(id_or_path);
                if dry_run {
                    println!("  would remove: {path}");
                } else if r.removed {
                    println!("  removed: {path}");
                }
            }
            Err(e) => {
                failed = true;
                eprintln!("  error removing {id_or_path}: {e}");
            }
        }
    }
    if failed {
        bail!("one or more worktrees could not be removed");
    }
    Ok(())
}

async fn cmd_gc(
    tx: &xai_acp_lib::AcpAgentTx,
    dry_run: bool,
    max_age: Option<String>,
    force: bool,
) -> Result<()> {
    let report: GcReport = ext_call(
        tx,
        "x.ai/git/worktree/gc",
        &serde_json::json!({
            "dryRun": dry_run,
            "maxAge": max_age,
            "force": force,
        }),
    )
    .await?;

    let mut out = std::io::stdout().lock();
    let written = (|| {
        if dry_run {
            writeln!(out, "Dry run \u{2014} no changes made.")?;
        }
        display::print_gc(&report, &mut out)
    })();
    Ok(crate::util::ignore_broken_pipe(written)?)
}

async fn cmd_db(tx: &xai_acp_lib::AcpAgentTx, command: WorktreeDbCommand) -> Result<()> {
    match command {
        WorktreeDbCommand::Stats => {
            let stats: DbStats = ext_call(tx, "x.ai/git/worktree/db/stats", &()).await?;
            let written = display::print_stats(&stats, &mut std::io::stdout().lock());
            Ok(crate::util::ignore_broken_pipe(written)?)
        }
        WorktreeDbCommand::Path => {
            #[derive(serde::Deserialize)]
            struct PathResp {
                path: String,
            }
            let resp: PathResp = ext_call(tx, "x.ai/git/worktree/db/path", &()).await?;
            println!("{}", resp.path);
            Ok(())
        }
        WorktreeDbCommand::Rebuild => {
            let report: RebuildReport = ext_call(tx, "x.ai/git/worktree/db/rebuild", &()).await?;
            let written = display::print_rebuild(&report, &mut std::io::stdout().lock());
            Ok(crate::util::ignore_broken_pipe(written)?)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn remove_errors_produce_failure_status() {
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpAgentMessage>();
        tokio::spawn(async move {
            let xai_acp_lib::AcpAgentMessage::ExtMethod(args) = rx.recv().await.unwrap() else {
                panic!("expected extension request");
            };
            let reply = serde_json::value::to_raw_value(&serde_json::json!({
                "result": null, "error": "worktree is in use by a running process"
            }))
            .unwrap();
            args.response_tx
                .send(Ok(acp::ExtResponse::new(std::sync::Arc::from(reply))))
                .unwrap();
        });
        assert!(
            cmd_rm(&tx, vec!["active".into()], true, false)
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn create_sends_ref_and_prints_the_matching_subdirectory() {
        let source = tempfile::tempdir().unwrap();
        let cwd = source.path().join("nested");
        std::fs::create_dir_all(&cwd).unwrap();
        let worktree = tempfile::tempdir().unwrap();
        let nested = worktree.path().join("nested");
        std::fs::create_dir_all(&nested).unwrap();
        let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel::<xai_acp_lib::AcpAgentMessage>();
        let (seen_tx, mut seen_rx) = tokio::sync::mpsc::unbounded_channel();
        let source_root = source.path().to_path_buf();
        let worktree_root = worktree.path().to_path_buf();
        tokio::spawn(async move {
            let xai_acp_lib::AcpAgentMessage::ExtMethod(args) = rx.recv().await.unwrap() else {
                panic!("expected extension request");
            };
            let params: serde_json::Value =
                serde_json::from_str(args.request.params.get()).unwrap();
            seen_tx
                .send((args.request.method.to_string(), params))
                .unwrap();
            let reply = serde_json::value::to_raw_value(&serde_json::json!({"result": {
                "worktreePath": worktree_root, "sourceGitRoot": source_root,
            }}))
            .unwrap();
            args.response_tx
                .send(Ok(acp::ExtResponse::new(std::sync::Arc::from(reply))))
                .unwrap();
        });
        let mut out = Vec::new();
        cmd_create(&tx, &cwd, Some("fix"), Some("main"), &mut out)
            .await
            .unwrap();
        let (method, params) = seen_rx.recv().await.unwrap();
        assert_eq!(method, "x.ai/git/worktree/create_from_worktree_sync");
        assert_eq!(params["label"], "fix");
        assert_eq!(params["gitRef"], "main");
        assert_eq!(params["copyMode"], "clean");
        assert!(
            params["newSessionId"]
                .as_str()
                .unwrap()
                .starts_with("pager-")
        );
        assert_eq!(
            String::from_utf8(out).unwrap(),
            format!("{}\n", nested.display())
        );
    }

    #[test]
    fn create_parses_optional_name_and_ref() {
        use clap::Parser;
        #[derive(Parser)]
        struct Cli {
            #[command(subcommand)]
            command: WorktreeCommand,
        }
        let cli = Cli::parse_from(["test", "create", "fix", "--ref", "main"]);
        assert!(
            matches!(cli.command, WorktreeCommand::Create { name: Some(name), git_ref: Some(git_ref) } if name == "fix" && git_ref == "main")
        );
        let cli = Cli::parse_from(["test", "create"]);
        assert!(matches!(
            cli.command,
            WorktreeCommand::Create {
                name: None,
                git_ref: None
            }
        ));
    }

    #[test]
    fn ext_request_builds_list_with_filters() {
        let req = ext_request(
            "x.ai/git/worktree/list",
            &serde_json::json!({
                "repo": "xai",
                "type": ["session"],
                "includeAll": true,
            }),
        )
        .unwrap();
        assert_eq!(req.method.as_ref(), "x.ai/git/worktree/list");
        let params: serde_json::Value = serde_json::from_str(req.params.get()).unwrap();
        assert_eq!(params["repo"], "xai");
        assert_eq!(params["includeAll"], true);
    }

    #[test]
    fn ext_request_builds_gc_with_max_age_string() {
        let req = ext_request(
            "x.ai/git/worktree/gc",
            &serde_json::json!({
                "dryRun": true,
                "maxAge": "7d",
                "force": false,
            }),
        )
        .unwrap();
        let params: serde_json::Value = serde_json::from_str(req.params.get()).unwrap();
        assert_eq!(params["maxAge"], "7d");
        assert_eq!(params["dryRun"], true);
    }

    #[test]
    fn ext_request_builds_remove_with_id_or_path() {
        let req = ext_request(
            "x.ai/git/worktree/remove",
            &serde_json::json!({
                "idOrPath": "wt-abc123",
                "force": true,
                "dryRun": false,
            }),
        )
        .unwrap();
        let params: serde_json::Value = serde_json::from_str(req.params.get()).unwrap();
        assert_eq!(params["idOrPath"], "wt-abc123");
    }

    #[test]
    fn ext_request_builds_show() {
        let req = ext_request(
            "x.ai/git/worktree/show",
            &serde_json::json!({ "idOrPath": "/some/path" }),
        )
        .unwrap();
        let params: serde_json::Value = serde_json::from_str(req.params.get()).unwrap();
        assert_eq!(params["idOrPath"], "/some/path");
    }

    #[test]
    fn ext_request_builds_db_stats_empty_params() {
        let req = ext_request("x.ai/git/worktree/db/stats", &()).unwrap();
        assert_eq!(req.method.as_ref(), "x.ai/git/worktree/db/stats");
    }

    #[test]
    fn remove_response_deserializes_with_resolved_path() {
        let json = r#"{"removed": true, "resolvedPath": "/resolved"}"#;
        let resp: RemoveResponse = serde_json::from_str(json).unwrap();
        assert!(resp.removed);
        assert_eq!(resp.resolved_path.as_deref(), Some("/resolved"));
    }

    #[test]
    fn remove_response_deserializes_without_resolved_path() {
        let json = r#"{"removed": true}"#;
        let resp: RemoveResponse = serde_json::from_str(json).unwrap();
        assert!(resp.removed);
        assert!(resp.resolved_path.is_none());
    }

    #[test]
    fn ext_envelope_unwraps_success_result() {
        let json = r#"{"result": {"path": "/home/user/.opengrok/worktrees.db"}, "error": null}"#;
        #[derive(serde::Deserialize)]
        struct PathResp {
            path: String,
        }
        let envelope: ExtEnvelope<PathResp> = serde_json::from_str(json).unwrap();
        assert!(envelope.error.is_none());
        let inner = envelope.result.unwrap();
        assert_eq!(inner.path, "/home/user/.opengrok/worktrees.db");
    }

    #[test]
    fn ext_envelope_unwraps_error_result() {
        let json = r#"{"result": null, "error": "something went wrong"}"#;
        let envelope: ExtEnvelope<serde_json::Value> = serde_json::from_str(json).unwrap();
        assert!(envelope.result.is_none());
        assert!(envelope.error.is_some());
    }

    #[test]
    fn ext_envelope_unwraps_list_of_records() {
        let json = r#"{"result": [], "error": null}"#;
        let envelope: ExtEnvelope<Vec<WorktreeRecord>> = serde_json::from_str(json).unwrap();
        assert!(envelope.error.is_none());
        assert!(envelope.result.unwrap().is_empty());
    }

    #[test]
    fn ext_envelope_unwraps_db_stats() {
        let json = r#"{"result": {"total_records": 5, "alive_count": 3, "dead_count": 2, "db_file_bytes": 1024}}"#;
        let envelope: ExtEnvelope<DbStats> = serde_json::from_str(json).unwrap();
        let stats = envelope.result.unwrap();
        assert_eq!(stats.total_records, 5);
        assert_eq!(stats.alive_count, 3);
    }

    #[test]
    fn ext_envelope_unwraps_gc_report() {
        let json = r#"{"result": {"dead_removed": 2, "expired_removed": 1, "skipped_alive": 0}}"#;
        let envelope: ExtEnvelope<GcReport> = serde_json::from_str(json).unwrap();
        let report = envelope.result.unwrap();
        assert_eq!(report.dead_removed, 2);
        assert_eq!(report.expired_removed, 1);
        // Older agents omit remove_failed; it must default to zero.
        assert_eq!(report.remove_failed, 0);
    }

    #[test]
    fn rm_parses_short_force_flag() {
        use clap::Parser;

        #[derive(Parser)]
        struct Cli {
            #[command(subcommand)]
            command: WorktreeCommand,
        }

        let cli = Cli::parse_from(["test", "rm", "-f", "wt-1"]);
        match cli.command {
            WorktreeCommand::Rm {
                ids,
                force,
                dry_run,
            } => {
                assert!(force);
                assert!(!dry_run);
                assert_eq!(ids, vec!["wt-1"]);
            }
            _ => panic!("expected Rm variant"),
        }
    }

    #[test]
    fn rm_parses_long_force_flag() {
        use clap::Parser;

        #[derive(Parser)]
        struct Cli {
            #[command(subcommand)]
            command: WorktreeCommand,
        }

        let cli = Cli::parse_from(["test", "rm", "--force", "a", "b"]);
        match cli.command {
            WorktreeCommand::Rm {
                ids,
                force,
                dry_run,
            } => {
                assert!(force);
                assert!(!dry_run);
                assert_eq!(ids, vec!["a", "b"]);
            }
            _ => panic!("expected Rm variant"),
        }
    }

    #[test]
    fn gc_parses_short_force_flag() {
        use clap::Parser;

        #[derive(Parser)]
        struct Cli {
            #[command(subcommand)]
            command: WorktreeCommand,
        }

        let cli = Cli::parse_from(["test", "gc", "-f"]);
        match cli.command {
            WorktreeCommand::Gc {
                force,
                dry_run,
                max_age,
            } => {
                assert!(force);
                assert!(!dry_run);
                assert!(max_age.is_none());
            }
            _ => panic!("expected Gc variant"),
        }
    }

    #[test]
    fn list_accepts_ls_alias() {
        use clap::Parser;

        #[derive(Parser)]
        struct Cli {
            #[command(subcommand)]
            command: WorktreeCommand,
        }

        let cli = Cli::parse_from(["test", "ls", "--json"]);
        match cli.command {
            WorktreeCommand::List {
                repo,
                r#type,
                json,
                all,
            } => {
                assert!(repo.is_none());
                assert!(r#type.is_empty());
                assert!(json);
                assert!(!all);
            }
            _ => panic!("expected List variant"),
        }
    }
}

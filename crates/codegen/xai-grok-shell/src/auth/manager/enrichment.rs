//! Background `/user` enrichment spawned by `AuthManager::update()`.

use std::sync::Arc;
use std::time::Duration as StdDuration;

use super::AuthManager;
use super::lock::try_lock_auth_file_async;
use crate::auth::manager::AUTH_LOCK_TIMEOUT;
use crate::auth::model::{GrokAuth, UserInfo, lookup_auth};
use crate::auth::storage::{read_auth_json, write_auth_json};
use xai_grok_telemetry::unified_log::LogLevel;

/// `/user` fetch budget, shared by the inline (login) and background paths.
const USER_FETCH_TIMEOUT: StdDuration = StdDuration::from_secs(10);

#[cfg(test)]
tokio::task_local! {
    static TEST_LOG_HOOK: Arc<dyn Fn() + Send + Sync>;
}

// The unified log takes a process-wide mutex and can rotate files. Keep it off
// the async executor, and never let a failed logging task alter auth results.
async fn log_offloaded(level: LogLevel, message: String, context: serde_json::Value) {
    #[cfg(test)]
    let hook = TEST_LOG_HOOK.try_with(Arc::clone).ok();
    let task = tokio::task::spawn_blocking(move || {
        #[cfg(test)]
        if let Some(hook) = hook {
            hook();
        }
        xai_grok_telemetry::unified_log::emit(level, &message, None, Some(context));
    });
    if let Err(error) = task.await {
        tracing::warn!(%error, "auth enrichment log task failed");
    }
}

/// Logs `auth update enrichment dropped` if the task is cancelled
/// mid-flight. Disarmed on normal completion.
pub(super) struct EnrichmentExitGuard {
    pub(super) started: std::time::Instant,
    pub(super) armed: bool,
}

impl EnrichmentExitGuard {
    pub(super) fn disarm(&mut self) {
        self.armed = false;
    }
}

impl Drop for EnrichmentExitGuard {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        let elapsed_ms = self.started.elapsed().as_millis() as u64;
        if let Ok(handle) = tokio::runtime::Handle::try_current() {
            handle.spawn_blocking(move || {
                xai_grok_telemetry::unified_log::warn(
                    "auth update enrichment dropped",
                    None,
                    Some(serde_json::json!({ "elapsed_ms": elapsed_ms })),
                );
            });
        } else {
            tracing::warn!(elapsed_ms, "auth update enrichment dropped");
        }
    }
}

pub(super) fn spawn(manager: Arc<AuthManager>, auth: GrokAuth) {
    tokio::spawn(async move {
        let mut exit_guard = EnrichmentExitGuard {
            started: std::time::Instant::now(),
            armed: true,
        };
        run_user_info_enrichment(&manager, auth).await;
        exit_guard.disarm();
    });
}

async fn fetch_user_info(manager: &AuthManager, key: &str, log_label: &str) -> Option<UserInfo> {
    let user_url = format!("{}/user", manager.proxy_base_url);
    let token_header = &manager.grok_com_config.token_header;
    let started = std::time::Instant::now();
    let http_client = crate::http::shared_client();
    let response = http_client
        .get(&user_url)
        .timeout(USER_FETCH_TIMEOUT)
        .header("Authorization", format!("Bearer {}", key))
        .header("X-XAI-Token-Auth", token_header.as_str())
        .header("x-grok-client-version", xai_grok_version::version())
        .header(
            crate::http::CLIENT_MODE_HEADER,
            crate::http::process_client_mode(),
        )
        .send()
        .await;

    match response {
        Ok(resp) if resp.status().is_success() => match resp.json::<UserInfo>().await {
            Ok(ui) if !ui.user_id.is_empty() => Some(ui),
            Ok(_) => {
                log_offloaded(
                    LogLevel::Warn,
                    format!("{log_label} skipped"),
                    serde_json::json!({
                        "reason": "empty_user_id",
                        "elapsed_ms": started.elapsed().as_millis() as u64,
                    }),
                )
                .await;
                None
            }
            Err(e) => {
                log_offloaded(
                    LogLevel::Warn,
                    format!("{log_label} failed"),
                    serde_json::json!({
                        "reason": "parse",
                        "error": e.to_string(),
                        "elapsed_ms": started.elapsed().as_millis() as u64,
                    }),
                )
                .await;
                None
            }
        },
        Ok(resp) => {
            log_offloaded(
                LogLevel::Warn,
                format!("{log_label} failed"),
                serde_json::json!({
                    "reason": "http_status",
                    "http_status": resp.status().as_u16(),
                    "elapsed_ms": started.elapsed().as_millis() as u64,
                }),
            )
            .await;
            None
        }
        Err(e) => {
            log_offloaded(
                LogLevel::Warn,
                format!("{log_label} failed"),
                serde_json::json!({
                    "reason": if e.is_timeout() { "timeout" } else { "transport" },
                    "error": e.to_string(),
                    "elapsed_ms": started.elapsed().as_millis() as u64,
                }),
            )
            .await;
            None
        }
    }
}

fn same_team_identity(expected: &GrokAuth, live: &GrokAuth) -> bool {
    expected
        .email
        .as_deref()
        .is_some_and(|email| !email.is_empty())
        && expected
            .team_id
            .as_deref()
            .is_some_and(|team| !team.is_empty())
        && expected.email == live.email
        && expected.team_id == live.team_id
        && expected.principal_type == live.principal_type
        && expected.principal_id == live.principal_id
        && expected.oidc_issuer == live.oidc_issuer
        && live.is_xai_auth()
        && live.is_team_principal()
}

fn merge_team_capability(
    manager: &AuthManager,
    expected: &GrokAuth,
    value: Option<bool>,
) -> Option<bool> {
    manager.with_inner_write(|inner| match inner.as_mut() {
        Some(live) if same_team_identity(expected, live) => {
            if live.can_administer_team.is_none() {
                live.can_administer_team = value;
            }
            live.can_administer_team
        }
        _ => None,
    })
}

pub(super) async fn hydrate_can_administer_team(
    manager: &AuthManager,
    auth: &GrokAuth,
) -> Option<bool> {
    let info = fetch_user_info(manager, &auth.key, "auth team capability").await?;
    if info
        .team_id
        .as_deref()
        .is_some_and(|id| Some(id) != auth.team_id.as_deref())
        || info
            .email
            .as_deref()
            .filter(|email| !email.is_empty())
            .is_some_and(|email| Some(email) != auth.email.as_deref())
        || info
            .principal_id
            .as_deref()
            .is_some_and(|id| Some(id) != auth.principal_id.as_deref())
        || info
            .principal_type
            .as_deref()
            .is_some_and(|kind| kind != crate::auth::model::TEAM_PRINCIPAL_TYPE)
    {
        return None;
    }
    let merged = merge_team_capability(manager, auth, info.can_administer_team);
    log_offloaded(
        LogLevel::Info,
        "auth team capability hydration done".to_owned(),
        serde_json::json!({"capability_present": merged.is_some()}),
    )
    .await;
    // A login can change while the blocking log writer is running.
    manager
        .current()
        .filter(|live| same_team_identity(auth, live))?
        .can_administer_team
}

/// Blocking login-time enrichment: merge `/user` fields before the first save.
pub(super) async fn enrich_inline(manager: &AuthManager, auth: &mut GrokAuth) {
    let Some(ui) = fetch_user_info(manager, &auth.key, "auth login enrichment").await else {
        return;
    };
    apply_user_info_enrichment(auth, ui);
}

// An auth file can change twice and return to the same privacy boolean while a
// GET is in flight. Track the backing file too, so an ABA change is not missed.
fn auth_file_revision(
    path: &std::path::Path,
) -> Option<(u64, std::time::SystemTime, Option<(u64, u64)>)> {
    let metadata = std::fs::metadata(path).ok()?;
    #[cfg(unix)]
    let identity = {
        use std::os::unix::fs::MetadataExt;
        Some((metadata.dev(), metadata.ino()))
    };
    #[cfg(not(unix))]
    let identity = None;
    Some((metadata.len(), metadata.modified().ok()?, identity))
}

async fn run_user_info_enrichment(manager: &AuthManager, auth: GrokAuth) {
    let started = std::time::Instant::now();
    let request_revision = auth_file_revision(&manager.path);
    let Some(mut user_info) = fetch_user_info(manager, &auth.key, "auth update enrichment").await
    else {
        return;
    };
    let user_elapsed_ms = started.elapsed().as_millis() as u64;
    let lock_started = std::time::Instant::now();
    let guard = try_lock_auth_file_async(
        &manager.path,
        AUTH_LOCK_TIMEOUT,
        super::lock::Heartbeat::Skip,
    )
    .await
    .into_guard();
    let lock_wait_ms = lock_started.elapsed().as_millis() as u64;
    let Some(guard) = guard else {
        log_offloaded(
            LogLevel::Warn,
            "auth update enrichment skipped".into(),
            serde_json::json!({"reason": "lock_timeout", "lock_wait_ms": lock_wait_ms}),
        )
        .await;
        return;
    };
    let Ok(mut map) = read_auth_json(&manager.path) else {
        drop(guard);
        log_offloaded(
            LogLevel::Warn,
            "auth update enrichment skipped".into(),
            serde_json::json!({"reason": "read_disk_failed"}),
        )
        .await;
        return;
    };
    let Some(mut disk) = lookup_auth(&map, &manager.scope) else {
        drop(guard);
        log_offloaded(
            LogLevel::Info,
            "auth update enrichment skipped".into(),
            serde_json::json!({"reason": "no_disk_auth"}),
        )
        .await;
        return;
    };
    // Either token may rotate while /user is in flight. Never stomp a sibling.
    if disk.key != auth.key
        || disk.refresh_token != auth.refresh_token
        || !guard.still_live(&manager.path)
    {
        drop(guard);
        log_offloaded(
            LogLevel::Info,
            "auth update enrichment skipped".into(),
            serde_json::json!({"reason": "credential_or_lock_changed"}),
        )
        .await;
        return;
    }
    // A privacy write can happen while the request is in flight. Preserve the
    // newer disk value rather than applying the stale response over it.
    if request_revision.is_none()
        || request_revision != auth_file_revision(&manager.path)
        || disk.coding_data_retention_opt_out != auth.coding_data_retention_opt_out
    {
        user_info.coding_data_retention_opt_out = None;
    }
    if disk.team_blocked_reasons != auth.team_blocked_reasons {
        user_info.team_blocked_reasons = None;
    }
    apply_user_info_enrichment(&mut disk, user_info);
    map.insert(manager.scope.clone(), disk.clone());
    let write_started = std::time::Instant::now();
    if let Err(error) = write_auth_json(&manager.path, &map) {
        drop(guard);
        log_offloaded(LogLevel::Error, "auth update enrichment write failed".into(),
            serde_json::json!({"error": error.to_string(), "user_ms": user_elapsed_ms,
                "lock_wait_ms": lock_wait_ms, "write_ms": write_started.elapsed().as_millis() as u64})).await;
        return;
    }
    manager.with_inner_write(|inner| {
        if let Some(live) = inner.as_mut()
            && live.key == auth.key
            && live.refresh_token == auth.refresh_token
        {
            // A newer in-memory privacy edit must not be reverted by this GET.
            if live.coding_data_retention_opt_out != auth.coding_data_retention_opt_out {
                disk.coding_data_retention_opt_out = live.coding_data_retention_opt_out;
            }
            if live.team_blocked_reasons != auth.team_blocked_reasons {
                disk.team_blocked_reasons
                    .clone_from(&live.team_blocked_reasons);
            }
            *live = disk;
        }
    });
    drop(guard);
    log_offloaded(
        LogLevel::Info,
        "auth update enrichment done".into(),
        serde_json::json!({"user_ms": user_elapsed_ms, "lock_wait_ms": lock_wait_ms,
            "write_ms": write_started.elapsed().as_millis() as u64,
            "total_ms": started.elapsed().as_millis() as u64}),
    )
    .await;
}

/// Merge enrichment fields into disk auth. Does not touch token fields.
pub(super) fn apply_user_info_enrichment(disk: &mut GrokAuth, user_info: UserInfo) {
    disk.user_id = user_info.user_id;
    disk.first_name = user_info.first_name.or(disk.first_name.take());
    disk.last_name = user_info.last_name.or(disk.last_name.take());
    disk.profile_image_asset_id = user_info
        .profile_image_asset_id
        .or(disk.profile_image_asset_id.take());
    disk.principal_type = user_info.principal_type.or(disk.principal_type.take());
    disk.principal_id = user_info.principal_id.or(disk.principal_id.take());
    disk.team_id = user_info.team_id.or(disk.team_id.take());
    disk.team_name = user_info.team_name.or(disk.team_name.take());
    disk.team_role = user_info.team_role.or(disk.team_role.take());
    disk.organization_id = user_info.organization_id.or(disk.organization_id.take());
    disk.organization_name = user_info
        .organization_name
        .or(disk.organization_name.take());
    disk.organization_role = user_info
        .organization_role
        .or(disk.organization_role.take());
    disk.user_blocked_reason = user_info
        .user_blocked_reason
        .or(disk.user_blocked_reason.take());
    if let Some(reasons) = user_info.team_blocked_reasons {
        disk.team_blocked_reasons = reasons;
    }
    if let Some(opt_out) = user_info.coding_data_retention_opt_out {
        disk.coding_data_retention_opt_out = opt_out;
    }
    disk.can_administer_team = user_info.can_administer_team;
    if let Some(ref email) = user_info.email
        && !email.is_empty()
    {
        disk.email = user_info.email;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::GrokComConfig;

    fn team_auth() -> GrokAuth {
        GrokAuth {
            key: "team-bearer".into(),
            oidc_issuer: Some(crate::auth::XAI_OAUTH2_ISSUER.into()),
            email: Some("a@example.test".into()),
            principal_type: Some("Team".into()),
            principal_id: Some("team-a".into()),
            team_id: Some("team-a".into()),
            ..GrokAuth::test_default()
        }
    }

    #[test]
    fn capability_merge_is_identity_scoped_and_does_not_touch_privacy() {
        let dir = tempfile::tempdir().unwrap();
        let manager = AuthManager::new(dir.path(), GrokComConfig::default());
        let expected = team_auth();
        manager.hot_swap(expected.clone());
        let changes = manager.login_changes();
        manager
            .with_inner_write(|inner| inner.as_mut().unwrap().coding_data_retention_opt_out = true);
        assert_eq!(
            merge_team_capability(&manager, &expected, Some(false)),
            Some(false)
        );
        let live = manager.current().unwrap();
        assert!(live.coding_data_retention_opt_out);
        assert_eq!(live.can_administer_team, Some(false));
        assert!(changes.has_changed(), "privacy change should notify");
        let new_team = GrokAuth {
            team_id: Some("team-b".into()),
            principal_id: Some("team-b".into()),
            can_administer_team: None,
            ..expected.clone()
        };
        manager.hot_swap(new_team);
        assert_eq!(merge_team_capability(&manager, &expected, Some(true)), None);
        assert_eq!(manager.current().unwrap().can_administer_team, None);
    }

    #[tokio::test]
    async fn hydration_does_not_write_disk_or_publish_to_a_switched_account() {
        let entered = Arc::new(tokio::sync::Notify::new());
        let release = Arc::new(tokio::sync::Notify::new());
        let router = axum::Router::new().route(
            "/v1/user",
            axum::routing::get({
                let entered = entered.clone();
                let release = release.clone();
                move || {
                    let entered = entered.clone();
                    let release = release.clone();
                    async move {
                        entered.notify_one();
                        release.notified().await;
                        axum::Json(serde_json::json!({
                            "userId": "user-a", "email": "a@example.test",
                            "teamId": "team-a", "principalId": "team-a",
                            "principalType": "Team", "canAdministerTeam": false,
                            "codingDataRetentionOptOut": false
                        }))
                    }
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let dir = tempfile::tempdir().unwrap();
        let manager = AuthManager::new(dir.path(), GrokComConfig::default())
            .with_proxy_base_url(&format!("http://{address}/v1"));
        manager.hot_swap(team_auth());

        let hydrate = manager.hydrate_can_administer_team(Some("a@example.test"), Some("team-a"));
        let change_privacy = async {
            entered.notified().await;
            manager.with_inner_write(|inner| {
                inner.as_mut().unwrap().coding_data_retention_opt_out = true
            });
            release.notify_one();
        };
        let (answer, ()) = tokio::join!(hydrate, change_privacy);
        assert_eq!(answer, Some(false));
        assert!(manager.current().unwrap().coding_data_retention_opt_out);
        assert!(!dir.path().join("auth.json").exists());

        manager.with_inner_write(|inner| inner.as_mut().unwrap().can_administer_team = None);
        let hydrate = manager.hydrate_can_administer_team(Some("a@example.test"), Some("team-a"));
        let switch = async {
            entered.notified().await;
            manager.hot_swap(GrokAuth {
                email: Some("b@example.test".into()),
                ..team_auth()
            });
            release.notify_one();
        };
        let (answer, ()) = tokio::join!(hydrate, switch);
        assert_eq!(answer, None);
        assert_eq!(manager.current().unwrap().can_administer_team, None);
        server.abort();
    }

    #[tokio::test]
    async fn stale_user_response_cannot_revert_privacy_after_two_changes() {
        let entered = Arc::new(tokio::sync::Notify::new());
        let release = Arc::new(tokio::sync::Notify::new());
        let router = axum::Router::new().route(
            "/v1/user",
            axum::routing::get({
                let entered = entered.clone();
                let release = release.clone();
                move || {
                    let entered = entered.clone();
                    let release = release.clone();
                    async move {
                        entered.notify_one();
                        release.notified().await;
                        axum::Json(serde_json::json!({
                            "userId": "user-a", "codingDataRetentionOptOut": true
                        }))
                    }
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let dir = tempfile::tempdir().unwrap();
        let manager = AuthManager::new(dir.path(), GrokComConfig::default())
            .with_proxy_base_url(&format!("http://{address}/v1"));
        let auth = team_auth();
        manager.persist_and_swap(auth.clone());
        let fetch = run_user_info_enrichment(&manager, auth.clone());
        let changes = async {
            entered.notified().await;
            assert!(manager.save_privacy_if_current(&auth, true).await.unwrap());
            assert!(manager.save_privacy_if_current(&auth, false).await.unwrap());
            release.notify_one();
        };
        tokio::join!(fetch, changes);
        assert!(!manager.current().unwrap().coding_data_retention_opt_out);
        let map = read_auth_json(&manager.path).unwrap();
        assert!(
            !lookup_auth(&map, &manager.scope)
                .unwrap()
                .coding_data_retention_opt_out
        );
        server.abort();
    }

    #[tokio::test(flavor = "current_thread")]
    async fn enrichment_log_write_does_not_block_runtime() {
        let (entered_tx, entered_rx) = tokio::sync::oneshot::channel();
        let (release_tx, release_rx) = std::sync::mpsc::channel::<()>();
        let entered_tx = std::sync::Mutex::new(Some(entered_tx));
        let release_rx = std::sync::Mutex::new(release_rx);
        let timed_out = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let hook: Arc<dyn Fn() + Send + Sync> = {
            let timed_out = timed_out.clone();
            Arc::new(move || {
                if let Some(tx) = entered_tx.lock().unwrap().take() {
                    let _ = tx.send(());
                }
                if release_rx
                    .lock()
                    .unwrap()
                    .recv_timeout(StdDuration::from_secs(5))
                    .is_err()
                {
                    timed_out.store(true, std::sync::atomic::Ordering::SeqCst);
                }
            })
        };
        let writer = TEST_LOG_HOOK.scope(
            hook,
            log_offloaded(LogLevel::Info, "test".into(), serde_json::json!({})),
        );
        let release = async {
            tokio::time::timeout(StdDuration::from_secs(5), entered_rx)
                .await
                .unwrap()
                .unwrap();
            release_tx.send(()).unwrap();
        };
        tokio::join!(writer, release);
        assert!(!timed_out.load(std::sync::atomic::Ordering::SeqCst));
    }
}

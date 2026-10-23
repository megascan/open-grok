//! Browser handoff for the validated Google OAuth URL. Never log or persist it.

use std::future::Future;
use std::time::Duration;

const BROWSER_LAUNCH_TIMEOUT: Duration = Duration::from_secs(5);

async fn launch_with_timeout(
    timeout: Duration,
    launch: impl Future<Output = std::io::Result<std::process::ExitStatus>>,
) -> bool {
    matches!(tokio::time::timeout(timeout, launch).await, Ok(Ok(status)) if status.success())
}

pub(super) async fn open_authorization_url(url: &str) -> bool {
    #[cfg(target_os = "macos")]
    let mut command = tokio::process::Command::new("open");
    #[cfg(target_os = "linux")]
    let mut command = tokio::process::Command::new("xdg-open");
    #[cfg(any(target_os = "macos", target_os = "linux"))]
    {
        command
            .arg(url)
            .stdin(std::process::Stdio::null())
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .kill_on_drop(true);
        launch_with_timeout(BROWSER_LAUNCH_TIMEOUT, command.status()).await
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux")))]
    {
        let _ = url;
        false
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::process::ExitStatusExt;

    #[tokio::test]
    async fn antigravity_browser_launcher_uses_exit_status_and_timeout() {
        let timeout = Duration::from_millis(10);
        assert!(
            launch_with_timeout(timeout, async { Ok(std::process::ExitStatus::from_raw(0)) }).await
        );
        assert!(
            !launch_with_timeout(timeout, async {
                Ok(std::process::ExitStatus::from_raw(1 << 8))
            })
            .await
        );
        assert!(
            !launch_with_timeout(timeout, async {
                Err(std::io::Error::other("missing launcher"))
            })
            .await
        );
        assert!(!launch_with_timeout(timeout, std::future::pending()).await);
    }
}

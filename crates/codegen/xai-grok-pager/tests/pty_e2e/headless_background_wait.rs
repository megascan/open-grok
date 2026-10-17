//! Real headless process coverage for background completion and its wake turn.

use super::common::*;

#[cfg(unix)]
struct ReleaseFile(std::path::PathBuf);

#[cfg(unix)]
impl ReleaseFile {
    fn release(&self) {
        std::fs::write(&self.0, b"release").expect("release background command");
    }
}

#[cfg(unix)]
impl Drop for ReleaseFile {
    fn drop(&mut self) {
        // Do not strand the gated shell command if an assertion fails.
        let _ = std::fs::write(&self.0, b"release");
    }
}

#[cfg(unix)]
fn start_headless(
    content: &ContentController,
    timeout_seconds: &str,
) -> (tokio::process::Child, ReleaseFile) {
    let release = ReleaseFile(content.home().join("background-release"));
    let mut command = tokio::process::Command::new(pager_binary().expect("resolve pager binary"));
    content.sandbox().apply_to_tokio_command(&mut command);
    command
        .current_dir(content.home())
        .args([
            "--yolo",
            "--trust",
            "--no-leader",
            "--background-wait-timeout",
            timeout_seconds,
            "-p",
            "start the background command",
        ])
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true);
    (command.spawn().expect("spawn headless pager"), release)
}

#[cfg(unix)]
fn script_background(content: &ContentController) -> AgentTurnExpectation {
    let args = json!({
        "command": "while [ ! -f background-release ]; do sleep 0.05; done; printf 'background complete\\n'",
        "description": "gated background work",
        "is_background": true
    })
    .to_string();
    expect_tool_turn(content, "headless_background", "run_terminal_command", args)
}

#[cfg(unix)]
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "process e2e; run with the pager binary and --ignored"]
async fn headless_waits_for_background_and_its_followup_turn() {
    let content = ContentController::start().await.expect("start content");
    let mut background = script_background(&content);
    let mut first = content.expect_agent_turn("headless first reply", "FIRST_TURN_SETTLED");
    let (mut child, release_file) = start_headless(&content, "30");

    tokio::time::timeout(Duration::from_secs(45), background.wait_satisfied())
        .await
        .expect("background tool call");
    tokio::time::timeout(Duration::from_secs(45), first.wait_satisfied())
        .await
        .expect("first reply");
    let mut wake = content.expect_agent_turn_blocked("headless wake", "FOLLOWUP_FINISHED");
    tokio::time::sleep(Duration::from_millis(750)).await;
    assert!(
        child.try_wait().unwrap().is_none(),
        "exited with background work pending"
    );

    release_file.release();
    tokio::time::timeout(Duration::from_secs(30), wake.wait_blocked())
        .await
        .expect("background completion did not start a wake turn");
    tokio::time::sleep(Duration::from_millis(750)).await;
    assert!(
        child.try_wait().unwrap().is_none(),
        "exited while the wake turn was blocked"
    );
    wake.release();
    let output = tokio::time::timeout(Duration::from_secs(30), child.wait_with_output())
        .await
        .expect("headless exit timed out")
        .expect("collect headless output");
    assert!(
        output.status.success(),
        "stderr: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(String::from_utf8_lossy(&output.stdout).contains("FOLLOWUP_FINISHED"));
    wake.assert_satisfied();
}

#[cfg(unix)]
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "process e2e; run with the pager binary and --ignored"]
async fn headless_background_timeout_exits_nonzero() {
    let content = ContentController::start().await.expect("start content");
    let mut background = script_background(&content);
    let mut first = content.expect_agent_turn("headless timeout reply", "FIRST_TURN_SETTLED");
    let (child, _release_file) = start_headless(&content, "2");
    tokio::time::timeout(Duration::from_secs(45), background.wait_satisfied())
        .await
        .expect("background tool call");
    tokio::time::timeout(Duration::from_secs(45), first.wait_satisfied())
        .await
        .expect("first reply");
    let output = tokio::time::timeout(Duration::from_secs(20), child.wait_with_output())
        .await
        .expect("headless timeout exit")
        .expect("collect headless output");
    assert!(
        !output.status.success(),
        "pending background work must fail the command"
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("background wait timed out"),
        "stderr: {stderr}"
    );
}

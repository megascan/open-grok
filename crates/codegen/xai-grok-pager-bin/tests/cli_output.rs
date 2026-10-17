//! Real-process coverage for early CLI dispatch and closed output pipes.

use std::path::{Path, PathBuf};
use std::process::{Command, Output, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};

static NEXT: AtomicU64 = AtomicU64::new(0);

struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        let root = std::env::temp_dir().join(format!(
            "open-grok-cli-output-{}-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos(),
            NEXT.fetch_add(1, Ordering::Relaxed),
        ));
        std::fs::create_dir(&root).unwrap();
        Self(root)
    }

    fn command(&self, home: &Path, args: &[&str]) -> Command {
        let mut command = Command::new(env!("CARGO_BIN_EXE_open-grok"));
        command
            .args(args)
            .env("OPENGROK_HOME", home)
            .env("HOME", &self.0)
            .env("USERPROFILE", &self.0)
            .env("XDG_CONFIG_HOME", self.0.join("config"))
            .env("OPENGROK_DISABLE_AUTOUPDATER", "1");
        command
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

#[test]
fn help_and_version_do_not_create_state() {
    let fixture = Fixture::new();
    for (i, args) in [
        &["--help"][..],
        &["-h"],
        &["agent", "--help"],
        &["version", "--help"],
        &["--version"],
        &["-v"],
        &["-V"],
        &["version"],
        &["v"],
        &["version", "--json"],
    ]
    .into_iter()
    .enumerate()
    {
        let home = fixture.0.join(format!("state-{i}"));
        let output = fixture.command(&home, args).output().unwrap();
        assert!(
            output.status.success(),
            "{args:?}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(!output.stdout.is_empty(), "{args:?} produced no output");
        assert!(!home.exists(), "{args:?} created {}", home.display());
    }
}

#[cfg(unix)]
fn closed_pipe() -> Stdio {
    use std::os::fd::FromRawFd;
    let mut fds = [-1; 2];
    assert_eq!(unsafe { libc::pipe(fds.as_mut_ptr()) }, 0);
    // Close the read end before spawning, so even a one-byte write sees EPIPE.
    unsafe {
        libc::close(fds[0]);
    }
    Stdio::from(unsafe { std::fs::File::from_raw_fd(fds[1]) })
}

#[cfg(unix)]
fn full_nonblocking_pipe() -> (std::fs::File, Stdio) {
    use std::os::fd::FromRawFd;

    let mut fds = [-1; 2];
    assert_eq!(unsafe { libc::pipe(fds.as_mut_ptr()) }, 0);
    let reader = unsafe { std::fs::File::from_raw_fd(fds[0]) };
    let writer = unsafe { std::fs::File::from_raw_fd(fds[1]) };
    let flags = unsafe { libc::fcntl(fds[1], libc::F_GETFL) };
    assert!(flags >= 0);
    assert_eq!(
        unsafe { libc::fcntl(fds[1], libc::F_SETFL, flags | libc::O_NONBLOCK) },
        0
    );

    // Fill any remaining space one byte at a time after bulk writes so even
    // short CLI output encounters WouldBlock. Keep the reader alive, but idle.
    for buffer in [&[0u8; 4096][..], &[0u8][..]] {
        loop {
            let written = unsafe { libc::write(fds[1], buffer.as_ptr().cast(), buffer.len()) };
            if written >= 0 {
                assert!(written > 0);
                continue;
            }
            let error = std::io::Error::last_os_error();
            if error.kind() == std::io::ErrorKind::Interrupted {
                continue;
            }
            assert_eq!(error.kind(), std::io::ErrorKind::WouldBlock, "{error}");
            break;
        }
    }
    (reader, Stdio::from(writer))
}

#[cfg(unix)]
fn assert_no_panic(output: &Output) {
    assert!(
        !String::from_utf8_lossy(&output.stderr).contains("panicked"),
        "{:?}",
        output
    );
}

#[cfg(unix)]
#[test]
fn closed_stdout_does_not_panic() {
    let fixture = Fixture::new();
    for (i, args) in [
        &["--help"][..],
        &["--version"],
        &["version"],
        &["version", "--json"],
        &["completions", "bash"],
        &["completions", "zsh"],
        &["completions", "fish"],
        &["mcp", "list"],
        &["doctor"],
        &["doctor", "--json"],
        &["inspect", "--json"],
    ]
    .into_iter()
    .enumerate()
    {
        let home = fixture.0.join(format!("stdout-{i}"));
        let output = fixture
            .command(&home, args)
            .stdout(closed_pipe())
            .output()
            .unwrap();
        assert!(output.status.success(), "{args:?}: {:?}", output);
        assert_no_panic(&output);
    }
}

#[cfg(unix)]
#[test]
fn closed_stderr_preserves_cli_error_status() {
    let fixture = Fixture::new();
    for (i, args) in [
        &["--invalid-flag"][..],
        &["usage", "missing-session"],
        &["plugin", "install", "/missing-plugin"],
    ]
    .into_iter()
    .enumerate()
    {
        let home = fixture.0.join(format!("stderr-{i}"));
        let output = fixture
            .command(&home, args)
            .stderr(closed_pipe())
            .output()
            .unwrap();
        assert!(
            output.status.code().is_some_and(|code| code != 0),
            "{args:?}: {:?}",
            output
        );
    }
}

#[cfg(unix)]
#[test]
fn non_pipe_output_errors_are_failures() {
    let fixture = Fixture::new();
    for (i, args) in [
        &["--version"][..],
        &["version", "--json"],
        &["--help"],
        &["completions", "bash"],
    ]
    .into_iter()
    .enumerate()
    {
        let (_reader, writer) = full_nonblocking_pipe();
        let output = fixture
            .command(&fixture.0.join(format!("bad-out-{i}")), args)
            .stdout(writer)
            .output()
            .unwrap();
        assert!(
            output.status.code().is_some_and(|code| code != 0),
            "{args:?}: {output:?}"
        );
        assert_no_panic(&output);
    }

    let (_reader, writer) = full_nonblocking_pipe();
    let output = fixture
        .command(&fixture.0.join("bad-err"), &["--invalid-flag"])
        .stderr(writer)
        .output()
        .unwrap();
    assert!(output.status.code().is_some_and(|code| code != 0));
}

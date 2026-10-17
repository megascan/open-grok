use pretty_assertions::assert_eq;

use super::*;
use crate::test_support::{far_future, private_tempdir};

#[cfg(unix)]
#[tokio::test]
async fn log_replaces_symlink_without_truncating_its_target() {
    use std::os::unix::fs::{PermissionsExt, symlink};
    let dir = private_tempdir();
    let target = dir.path().join("protected");
    let path = dir.path().join("exec.log");
    std::fs::write(&target, "keep this").expect("target");
    symlink(&target, &path).expect("symlink");
    let log = ExecLog::new(path.clone());
    log.start_trigger(far_future()).await;
    log.append(b"handler output").await;
    log.finish_trigger();
    assert_eq!(
        "keep this",
        std::fs::read_to_string(target).expect("target untouched")
    );
    assert_eq!(
        "handler output",
        std::fs::read_to_string(&path).expect("log")
    );
    assert!(
        !std::fs::symlink_metadata(&path)
            .expect("log metadata")
            .file_type()
            .is_symlink()
    );
    assert_eq!(
        0o600,
        std::fs::metadata(path)
            .expect("log metadata")
            .permissions()
            .mode()
            & 0o777
    );
}

#[cfg(unix)]
#[tokio::test]
async fn log_replaces_hard_link_without_truncating_its_target() {
    use std::os::unix::fs::{MetadataExt, PermissionsExt};
    let dir = private_tempdir();
    let target = dir.path().join("protected");
    let path = dir.path().join("exec.log");
    std::fs::write(&target, "keep this").expect("target");
    std::fs::hard_link(&target, &path).expect("hard link");
    let original_inode = std::fs::metadata(&target).expect("target metadata").ino();
    assert_eq!(
        original_inode,
        std::fs::metadata(&path).expect("link").ino()
    );

    let log = ExecLog::new(path.clone());
    log.start_trigger(far_future()).await;
    log.append(b"handler output").await;
    log.finish_trigger();

    assert_eq!(
        "keep this",
        std::fs::read_to_string(&target).expect("target")
    );
    assert_eq!(
        "handler output",
        std::fs::read_to_string(&path).expect("log")
    );
    let metadata = std::fs::metadata(&path).expect("log metadata");
    assert_ne!(original_inode, metadata.ino());
    assert_eq!(0o600, metadata.permissions().mode() & 0o777);
}

#[cfg(unix)]
#[tokio::test]
async fn log_rejects_public_parent_without_modifying_existing_file() {
    use std::os::unix::fs::PermissionsExt;
    let dir = private_tempdir();
    let path = dir.path().join("exec.log");
    std::fs::write(&path, "keep this").expect("existing file");
    std::fs::set_permissions(dir.path(), std::fs::Permissions::from_mode(0o755))
        .expect("public mode");
    let log = ExecLog::new(path.clone());
    log.start_trigger(far_future()).await;
    log.append(b"handler output").await;
    log.finish_trigger();
    assert_eq!(
        "keep this",
        std::fs::read_to_string(path).expect("file intact")
    );
}

#[cfg(unix)]
#[tokio::test]
async fn log_rejects_symlink_parent() {
    use std::os::unix::fs::symlink;
    let root = private_tempdir();
    let target = private_tempdir();
    let path = target.path().join("exec.log");
    std::fs::write(&path, "keep").expect("victim");
    let alias = root.path().join("alias");
    symlink(target.path(), &alias).expect("symlink");
    let log = ExecLog::new(alias.join("exec.log"));
    log.start_trigger(far_future()).await;
    log.append(b"output").await;
    log.finish_trigger();
    assert_eq!(
        "keep",
        std::fs::read_to_string(path).expect("victim intact")
    );
}

#[tokio::test]
async fn log_never_exceeds_the_cap_and_marks_the_cut() {
    let dir = private_tempdir();
    let path = dir.path().join("exec.log");
    let log = ExecLog::new(path.clone());
    log.start_trigger(far_future()).await;
    let chunk = vec![b'x'; 8 * 1024];
    for _ in 0..16 {
        log.append(&chunk).await;
    }
    log.finish_trigger();

    let contents = std::fs::read(&path).expect("read log");
    assert_eq!(EXEC_LOG_CAP_BYTES, contents.len());
    assert!(contents.ends_with(TRUNCATION_MARKER));
}

#[tokio::test]
async fn finish_while_an_append_holds_the_sink_still_closes_it() {
    let dir = private_tempdir();
    let path = dir.path().join("exec.log");
    let log = ExecLog::new(path.clone());
    log.start_trigger(far_future()).await;
    log.append(b"first trigger\n").await;

    let in_flight = log.sink.lock().await;
    log.finish_trigger();
    drop(in_flight);
    log.append(b"late output\n").await;

    assert_eq!(
        b"first trigger\n".to_vec(),
        std::fs::read(&path).expect("read")
    );
    assert!(log.sink.lock().await.is_none(), "the sink is still open");
}

#[tokio::test]
async fn each_trigger_starts_an_empty_log_and_output_after_finish_is_dropped() {
    let dir = private_tempdir();
    let path = dir.path().join("exec.log");
    let log = ExecLog::new(path.clone());

    log.start_trigger(far_future()).await;
    log.append(b"first trigger\n").await;
    log.finish_trigger();
    log.append(b"late output\n").await;
    assert_eq!(
        b"first trigger\n".to_vec(),
        std::fs::read(&path).expect("read")
    );

    log.start_trigger(far_future()).await;
    log.append(b"second trigger\n").await;
    log.finish_trigger();
    assert_eq!(
        b"second trigger\n".to_vec(),
        std::fs::read(&path).expect("read")
    );
}

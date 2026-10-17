use super::*;
use crate::session::storage::{PromptExtractIterator, UpdatesIterator};

#[tokio::test]
async fn gzip_history_resumes_appends_rewrites_and_keeps_summary_plain() {
    let dir = tempfile::tempdir().unwrap();
    let adapter = JsonlStorageAdapter::with_explicit_session_dir(dir.path().to_path_buf());
    let info = Info {
        id: acp::SessionId::new("gzip-history"),
        cwd: "/gzip-fixture".into(),
    };
    adapter
        .init_session(&info, crate::session::persistence::default_model_id())
        .await
        .unwrap();
    let message = ConversationItem::user("Compress this repeated thread history. ".repeat(8_000));
    adapter
        .replace_chat_history(&info, &[message.clone()])
        .await
        .unwrap();
    let path = adapter.chat_file(&info);
    let encoded = std::fs::read(&path).unwrap();
    assert_eq!(&encoded[..2], &[0x1f, 0x8b]);
    assert!(
        adapter
            .load_chat_history_bounded_from_dir(dir.path(), 1024, 10)
            .is_err()
    );
    let loaded = adapter.load_session(&info).await.unwrap();
    assert_eq!(
        serde_json::to_value(&loaded.chat_history).unwrap(),
        serde_json::to_value([message.clone()]).unwrap()
    );
    adapter
        .append_chat_message(&info, &ConversationItem::user("after resume"))
        .await
        .unwrap();
    assert!(
        !xai_file_utils::session_file::open(&path)
            .unwrap()
            .is_compressed()
    );
    adapter.compress_history(&info).await.unwrap();
    assert!(
        xai_file_utils::session_file::open(&path)
            .unwrap()
            .is_compressed()
    );
    assert_eq!(
        adapter
            .load_session(&info)
            .await
            .unwrap()
            .chat_history
            .len(),
        2
    );
    adapter
        .replace_chat_history(&info, &[ConversationItem::user("rewound")])
        .await
        .unwrap();
    assert_eq!(
        adapter
            .load_session(&info)
            .await
            .unwrap()
            .chat_history
            .len(),
        1
    );
    assert!(
        !xai_file_utils::session_file::open(&path)
            .unwrap()
            .is_compressed()
    );
    serde_json::from_slice::<serde_json::Value>(
        &std::fs::read(adapter.summary_file(&info)).unwrap(),
    )
    .unwrap();
    eprintln!(
        "gzip history: decoded={} bytes, gzip={} bytes",
        serde_json::to_vec(&message).unwrap().len() + 1,
        encoded.len()
    );
}

#[tokio::test]
async fn gzip_durable_update_append_and_corrupt_append_preserve_original() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("updates.jsonl");
    let bytes = b"{\"fixture\":true}\n".repeat(8_000);
    std::fs::write(&path, &bytes).unwrap();
    super::super::compression::compress_history(&path).unwrap();
    JsonlStorageAdapter::append_jsonl_line_sync(
        &path,
        b"{\"next\":true}\n".to_vec(),
        AppendDurability::Durable,
    )
    .unwrap();
    assert_eq!(
        std::fs::read(&path).unwrap(),
        [bytes, b"{\"next\":true}\n".to_vec()].concat()
    );
    super::super::compression::compress_history(&path).unwrap();
    let mut corrupt = std::fs::read(&path).unwrap();
    corrupt.pop();
    std::fs::write(&path, &corrupt).unwrap();
    assert!(
        JsonlStorageAdapter::append_jsonl_line_sync(
            &path,
            b"{}\n".to_vec(),
            AppendDurability::Buffered
        )
        .is_err()
    );
    assert_eq!(std::fs::read(&path).unwrap(), corrupt);
}

#[test]
fn gzip_cwd_switch_retry_remains_idempotent_after_compression() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("chat_history.jsonl");
    let seed = ConversationItem::user("large history ".repeat(8_000));
    let mut bytes = serde_json::to_vec(&seed).unwrap();
    bytes.push(b'\n');
    std::fs::write(&path, bytes).unwrap();
    super::super::compression::compress_history(&path).unwrap();
    let item = ConversationItem::working_directory_switch("moved", 3);
    let mut line = serde_json::to_vec(&item).unwrap();
    line.push(b'\n');
    assert!(matches!(
        JsonlStorageAdapter::append_cwd_switch_line_sync_with(
            &path,
            line.clone(),
            3,
            |_| Err(io::Error::other("file barrier failed")),
            || Ok(()),
        ),
        Err(crate::session::storage::AppendCwdSwitchError::Committed {
            acknowledgement: StrictAppendAck::Appended,
            ..
        })
    ));
    super::super::compression::compress_history(&path).unwrap();
    assert!(matches!(
        JsonlStorageAdapter::append_cwd_switch_line_sync_with(
            &path, line, 3, |_| Ok(()), || Ok(()),
        ).unwrap(),
        StrictAppendAck::AlreadyPresent(item) if item.text_content() == "moved"
    ));
    assert_eq!(
        xai_file_utils::session_file::read_to_string(&path)
            .unwrap()
            .lines()
            .count(),
        2
    );
}

#[test]
fn gzip_iterators_fuse_transport_errors_and_finish_checks_unread_trailers() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("updates.jsonl");
    let line = r#"{"sessionId":"gzip","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello"}}}"#;
    let bytes = format!("{line}\n").repeat(2_000).into_bytes();
    std::fs::write(&path, &bytes).unwrap();
    super::super::compression::compress_history(&path).unwrap();
    let mut corrupt = std::fs::read(&path).unwrap();
    let crc = corrupt.len() - 8;
    corrupt[crc] ^= 1;
    std::fs::write(&path, corrupt).unwrap();
    let mut updates = UpdatesIterator::open(&path).unwrap().unwrap();
    assert!(updates.next().unwrap().is_ok());
    assert!(updates.finish().is_err());
    let mut updates = UpdatesIterator::open(&path).unwrap().unwrap();
    assert!(updates.by_ref().take(2_002).any(|item| item.is_err()));
    assert!(updates.next().is_none());
    assert!(updates.finish().is_err());
    let mut prompts = PromptExtractIterator::open(&path).unwrap().unwrap();
    assert!(prompts.by_ref().take(2_002).count() <= 2_001);
    assert!(prompts.next().is_none());
    assert!(prompts.finish().is_err());
}

#[test]
fn gzip_compression_and_append_share_the_history_lock() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("updates.jsonl");
    let seed = b"{\"seed\":true}\n".repeat(8_000);
    std::fs::write(&path, &seed).unwrap();
    std::thread::scope(|scope| {
        let compress = scope.spawn(|| {
            for _ in 0..8 {
                super::super::compression::compress_history(&path).unwrap();
            }
        });
        let append = scope.spawn(|| {
            for i in 0..20 {
                JsonlStorageAdapter::append_jsonl_line_sync(
                    &path,
                    format!("{{\"append\":{i}}}\n").into_bytes(),
                    AppendDurability::Buffered,
                )
                .unwrap();
            }
        });
        compress.join().unwrap();
        append.join().unwrap();
    });
    let decoded = xai_file_utils::session_file::read_to_string(&path).unwrap();
    assert!(decoded.starts_with(std::str::from_utf8(&seed).unwrap()));
    assert_eq!(decoded.lines().count(), 8_020);
    for i in 0..20 {
        assert_eq!(decoded.matches(&format!("{{\"append\":{i}}}\n")).count(), 1);
    }
}

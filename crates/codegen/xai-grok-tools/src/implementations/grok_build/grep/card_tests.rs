use super::*;
use crate::DEFAULT_TOOL_OUTPUT_BYTES;
use crate::implementations::grok_build::grep::DEFAULT_MAX_CHARS_PER_LINE;

#[test]
fn grep_timeout_output_is_error_with_guidance() {
    let out = grep_timeout_output(20);
    assert_eq!(out.exit_code, -1);
    assert_eq!(out.match_count, 0);
    assert!(out.file_matches.is_empty());
    let msg = String::from_utf8_lossy(&out.stdout);
    assert!(msg.contains("timed out after 20 seconds"), "msg: {msg}");
    assert!(msg.contains("did not complete in time"), "msg: {msg}");
    assert!(msg.contains("more specific path or pattern"), "msg: {msg}");
    assert!(crate::types::output::ToolOutput::GrepSearch(out).is_error());
}

#[test]
fn grep_failure_is_not_masked_by_no_files_message() {
    let config = GrepFormatConfig {
        output_mode: OutputMode::Content,
        effective_head_limit: 100,
        max_chars_per_line: DEFAULT_MAX_CHARS_PER_LINE,
        max_output_bytes: DEFAULT_TOOL_OUTPUT_BYTES,
        cwd_display: "/workspace".into(),
    };
    let error = finalize_grep(
        Vec::new(),
        false,
        b"permission denied\nNo files were searched".to_vec(),
        2,
        &config,
    );
    let message = String::from_utf8_lossy(&error.stdout);
    assert!(message.contains("permission denied"), "{message}");
    assert!(crate::types::output::ToolOutput::GrepSearch(error).is_error());
    let empty = finalize_grep(Vec::new(), false, Vec::new(), 1, &config);
    assert!(String::from_utf8_lossy(&empty.stdout).contains("No matches found"));
    assert!(!crate::types::output::ToolOutput::GrepSearch(empty).is_error());
    let spawn = spawn_failure_output(&io::Error::new(io::ErrorKind::PermissionDenied, "denied"));
    let prompt = crate::types::output::ToolOutput::GrepSearch(spawn);
    assert!(prompt.is_error());
    assert!(prompt.to_prompt_format().contains("denied"));
    let partial = crate::types::output::ToolOutput::GrepSearch(GrepSearchOutput {
        stdout: b"file: matching line".to_vec(),
        stderr: b"permission denied".to_vec(),
        exit_code: 2,
        match_count: 1,
        file_matches: Vec::new(),
    });
    assert!(partial.is_error());
    let prompt = partial.to_prompt_format();
    assert!(
        prompt.contains("matching line") && prompt.contains("permission denied"),
        "{prompt}"
    );
    let partial_rg = finalize_grep(
        b"file\n1:match\n".to_vec(),
        false,
        b"permission denied".to_vec(),
        2,
        &config,
    );
    let partial_card = String::from_utf8_lossy(&partial_rg.stdout);
    assert!(
        partial_card.contains("permission denied")
            && partial_card.contains("match")
            && partial_card.contains("partial"),
        "{partial_card}"
    );
    let truncated_rg = finalize_grep(
        b"file\n1:match\n".to_vec(),
        true,
        b"permission denied".to_vec(),
        0,
        &config,
    );
    assert!(crate::types::output::ToolOutput::GrepSearch(truncated_rg).is_error());
}

#[test]
fn test_parse_numbered_line_prefix() {
    assert_eq!(
        parse_numbered_line_prefix("123:content"),
        Some((123, ':', "content"))
    );
    assert_eq!(
        parse_numbered_line_prefix("45-context line"),
        Some((45, '-', "context line"))
    );
    assert_eq!(parse_numbered_line_prefix("not a match"), None);
    assert_eq!(parse_numbered_line_prefix(""), None);
}

#[test]
fn test_trim_line() {
    assert_eq!(trim_line("short", DEFAULT_MAX_CHARS_PER_LINE), "short");

    let long_line = "a".repeat(2000);
    let trimmed = trim_line(&long_line, DEFAULT_MAX_CHARS_PER_LINE);
    assert!(trimmed.len() < long_line.len());
    assert!(trimmed.contains("[... truncated"));
}

#[test]
fn test_parse_file_matches() {
    let lines: Vec<String> = vec![
        "src/main.rs",
        "10:fn main() {",
        "15:    println!(\"hello\");",
        "",
        "src/lib.rs",
        "5:pub fn greet() {",
    ]
    .into_iter()
    .map(String::from)
    .collect();

    let matches = parse_file_matches(&lines, DEFAULT_MAX_CHARS_PER_LINE);
    let [first, second] = matches.as_slice() else {
        panic!("expected two file matches: {matches:?}");
    };
    assert_eq!(first.path, "src/main.rs");
    assert_eq!(first.matches.len(), 2);
    let Some(first_hit) = first.matches.first() else {
        panic!("expected a match in first file: {:?}", first.matches);
    };
    assert_eq!(first_hit.line_number, 10);
    assert_eq!(second.path, "src/lib.rs");
    assert_eq!(second.matches.len(), 1);
}

#[test]
fn test_count_matches() {
    let lines: Vec<String> = vec!["10:match", "11-context", "12:match", ""]
        .into_iter()
        .map(String::from)
        .collect();
    assert_eq!(count_matches(&lines), 2);
}

#[test]
fn test_format_content_output_not_truncated() {
    let lines: Vec<String> = vec!["src/main.rs", "10:fn main() {", ""]
        .into_iter()
        .map(String::from)
        .collect();

    let result = format_content_output(
        lines,
        false,
        DEFAULT_MAX_CHARS_PER_LINE,
        DEFAULT_TOOL_OUTPUT_BYTES,
    );
    assert!(result.starts_with("Found 1 matching lines"));
}

#[test]
fn test_format_content_output_truncated() {
    let lines: Vec<String> = vec!["src/main.rs", "10:fn main() {"]
        .into_iter()
        .map(String::from)
        .collect();

    let result = format_content_output(
        lines,
        true,
        DEFAULT_MAX_CHARS_PER_LINE,
        DEFAULT_TOOL_OUTPUT_BYTES,
    );
    assert!(result.starts_with("Found at least 1 matching lines"));
}

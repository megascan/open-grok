//! Resolve the per-path lock key a tool call targets.
//!
//! Used by the Grok CLI's `execute_tool_calls`, which buckets a whole batch of parallel calls
//! by the first present arg among [`PROD_LOCK_PATH_KEYS`] and serializes writers of one file.
//! Relative vs absolute, `./`/`..` aliases, and symlinked ancestors all collapse to one string,
//! or two writers of one file would miss each other. The tool server itself does not lock; it
//! advertises each writer's [`ToolMetadata::lock_path_param`] (see
//! [`LOCK_PATH_PARAM_CAPABILITY_PREFIX`]) so the client issuing the batch can do the same.
//!
//! [`ToolMetadata::lock_path_param`]: crate::types::tool_metadata::ToolMetadata::lock_path_param

use std::path::{Path, PathBuf};

/// The argument names the production Grok CLI dispatcher keys its per-path lock on, in
/// priority order. `file_path`: grok_build (`search_replace`), opencode (`edit`, `write`,
/// `read`), codex (`read_file`); `path`: tools that take a bare `path`; `target_file`: grok_build
/// `read_file`.
/// `target_directory` is deliberately omitted: a directory listing isn't an edit and must not
/// share a file lock.
pub const PROD_LOCK_PATH_KEYS: &[&str] = &["file_path", "path", "target_file"];

/// Prefix of the `ToolCapabilities.custom_capabilities` entry a tool server emits for every
/// non-read-only tool that declares a `lock_path_param`: `lock_path_param:<name>`, where
/// `<name>` is the *client-facing* argument name (after any per-session renaming), so the
/// client can find the path in the arguments it is about to send without knowing the
/// canonical schema. The client owns serialization: calls whose named argument resolves to
/// the same path must not run concurrently. Absence means the tool has no single path to key
/// on (a shell, a multi-file patch), not that concurrent calls are safe.
pub const LOCK_PATH_PARAM_CAPABILITY_PREFIX: &str = "lock_path_param:";

/// The capability entry advertising `client_param` as a tool's lock path argument.
pub fn lock_path_param_capability(client_param: &str) -> String {
    format!("{LOCK_PATH_PARAM_CAPABILITY_PREFIX}{client_param}")
}

/// First string-valued argument among `keys`, in priority order.
fn str_arg<'a>(args: &'a serde_json::Value, keys: &[&str]) -> Option<&'a str> {
    keys.iter().find_map(|k| args.get(*k)?.as_str())
}

/// Lock key for the path held by the first present string arg among `keys`, resolved against
/// `cwd`. `None` when no key is present or the value is not a string.
pub fn lock_path_for_keys(args: &serde_json::Value, keys: &[&str], cwd: &Path) -> Option<String> {
    Some(lock_path_for_path(Path::new(str_arg(args, keys)?), cwd))
}

/// Lock key for a call using the production CLI's key list ([`PROD_LOCK_PATH_KEYS`]).
pub fn lock_path_for_args(args: &serde_json::Value, cwd: &Path) -> Option<String> {
    lock_path_for_keys(args, PROD_LOCK_PATH_KEYS, cwd)
}

/// Lock key for one path, joined to `cwd` if relative. Existing components are canonicalized as
/// they are traversed so symlink aliases and `..` follow filesystem order; missing suffixes are
/// normalized lexically so not-yet-created files still resolve.
pub fn lock_path_for_path(input: &Path, cwd: &Path) -> String {
    let absolute = if input.is_absolute() {
        input.to_path_buf()
    } else {
        cwd.join(input)
    };
    // Resolve existing components before consuming `..`: an OS path such as
    // `link/../file` walks the parent of the symlink target, not the parent of
    // `link` itself. Lexically collapsing first would use a different lock key
    // for two spellings that write the same file.
    let mut resolved = PathBuf::new();
    for component in absolute.components() {
        match component {
            std::path::Component::CurDir => {}
            std::path::Component::ParentDir => {
                resolved.pop();
            }
            component => {
                resolved.push(component.as_os_str());
                if let Ok(canonical) = dunce::canonicalize(&resolved) {
                    resolved = canonical;
                }
            }
        }
    }
    resolved.to_string_lossy().into_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn declared_key_and_prod_keys_agree_on_the_same_file() {
        let cwd = Path::new("/cwd");
        let args = serde_json::json!({"file_path": "src/lib.rs", "old_string": "a"});
        assert_eq!(
            lock_path_for_keys(&args, &["file_path"], cwd),
            lock_path_for_args(&args, cwd)
        );
    }

    #[test]
    fn declared_key_ignores_other_path_shaped_args() {
        let cwd = Path::new("/cwd");
        // A tool that declares `target_notebook` must not pick up an unrelated `path` arg.
        let args = serde_json::json!({"target_notebook": "nb.ipynb", "path": "other.py"});
        assert_eq!(
            lock_path_for_keys(&args, &["target_notebook"], cwd).as_deref(),
            Some(lock_path_for_path(Path::new("/cwd/nb.ipynb"), cwd).as_str())
        );
        assert_eq!(lock_path_for_keys(&args, &["file_path"], cwd), None);
    }

    #[test]
    fn non_string_and_missing_values_yield_none() {
        let cwd = Path::new("/cwd");
        assert_eq!(
            lock_path_for_keys(&serde_json::json!({"path": 3}), &["path"], cwd),
            None
        );
        assert_eq!(
            lock_path_for_keys(&serde_json::json!({}), &["path"], cwd),
            None
        );
        assert_eq!(
            lock_path_for_keys(&serde_json::json!(null), &["path"], cwd),
            None
        );
    }

    #[test]
    fn relative_aliases_collapse_to_one_key() {
        let cwd = Path::new("/cwd/project");
        let expected = lock_path_for_path(Path::new("/cwd/project/src/main.rs"), cwd);
        for alias in [
            "src/main.rs",
            "./src/main.rs",
            "src/../src/main.rs",
            "/cwd/project/src/main.rs",
        ] {
            assert_eq!(
                lock_path_for_path(Path::new(alias), cwd),
                expected,
                "{alias}"
            );
        }
    }

    #[cfg(unix)]
    #[test]
    fn parent_after_symlink_resolves_from_the_target() {
        let dir = tempfile::tempdir().unwrap();
        let real = dir.path().join("real");
        let nested = real.join("nested");
        std::fs::create_dir_all(&nested).unwrap();
        std::os::unix::fs::symlink(&nested, dir.path().join("alias")).unwrap();
        assert_eq!(
            lock_path_for_path(Path::new("alias/../new.txt"), dir.path()),
            lock_path_for_path(Path::new("real/new.txt"), dir.path()),
        );
        assert_ne!(
            lock_path_for_path(Path::new("alias/../new.txt"), dir.path()),
            lock_path_for_path(Path::new("new.txt"), dir.path()),
        );
    }

    #[test]
    fn symlinked_ancestor_shares_the_real_key() {
        let dir = tempfile::tempdir().unwrap();
        let real = dir.path().join("real");
        std::fs::create_dir(&real).unwrap();
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(&real, dir.path().join("alias")).unwrap();
            assert_eq!(
                lock_path_for_path(Path::new("alias/new.txt"), dir.path()),
                lock_path_for_path(Path::new("real/new.txt"), dir.path()),
            );
        }
    }
}

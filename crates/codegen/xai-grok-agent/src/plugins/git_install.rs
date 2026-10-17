//! Git-based plugin installation.
//!
//! Handles cloning repos, copying local directories into the managed
//! `installed-plugins` snapshot (not live symlinks), discovering plugins within
//! installed sources, and URL/path parsing. Trusted / user-home local installs
//! are re-copied at session spawn / reload by [`super::local_refresh`].

use std::path::{Path, PathBuf};

use super::install_registry::{
    InstallError, InstallKind, InstallRegistry, InstalledRepo, RepoPlugin,
};
use super::manifest::{ManifestLoadResult, load_manifest, name_from_dirname};

/// Source of a plugin installation.
#[derive(Debug, Clone)]
pub enum InstallSource {
    /// Remote git repo or Git-supported local repository path — will be cloned.
    Git {
        url: String,
        git_ref: Option<String>,
        git_sha: Option<String>,
        subdir: Option<String>,
    },
    /// Local directory — will be copied into the managed install snapshot.
    Local {
        path: PathBuf,
        subdir: Option<String>,
    },
}

/// Result of installing a source.
pub struct InstallResult {
    pub repo_key: String,
    pub repo_path: PathBuf,
    pub plugins: Vec<DiscoveredPlugin>,
    pub commit: Option<String>,
    kind: InstallKind,
}

/// A plugin discovered within an installed source.
pub struct DiscoveredPlugin {
    pub name: String,
    pub subdir: Option<String>,
    pub version: Option<String>,
}

/// Parse an install source string into an `InstallSource`.
///
/// Supports:
/// - `https://github.com/user/repo` — Git HTTPS
/// - `https://github.com/user/repo@v1.0.0` — Git with ref
/// - `https://github.com/user/repo#subdir` — Git with subdirectory
/// - `git@github.com:user/repo.git` — Git SSH
/// - `user/repo` — GitHub shorthand (expands to `https://github.com/user/repo`)
/// - `user/repo@v1.0.0` — GitHub shorthand with ref
/// - `user/repo#subdir` — GitHub shorthand with subdirectory
/// - `/path/to/dir` or `./relative` or `~/dir` — Local
/// - `/path/to/dir#subdir` — Local with subdirectory
pub fn parse_install_source(input: &str, cwd: &Path) -> InstallSource {
    // Split on # for subdir
    let (main, subdir) = match input.rsplit_once('#') {
        Some((m, s)) if !s.is_empty() => (m, Some(s.to_string())),
        _ => (input, None),
    };

    // Detect if this is a URL or local path
    if main.contains("://") || main.contains("git@") {
        // Git URL — split on @ for ref (but not the git@ prefix)
        let (url, git_ref) = if main.starts_with("git@") {
            // SSH URL: git@host:user/repo.git@ref
            // The @ in git@ is part of the URL, look for @ after the first :
            if let Some(colon_pos) = main.find(':') {
                let after_colon = &main[colon_pos + 1..];
                if let Some(at_pos) = after_colon.rfind('@') {
                    let url = format!("{}:{}", &main[..colon_pos], &after_colon[..at_pos]);
                    let git_ref = after_colon[at_pos + 1..].to_string();
                    (url, Some(git_ref))
                } else {
                    (main.to_string(), None)
                }
            } else {
                (main.to_string(), None)
            }
        } else {
            // HTTPS URL: https://host/user/repo@ref
            match main.rsplit_once('@') {
                Some((url, r)) if !r.is_empty() => (url.to_string(), Some(r.to_string())),
                _ => (main.to_string(), None),
            }
        };

        InstallSource::Git {
            url,
            git_ref,
            git_sha: None,
            subdir,
        }
    } else if is_github_shorthand(main) {
        // GitHub shorthand: owner/repo or owner/repo@ref
        let (owner_repo, git_ref) = match main.rsplit_once('@') {
            Some((or, r)) if !r.is_empty() => (or, Some(r.to_string())),
            _ => (main, None),
        };
        InstallSource::Git {
            url: format!("https://github.com/{owner_repo}"),
            git_ref,
            git_sha: None,
            subdir,
        }
    } else {
        // Local path
        let path = if let Some(stripped) = main.strip_prefix("~/") {
            xai_dirs::home_dir()
                .unwrap_or_else(|| PathBuf::from("."))
                .join(stripped)
        } else {
            let p = PathBuf::from(main);
            if p.is_relative() { cwd.join(p) } else { p }
        };

        InstallSource::Local { path, subdir }
    }
}

/// A full commit sha (40-hex SHA-1 or 64-hex SHA-256) — the only thing the
/// pin policy accepts; branches, tags, and short prefixes are mutable or forgeable.
pub fn is_full_commit_sha(s: &str) -> bool {
    (s.len() == 40 || s.len() == 64) && s.bytes().all(|b| b.is_ascii_hexdigit())
}

fn validate_git_operand<'a>(value: &'a str, kind: &str) -> Result<&'a str, String> {
    let value = value.trim();
    if value.is_empty() {
        return Err(format!("empty git {kind}"));
    }
    if value.contains('\0') {
        return Err(format!("git {kind} contains NUL"));
    }
    if value.starts_with('-') {
        return Err(format!("git {kind} may not begin with '-'"));
    }
    Ok(value)
}

/// Validate and trim a Git repository URL or path used as a CLI operand.
pub fn validate_git_url(url: &str) -> Result<&str, String> {
    validate_git_operand(url, "URL")
}

/// Validate and trim a Git ref used as a CLI operand.
pub fn validate_git_ref(git_ref: &str) -> Result<&str, String> {
    validate_git_operand(git_ref, "ref")
}

/// Validate and trim a full Git commit object ID.
pub fn validate_git_sha(sha: &str) -> Result<&str, String> {
    let sha = sha.trim();
    if sha.contains('\0') {
        return Err("git commit SHA contains NUL".into());
    }
    if sha.starts_with('-') {
        return Err("git commit SHA may not begin with '-'".into());
    }
    if is_full_commit_sha(sha) {
        Ok(sha)
    } else {
        Err("git commit SHA must be 40 or 64 hexadecimal characters".into())
    }
}

/// The require-sha gate every remote plugin fetch goes through: policy on + no
/// full-hex pin → typed refusal. Local-directory installs are exempt (the
/// operator controls that disk; nothing is fetched).
pub fn ensure_pinned(
    require_sha: bool,
    sha: Option<&str>,
    plugin: &str,
    url: &str,
) -> Result<(), InstallError> {
    if !require_sha {
        return Ok(());
    }
    if sha.map(str::trim).is_some_and(is_full_commit_sha) {
        return Ok(());
    }
    tracing::warn!(
        plugin,
        url,
        "refusing unpinned remote plugin code (require_sha)"
    );
    Err(InstallError::UnpinnedRemoteRefused {
        plugin: plugin.to_owned(),
        url: url.to_owned(),
    })
}

/// Prefer an explicit supplied SHA; if only `git_ref` is a full commit SHA,
/// hoist it into the SHA slot so the verified clone path is used. Catalog pins
/// published as `ref` still need this.
pub fn hoist_pin_slots<'a>(
    git_ref: Option<&'a str>,
    git_sha: Option<&'a str>,
) -> (Option<&'a str>, Option<&'a str>) {
    match git_sha.map(str::trim) {
        Some(s) => (git_ref, Some(s)),
        None => match git_ref.map(str::trim).filter(|s| is_full_commit_sha(s)) {
            Some(s) => (None, Some(s)),
            None => (git_ref, None),
        },
    }
}

/// Check if a string looks like a GitHub `owner/repo` shorthand.
///
/// Returns `true` for strings like `user/repo` or `user/repo@v1.0`
/// that should be expanded to `https://github.com/user/repo`.
/// Avoids false positives for local paths (`/abs`, `./rel`, `~/home`).
fn is_github_shorthand(s: &str) -> bool {
    // Local path indicators.
    if s.starts_with('/') || s.starts_with('.') || s.starts_with('~') {
        return false;
    }
    // Strip optional @ref suffix before checking the owner/repo pattern.
    let base = match s.rsplit_once('@') {
        Some((b, r)) if !r.is_empty() => b,
        _ => s,
    };
    // Must be exactly owner/repo (two non-empty segments separated by one /).
    let parts: Vec<&str> = base.splitn(3, '/').collect();
    parts.len() == 2 && !parts[0].is_empty() && !parts[1].is_empty()
}

fn repo_source_id(source: &InstallSource) -> String {
    match source {
        InstallSource::Git { url, subdir, .. } => match subdir {
            Some(sub) => format!("{url}#{sub}"),
            None => url.clone(),
        },
        InstallSource::Local { path, subdir } => {
            let path_str = path.to_str().unwrap_or("local");
            match subdir {
                Some(sub) => format!("{path_str}#{sub}"),
                None => path_str.to_string(),
            }
        }
    }
}

/// Create a uniquely owned staging directory in managed storage.
pub fn create_staging_dir(registry: &InstallRegistry) -> Result<PathBuf, InstallError> {
    use std::sync::atomic::{AtomicU64, Ordering};
    static NEXT: AtomicU64 = AtomicU64::new(0);
    let dir = registry.install_dir();
    std::fs::create_dir_all(dir).map_err(|source| InstallError::Io {
        path: dir.to_path_buf(),
        source,
    })?;
    loop {
        let nonce = NEXT.fetch_add(1, Ordering::Relaxed);
        let nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or_default();
        let path = dir.join(format!(".staging-{}-{nanos}-{nonce}", std::process::id()));
        match std::fs::create_dir(&path) {
            Ok(()) => return Ok(path),
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(source) => return Err(InstallError::Io { path, source }),
        }
    }
}

pub(super) struct StagingDir(pub(super) PathBuf);
impl Drop for StagingDir {
    fn drop(&mut self) {
        if self.0.join(".open-grok-transaction").exists()
            && self
                .0
                .parent()
                .is_some_and(|parent| parent.join(".plugin-transaction.json").exists())
        {
            return;
        }
        let _ = remove_repo_path(&self.0);
    }
}

/// Install a source without registering it. Callers that persist installs should
/// use `install_and_register`, which also rolls back a failed registry save.
pub fn install_from_source(
    source: &InstallSource,
    registry: &InstallRegistry,
    require_sha: bool,
) -> Result<InstallResult, InstallError> {
    install_from_source_with_label(source, registry, require_sha, None)
}

pub fn install_from_source_with_label(
    source: &InstallSource,
    registry: &InstallRegistry,
    require_sha: bool,
    plugin_label: Option<&str>,
) -> Result<InstallResult, InstallError> {
    let (result, stage) = stage_from_source(source, registry, require_sha, plugin_label)?;
    if std::fs::symlink_metadata(&result.repo_path).is_ok() {
        return Err(InstallError::InstallFailed {
            detail: format!(
                "unregistered plugin path already exists: {}",
                result.repo_path.display()
            ),
        });
    }
    std::fs::rename(&stage.0, &result.repo_path).map_err(|source| InstallError::Io {
        path: result.repo_path.clone(),
        source,
    })?;
    Ok(result)
}

/// Stage, validate, and persist an install as one recoverable transaction.
pub fn install_and_register(
    source: &InstallSource,
    registry: &mut InstallRegistry,
    require_sha: bool,
    plugin_label: Option<&str>,
    marketplace: Option<super::install_registry::MarketplaceProvenance>,
) -> Result<InstallResult, InstallError> {
    install_and_register_with(
        source,
        registry,
        require_sha,
        plugin_label,
        marketplace,
        |registry, key, repo, stage| registry.publish_staged(key, repo, stage),
    )
}

fn install_and_register_with(
    source: &InstallSource,
    registry: &mut InstallRegistry,
    require_sha: bool,
    plugin_label: Option<&str>,
    marketplace: Option<super::install_registry::MarketplaceProvenance>,
    publish: impl FnOnce(&mut InstallRegistry, &str, InstalledRepo, &Path) -> Result<(), InstallError>,
) -> Result<InstallResult, InstallError> {
    let (result, stage) = stage_from_source(source, registry, require_sha, plugin_label)?;
    let mut repo = build_installed_repo(&result, source);
    repo.marketplace = marketplace;
    publish(registry, &result.repo_key, repo, &stage.0)?;
    Ok(result)
}

fn stage_from_source(
    source: &InstallSource,
    registry: &InstallRegistry,
    require_sha: bool,
    plugin_label: Option<&str>,
) -> Result<(InstallResult, StagingDir), InstallError> {
    let source = &normalize_install_source(source)?;
    if let InstallSource::Git { url, git_sha, .. } = source {
        ensure_pinned(
            require_sha,
            git_sha.as_deref(),
            plugin_label.unwrap_or(url),
            url,
        )?;
    }
    let repo_key = InstallRegistry::repo_key(&repo_source_id(source));
    if registry.get_repo(&repo_key).is_some() {
        return Err(InstallError::AlreadyInstalled { key: repo_key });
    }
    let repo_path = registry.install_dir().join(&repo_key);
    let stage = StagingDir(create_staging_dir(registry)?);
    let (kind, commit) = match source {
        InstallSource::Git {
            url,
            git_ref,
            git_sha,
            subdir,
        } => {
            clone_repo(url, git_ref.as_deref(), git_sha.as_deref(), &stage.0)?;
            let commit = read_head_commit(&stage.0);
            (
                InstallKind::Git {
                    url: url.clone(),
                    git_ref: git_sha.clone().or_else(|| git_ref.clone()),
                    commit: commit.clone().unwrap_or_default(),
                    subdir: subdir.clone(),
                },
                commit,
            )
        }
        InstallSource::Local { path, subdir } => {
            if !path.is_dir() {
                return Err(InstallError::InstallFailed {
                    detail: format!("local path is not a directory: {}", path.display()),
                });
            }
            copy_dir_recursive(path, &stage.0).map_err(|source| InstallError::Io {
                path: stage.0.clone(),
                source,
            })?;
            (
                InstallKind::Local {
                    source_path: path.clone(),
                    subdir: subdir.clone(),
                },
                None,
            )
        }
    };
    let subdir = match source {
        InstallSource::Git { subdir, .. } | InstallSource::Local { subdir, .. } => {
            subdir.as_deref()
        }
    };
    let plugins = discover_plugins_in_dir_as(&stage.0, subdir, &repo_path)?;
    if plugins.is_empty() {
        return Err(InstallError::InstallFailed {
            detail: "no plugins found in the source (no plugin.json or convention components)"
                .to_string(),
        });
    }
    Ok((
        InstallResult {
            repo_key,
            repo_path,
            plugins,
            commit,
            kind,
        },
        stage,
    ))
}

fn normalize_install_source(source: &InstallSource) -> Result<InstallSource, InstallError> {
    match source {
        InstallSource::Git {
            url,
            git_ref,
            git_sha,
            subdir,
        } => {
            let (git_ref, git_sha) = hoist_pin_slots(git_ref.as_deref(), git_sha.as_deref());
            let (url, git_ref, git_sha) = clone_operands(url, git_ref, git_sha)?;
            Ok(InstallSource::Git {
                url: url.to_owned(),
                git_ref: git_ref.map(str::to_owned),
                git_sha: git_sha.map(str::to_owned),
                subdir: subdir.clone(),
            })
        }
        local @ InstallSource::Local { .. } => Ok(local.clone()),
    }
}

/// Argv for `git remote add` with options terminated before free operands.
pub fn remote_add_args(url: &str) -> [&str; 5] {
    ["remote", "add", "--", "origin", url]
}

/// Argv for shallow `git fetch` of a SHA with options terminated before free operands.
pub fn fetch_sha_args(sha: &str) -> [&str; 6] {
    ["fetch", "--depth", "1", "--", "origin", sha]
}

/// Validate/normalize URL + optional ref/SHA for pre-trust clone paths.
pub fn clone_operands<'a>(
    url: &'a str,
    git_ref: Option<&'a str>,
    git_sha: Option<&'a str>,
) -> Result<(&'a str, Option<&'a str>, Option<&'a str>), InstallError> {
    let url = validate_git_url(url).map_err(|detail| InstallError::InstallFailed { detail })?;
    let git_ref = git_ref
        .map(validate_git_ref)
        .transpose()
        .map_err(|detail| InstallError::InstallFailed { detail })?;
    let git_sha = git_sha
        .map(validate_git_sha)
        .transpose()
        .map_err(|detail| InstallError::InstallFailed { detail })?;
    Ok((url, git_ref, git_sha))
}

/// Clone a git repo using the `git` CLI (supports shallow clone, SSH, etc.;
/// optionally SHA-pinned via `git_sha`).
fn clone_repo(
    url: &str,
    git_ref: Option<&str>,
    git_sha: Option<&str>,
    target: &Path,
) -> Result<(), InstallError> {
    let (url, git_ref, git_sha) = clone_operands(url, git_ref, git_sha)?;
    if let Some(sha) = git_sha {
        if git_ref.is_some() {
            tracing::debug!(git_ref, sha, "git_sha takes precedence over git_ref");
        }
        return clone_repo_at_sha(url, sha, target);
    }

    // Match marketplace cache: BatchMode SSH, empty ASKPASS, skip LFS smudge.
    let mut cmd = xai_tty_utils::git_command();
    cmd.arg("clone").arg("--depth").arg("1");

    if let Some(r) = git_ref {
        cmd.arg("--branch").arg(r);
    }

    cmd.arg("--").arg(url).arg(target);

    tracing::info!(url, target = %target.display(), "cloning plugin repo");

    let output = cmd.output().map_err(|e| InstallError::InstallFailed {
        detail: format!("failed to run git clone: {e}"),
    })?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        // Clean up partial clone
        let _ = std::fs::remove_dir_all(target);
        return Err(InstallError::InstallFailed {
            detail: format!(
                "git clone failed (exit {}):\n{stderr}",
                output.status.code().unwrap_or(-1)
            ),
        });
    }

    Ok(())
}

fn clone_repo_at_sha(url: &str, sha: &str, target: &Path) -> Result<(), InstallError> {
    let url = validate_git_url(url).map_err(|detail| InstallError::InstallFailed { detail })?;
    let sha = validate_git_sha(sha).map_err(|detail| InstallError::InstallFailed { detail })?;

    tracing::info!(url, sha, target = %target.display(), "cloning plugin repo at SHA");

    std::fs::create_dir_all(target).map_err(|e| InstallError::Io {
        path: target.to_path_buf(),
        source: e,
    })?;

    let wrap_fail = |detail: String| {
        let _ = std::fs::remove_dir_all(target);
        InstallError::InstallFailed { detail }
    };

    run_git_in(target, &["init", "--quiet"]).map_err(wrap_fail)?;
    run_git_in(target, &remote_add_args(url)).map_err(wrap_fail)?;
    run_git_in(target, &fetch_sha_args(sha))
        .map_err(|d| wrap_fail(format!("fetch-by-sha failed: {d}")))?;
    run_git_in(target, &["checkout", "--quiet", "FETCH_HEAD"]).map_err(wrap_fail)?;

    let head = read_head_commit(target).ok_or_else(|| {
        let _ = std::fs::remove_dir_all(target);
        InstallError::InstallFailed {
            detail: "could not read HEAD commit after SHA-pinned clone".into(),
        }
    })?;
    if !head.eq_ignore_ascii_case(sha) {
        let _ = std::fs::remove_dir_all(target);
        return Err(InstallError::ShaMismatch {
            expected: sha.to_string(),
            actual: head,
        });
    }

    Ok(())
}

fn run_git_in(cwd: &Path, args: &[&str]) -> Result<(), String> {
    run_git_in_capture(cwd, args).map(|_| ())
}

fn run_git_in_capture(cwd: &Path, args: &[&str]) -> Result<std::process::Output, String> {
    // Same auth/LFS/SSH suppression as marketplace cache clones.
    let mut cmd = xai_tty_utils::git_command();
    cmd.args(args).current_dir(cwd);
    let output = cmd
        .output()
        .map_err(|e| format!("failed to run git {}: {e}", args.first().unwrap_or(&"")))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(format!(
            "git {} failed (exit {}):\n{stderr}",
            args.first().unwrap_or(&""),
            output.status.code().unwrap_or(-1)
        ));
    }
    Ok(output)
}

/// Read the HEAD commit SHA from a git repo.
fn read_head_commit(repo_path: &Path) -> Option<String> {
    run_git_in_capture(repo_path, &["rev-parse", "HEAD"])
        .ok()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
}

/// Remove a repo path (handles both symlinks and directories).
pub fn remove_repo_path(path: &Path) -> Result<(), InstallError> {
    if path.is_symlink() {
        std::fs::remove_file(path).map_err(|e| InstallError::Io {
            path: path.to_path_buf(),
            source: e,
        })?;
    } else if path.is_dir() {
        std::fs::remove_dir_all(path).map_err(|e| InstallError::Io {
            path: path.to_path_buf(),
            source: e,
        })?;
    }
    Ok(())
}

/// Clean up plugin data directories for all plugins in a repo.
///
/// Each plugin has a data dir at `~/.opengrok/plugin-data/<plugin_id>/`.
/// This iterates all plugins in the repo and removes their data dirs.
pub fn cleanup_plugin_data(repo: &InstalledRepo, scope: super::discovery::PluginScope) {
    let plugin_data_base = xai_grok_config::grok_home().join("plugin-data");

    for (plugin_name, repo_plugin) in &repo.plugins {
        let plugin_root = match &repo_plugin.subdir {
            Some(sub) => repo.path.join(sub),
            None => repo.path.clone(),
        };
        let id = super::discovery::PluginId::new(scope, &plugin_root, plugin_name);
        let data_dir = plugin_data_base.join(&id.0);
        if data_dir.is_dir() {
            tracing::info!(
                plugin = plugin_name,
                data_dir = %data_dir.display(),
                "cleaning up plugin data directory"
            );
            let _ = std::fs::remove_dir_all(&data_dir);
        }
    }
}

/// Discover plugins within a directory.
///
/// Discovery logic:
/// 1. If `subdir` is specified, only look in that subdirectory
/// 2. If root has plugin.json or convention components, it's a single plugin
/// 3. Otherwise, scan immediate subdirectories for plugins
#[cfg(test)]
pub(super) fn discover_plugins_in_dir(
    root: &Path,
    subdir: Option<&str>,
) -> Result<Vec<DiscoveredPlugin>, InstallError> {
    discover_plugins_in_dir_as(root, subdir, root)
}

pub(super) fn discover_plugins_in_dir_as(
    root: &Path,
    subdir: Option<&str>,
    logical_root: &Path,
) -> Result<Vec<DiscoveredPlugin>, InstallError> {
    let scan_root = match subdir {
        Some(s) => {
            let candidate = Path::new(s);
            if candidate.is_absolute()
                || candidate.components().any(|c| {
                    matches!(
                        c,
                        std::path::Component::ParentDir
                            | std::path::Component::RootDir
                            | std::path::Component::Prefix(_)
                    )
                })
            {
                return Err(InstallError::InstallFailed {
                    detail: format!("subdirectory '{s}' escapes the source root"),
                });
            }
            let sub_path = root.join(s);
            if !sub_path.is_dir() {
                return Err(InstallError::InstallFailed {
                    detail: format!("subdirectory '{s}' not found in source"),
                });
            }
            sub_path
        }
        None => root.to_path_buf(),
    };

    // Check if scan_root itself is a plugin
    let name_path = if subdir.is_none() {
        logical_root
    } else {
        &scan_root
    };
    if let Some(plugin) = try_load_plugin(&scan_root, subdir, name_path) {
        return Ok(vec![plugin]);
    }

    // Scan immediate subdirectories
    let mut plugins = Vec::new();
    if let Ok(entries) = std::fs::read_dir(&scan_root) {
        for entry in entries.flatten() {
            if entry.file_type().map(|ft| ft.is_dir()).unwrap_or(false) {
                let entry_path = entry.path();
                let entry_name = entry.file_name().to_str().unwrap_or_default().to_string();
                let sub = match subdir {
                    Some(s) => Some(format!("{s}/{entry_name}")),
                    None => Some(entry_name),
                };
                if let Some(plugin) = try_load_plugin(&entry_path, sub.as_deref(), &entry_path) {
                    plugins.push(plugin);
                }
            }
        }
    }

    Ok(plugins)
}

/// Try to load a plugin from a directory.
///
/// Returns `Some` if the directory contains a plugin.json or convention components.
fn try_load_plugin(dir: &Path, subdir: Option<&str>, name_path: &Path) -> Option<DiscoveredPlugin> {
    // Try loading manifest
    match load_manifest(dir) {
        Ok(ManifestLoadResult::Found(manifest)) => {
            return Some(DiscoveredPlugin {
                name: manifest.name.clone(),
                subdir: subdir.map(|s| s.to_string()),
                version: manifest.version.clone(),
            });
        }
        Ok(ManifestLoadResult::NotFound) => {}
        Err(_) => {}
    }

    // Check convention components
    let has_skills = dir.join("skills").is_dir();
    let has_agents = dir.join("agents").is_dir();
    let has_mcp = dir.join(".mcp.json").is_file();
    let has_hooks = dir.join("hooks").join("hooks.json").is_file();

    if has_skills || has_agents || has_mcp || has_hooks {
        let name = name_from_dirname(name_path)?;
        Some(DiscoveredPlugin {
            name,
            subdir: subdir.map(|s| s.to_string()),
            version: None,
        })
    } else {
        None
    }
}

/// Build an `InstalledRepo` from the normalized install result.
pub fn build_installed_repo(result: &InstallResult, _: &InstallSource) -> InstalledRepo {
    let now = chrono::Utc::now().to_rfc3339();
    InstalledRepo {
        kind: result.kind.clone(),
        installed_at: now.clone(),
        updated_at: now,
        path: result.repo_path.clone(),
        plugins: repo_plugin_map(&result.plugins),
        marketplace: None,
    }
}

/// Build the `InstalledRepo.plugins` map from discovered plugins.
pub fn repo_plugin_map(
    plugins: &[DiscoveredPlugin],
) -> std::collections::HashMap<String, RepoPlugin> {
    plugins
        .iter()
        .map(|p| {
            (
                p.name.clone(),
                RepoPlugin {
                    subdir: p.subdir.clone(),
                    version: p.version.clone(),
                },
            )
        })
        .collect()
}

/// Result of updating a repo.
pub struct UpdateResult {
    pub repo_key: String,
    pub old_commit: Option<String>,
    pub new_commit: Option<String>,
    pub changed: bool,
    pub plugins: Vec<DiscoveredPlugin>,
}

/// Status of an update attempt.
pub enum UpdateStatus {
    /// Repo was updated successfully.
    Updated(UpdateResult),
    /// Repo is pinned to a tag or commit — no automatic update.
    Pinned { ref_name: String },
    /// Local install — explicit update is a no-op; session spawn / reload uses
    /// [`super::local_refresh`] to re-copy trusted sources (install is a full
    /// directory copy, not a live symlink).
    LiveLocal,
}

/// Update an installed repo in an independent checkout, then publish its tree
/// and registry record together. Network operations never hold the registry lock.
pub fn update_registered_repo(
    repo_key: &str,
    repo: &InstalledRepo,
    registry: &mut InstallRegistry,
    require_sha: bool,
) -> Result<UpdateStatus, InstallError> {
    update_registered_repo_with(
        repo_key,
        repo,
        registry,
        require_sha,
        || {},
        InstallRegistry::save_atomic,
    )
}

fn update_registered_repo_with(
    repo_key: &str,
    repo: &InstalledRepo,
    registry: &mut InstallRegistry,
    require_sha: bool,
    before_publish: impl FnOnce(),
    save: impl FnOnce(&InstallRegistry) -> Result<(), InstallError>,
) -> Result<UpdateStatus, InstallError> {
    match &repo.kind {
        InstallKind::Local { .. } => Ok(UpdateStatus::LiveLocal),
        InstallKind::Git {
            url,
            git_ref,
            commit,
            subdir,
        } => {
            // Check if pinned
            if let Some(r) = git_ref {
                // Heuristic: if the ref looks like a commit hash
                // or a version tag (starts with v and contains dots), it's pinned.
                let is_tag_or_commit =
                    is_full_commit_sha(r) || (r.starts_with('v') && r.contains('.'));
                if is_tag_or_commit {
                    return Ok(UpdateStatus::Pinned {
                        ref_name: r.clone(),
                    });
                }
            }
            // An update pulls whatever the mutable ref now points at — the same
            // unpinned fetch the install gate refuses.
            ensure_pinned(require_sha, None, repo_key, url)?;
            require_checkout_identity()?;

            let old_commit = Some(commit.clone());

            let identity =
                super::install_registry::directory_identity(&repo.path).ok_or_else(|| {
                    InstallError::InstallFailed {
                        detail: format!(
                            "repo directory not found or unsafe: {}",
                            repo.path.display()
                        ),
                    }
                })?;
            let stage = StagingDir(create_staging_dir(registry)?);
            #[cfg(windows)]
            verify_readonly_cleanup(&stage.0)?;
            copy_git_checkout(&repo.path, &stage.0).map_err(|source| InstallError::Io {
                path: repo.path.clone(),
                source,
            })?;
            let original = checkout_fingerprint(&stage.0)?;
            if !super::install_registry::same_directory(&repo.path, &identity)
                || checkout_fingerprint(&repo.path)? != original
            {
                return Err(InstallError::InstallFailed {
                    detail: "plugin checkout changed while it was copied".into(),
                });
            }
            let config = validate_git_checkout(&stage.0)?;
            let git_config = StagedGitConfig::new(&stage.0, &config)?;
            let mut cmd = staged_git_command(&stage.0)?;
            git_config.isolate(&mut cmd);
            cmd.args([
                "-c",
                "gc.auto=0",
                "-c",
                "maintenance.auto=false",
                "pull",
                "--ff-only",
                "--no-recurse-submodules",
            ]);
            let output = cmd.output().map_err(|e| InstallError::InstallFailed {
                detail: format!("failed to run git pull: {e}"),
            })?;

            if !output.status.success() {
                let stderr = String::from_utf8_lossy(&output.stderr);
                return Err(InstallError::InstallFailed {
                    detail: format!(
                        "git pull failed (exit {}):\n{stderr}",
                        output.status.code().unwrap_or(-1)
                    ),
                });
            }

            // Filters were disabled for the pull. Do not publish files whose
            // checkout requires a filter we deliberately did not execute.
            git_config.validate_attributes(&stage.0)?;
            git_config.restore()?;
            if std::fs::symlink_metadata(stage.0.join(".gitmodules")).is_ok() {
                return Err(InstallError::InstallFailed {
                    detail: "Git submodules are unsupported for staged plugin updates".into(),
                });
            }

            let new_commit = staged_head_commit(&stage.0)?;
            let changed = old_commit.as_deref() != new_commit.as_deref();

            // Re-discover plugins (new ones may have been added)
            let plugins = discover_plugins_in_dir_as(&stage.0, subdir.as_deref(), &repo.path)?;

            before_publish();
            let validate_original = |path: &Path| {
                if !super::install_registry::same_directory(path, &identity)
                    || checkout_fingerprint(path)? != original
                {
                    return Err(InstallError::InstallFailed {
                        detail: "plugin checkout changed during update".into(),
                    });
                }
                Ok(())
            };
            if changed {
                let mut updated = repo.clone();
                if let InstallKind::Git { commit, .. } = &mut updated.kind {
                    *commit = new_commit
                        .clone()
                        .ok_or_else(|| InstallError::InstallFailed {
                            detail: "could not read updated plugin HEAD".into(),
                        })?;
                }
                updated.updated_at = chrono::Utc::now().to_rfc3339();
                updated.plugins = repo_plugin_map(&plugins);
                registry.publish_staged_if_matches_checked(
                    repo_key,
                    repo,
                    updated,
                    &stage.0,
                    validate_original,
                    save,
                )?;
            } else {
                registry.validate_selected(repo_key, repo, validate_original)?;
            }

            Ok(UpdateStatus::Updated(UpdateResult {
                repo_key: repo_key.to_string(),
                old_commit,
                new_commit,
                changed,
                plugins,
            }))
        }
    }
}

/// Copy every regular file and symlink without following links or sharing Git
/// objects. Reject Git layouts which may refer to another working tree.
fn copy_git_checkout(source: &Path, dest: &Path) -> std::io::Result<()> {
    fn copy(source: &Path, dest: &Path, rel: &Path) -> std::io::Result<()> {
        let meta = std::fs::symlink_metadata(source)?;
        if meta.file_type().is_symlink() {
            if rel.starts_with(".git") || rel.file_name().is_some_and(|n| n == ".git") {
                return Err(std::io::Error::other(
                    "symlink in Git metadata is unsupported",
                ));
            }
            let target = std::fs::read_link(source)?;
            #[cfg(unix)]
            std::os::unix::fs::symlink(target, dest)?;
            #[cfg(not(unix))]
            {
                let _ = target;
                return Err(std::io::Error::other(
                    "symlinked checkout is unsupported on this platform",
                ));
            }
        } else if meta.is_dir() {
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                if meta.permissions().mode() & 0o200 == 0 {
                    return Err(std::io::Error::other(
                        "read-only checkout directories are unsupported",
                    ));
                }
            }
            #[cfg(not(any(unix, windows)))]
            if meta.permissions().readonly() {
                return Err(std::io::Error::other(
                    "read-only checkout directories are unsupported",
                ));
            }
            if !rel.as_os_str().is_empty()
                && rel.file_name().is_some_and(|n| n == ".git")
                && rel != Path::new(".git")
            {
                return Err(std::io::Error::other("nested Git checkout is unsupported"));
            }
            std::fs::create_dir_all(dest)?;
            for entry in std::fs::read_dir(source)? {
                let entry = entry?;
                copy(
                    &entry.path(),
                    &dest.join(entry.file_name()),
                    &rel.join(entry.file_name()),
                )?;
            }
            std::fs::set_permissions(dest, meta.permissions())?;
        } else if meta.is_file() {
            #[cfg(not(any(unix, windows)))]
            if meta.permissions().readonly() {
                return Err(std::io::Error::other(
                    "read-only checkout files are unsupported",
                ));
            }
            if rel.file_name().is_some_and(|n| n == ".git") {
                return Err(std::io::Error::other(
                    "external Git directory or submodule is unsupported",
                ));
            }
            if rel.starts_with(".git")
                && rel
                    .file_name()
                    .is_some_and(|n| n.to_string_lossy().ends_with(".lock"))
            {
                return Err(std::io::Error::other("Git checkout has an active lock"));
            }
            std::fs::copy(source, dest)?;
            std::fs::set_permissions(dest, meta.permissions())?;
        } else {
            return Err(std::io::Error::other(
                "special file in plugin checkout is unsupported",
            ));
        }
        Ok(())
    }
    copy(source, dest, Path::new(""))
}

// Rust's Windows remove_dir_all uses the ignore-readonly disposition on
// supported filesystems. Verify it on this volume before staging a checkout,
// so a committed transaction cannot strand its read-only Git objects.
#[cfg(windows)]
fn verify_readonly_cleanup(stage: &Path) -> Result<(), InstallError> {
    let probe = stage.join(".open-grok-cleanup-probe");
    let file = probe.join("readonly");
    let run = || -> std::io::Result<()> {
        std::fs::create_dir(&probe)?;
        std::fs::write(&file, b"probe")?;
        let mut permissions = std::fs::metadata(&file)?.permissions();
        permissions.set_readonly(true);
        std::fs::set_permissions(&file, permissions)?;
        std::fs::remove_dir_all(&probe)
    };
    if let Err(source) = run() {
        // Only this disposable probe is changed on failure.
        if let Ok(metadata) = std::fs::metadata(&file) {
            let mut permissions = metadata.permissions();
            permissions.set_readonly(false);
            let _ = std::fs::set_permissions(&file, permissions);
        }
        let _ = std::fs::remove_dir_all(&probe);
        return Err(InstallError::Io {
            path: probe,
            source,
        });
    }
    Ok(())
}

fn require_checkout_identity() -> Result<(), InstallError> {
    #[cfg(any(unix, windows))]
    {
        Ok(())
    }
    #[cfg(not(any(unix, windows)))]
    {
        Err(InstallError::InstallFailed {
            detail: "staged Git updates require a filesystem with stable directory identity".into(),
        })
    }
}

/// Content and mode snapshot, including ignored files, Git metadata and links.
fn checkout_fingerprint(root: &Path) -> Result<Vec<u8>, InstallError> {
    use sha2::{Digest, Sha256};
    use std::io::Read;
    fn walk(path: &Path, rel: &Path, hash: &mut Sha256) -> std::io::Result<()> {
        let meta = std::fs::symlink_metadata(path)?;
        let name = rel
            .to_str()
            .ok_or_else(|| std::io::Error::other("non-UTF-8 checkout path is unsupported"))?;
        hash.update((name.len() as u64).to_le_bytes());
        hash.update(name.as_bytes());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            hash.update(meta.permissions().mode().to_le_bytes());
        }
        #[cfg(not(unix))]
        hash.update([u8::from(meta.permissions().readonly())]);
        if meta.file_type().is_symlink() {
            hash.update(b"l");
            let target = std::fs::read_link(path)?;
            let target = target
                .to_str()
                .ok_or_else(|| std::io::Error::other("non-UTF-8 symlink target is unsupported"))?;
            hash.update((target.len() as u64).to_le_bytes());
            hash.update(target.as_bytes());
        } else if meta.is_dir() {
            hash.update(b"d");
            let mut entries = std::fs::read_dir(path)?.collect::<Result<Vec<_>, _>>()?;
            entries.sort_by_key(|entry| entry.file_name());
            for entry in entries {
                walk(&entry.path(), &rel.join(entry.file_name()), hash)?;
            }
        } else if meta.is_file() {
            hash.update(b"f");
            hash.update(meta.len().to_le_bytes());
            let mut file = std::fs::File::open(path)?;
            let mut buf = [0u8; 8192];
            loop {
                let count = file.read(&mut buf)?;
                if count == 0 {
                    break;
                }
                hash.update(&buf[..count]);
            }
        } else {
            return Err(std::io::Error::other(
                "special file in plugin checkout is unsupported",
            ));
        }
        Ok(())
    }
    let mut hash = Sha256::new();
    walk(root, Path::new(""), &mut hash).map_err(|source| InstallError::Io {
        path: root.to_path_buf(),
        source,
    })?;
    Ok(hash.finalize().to_vec())
}

fn staged_git_command(stage: &Path) -> Result<std::process::Command, InstallError> {
    let stage = std::fs::canonicalize(stage).map_err(|source| InstallError::Io {
        path: stage.to_path_buf(),
        source,
    })?;
    let mut cmd = xai_tty_utils::git_command_locking();
    // Inherited Git environment can point even an otherwise independent copy at
    // the original index, object database, config or working tree.
    for (key, _) in std::env::vars_os() {
        if key
            .to_string_lossy()
            .to_ascii_uppercase()
            .starts_with("GIT_")
        {
            cmd.env_remove(key);
        }
    }
    // Match git_command's hermetic Git installation, without inheriting an
    // unrelated GIT_EXEC_PATH from the surrounding process.
    if let Some(path) = std::env::var_os("GIT_BIN_PATH") {
        let path = PathBuf::from(path);
        let path = if path.is_relative() {
            std::env::current_dir().unwrap_or_default().join(path)
        } else {
            path
        };
        if path.file_name().is_some_and(|name| name == "git") {
            if let Some(parent) = path.parent() {
                cmd.env("GIT_EXEC_PATH", parent);
            }
        }
    }
    cmd.env("GIT_DIR", stage.join(".git"))
        .env("GIT_WORK_TREE", &stage)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_ASKPASS", "")
        .env("GIT_LFS_SKIP_SMUDGE", "1")
        .env("GIT_SSH_COMMAND", "ssh -o BatchMode=yes")
        .current_dir(stage);
    Ok(cmd)
}

fn validate_git_checkout(stage: &Path) -> Result<Vec<(String, String)>, InstallError> {
    let git_dir = stage.join(".git");
    if !std::fs::symlink_metadata(&git_dir).is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
    {
        return Err(InstallError::InstallFailed {
            detail: "plugin checkout must have an independent .git directory".into(),
        });
    }
    for path in [
        "commondir",
        "worktrees",
        "modules",
        "config.worktree",
        "objects/info/alternates",
        "objects/info/http-alternates",
    ] {
        if std::fs::symlink_metadata(git_dir.join(path)).is_ok() {
            return Err(InstallError::InstallFailed {
                detail: format!("unsupported Git metadata: {path}"),
            });
        }
    }
    if std::fs::symlink_metadata(stage.join(".gitmodules")).is_ok() {
        return Err(InstallError::InstallFailed {
            detail: "Git submodules are unsupported for staged plugin updates".into(),
        });
    }
    if std::fs::symlink_metadata(git_dir.join("hooks")).is_ok_and(|m| m.file_type().is_symlink()) {
        return Err(InstallError::InstallFailed {
            detail: "symlinked Git hooks are unsupported for staged plugin updates".into(),
        });
    }
    if std::fs::symlink_metadata(git_dir.join("info/attributes"))
        .is_ok_and(|m| m.file_type().is_symlink())
    {
        return Err(InstallError::InstallFailed {
            detail: "symlinked Git attributes are unsupported for staged plugin updates".into(),
        });
    }
    if let Ok(entries) = std::fs::read_dir(git_dir.join("hooks")) {
        for entry in entries {
            let entry = entry.map_err(|source| InstallError::Io {
                path: git_dir.join("hooks"),
                source,
            })?;
            if entry.file_name().to_string_lossy().ends_with(".sample") {
                continue;
            }
            let meta =
                std::fs::symlink_metadata(entry.path()).map_err(|source| InstallError::Io {
                    path: entry.path(),
                    source,
                })?;
            #[cfg(unix)]
            let active = {
                use std::os::unix::fs::PermissionsExt;
                meta.is_file() && meta.permissions().mode() & 0o111 != 0
            };
            #[cfg(not(unix))]
            let active = meta.is_file();
            if active {
                return Err(InstallError::InstallFailed {
                    detail: "active Git hooks are unsupported for staged plugin updates".into(),
                });
            }
        }
    }
    let output = staged_git_command(stage)?
        .args(["config", "--null", "--list", "--includes"])
        .output()
        .map_err(|source| InstallError::Io {
            path: git_dir.clone(),
            source,
        })?;
    if !output.status.success() {
        return Err(InstallError::InstallFailed {
            detail: "unable to inspect Git configuration for staged update".into(),
        });
    }
    let mut config = Vec::new();
    for item in output.stdout.split(|b| *b == 0).filter(|s| !s.is_empty()) {
        let mut parts = item.splitn(2, |b| *b == b'\n');
        let key = parts.next().unwrap_or_default();
        let original_key = std::str::from_utf8(key).map_err(|_| InstallError::InstallFailed {
            detail: "non-UTF-8 Git config key".into(),
        })?;
        let key = original_key.to_ascii_lowercase();
        // A key without an explicit value means true in Git config.
        let value = std::str::from_utf8(parts.next().unwrap_or(b"true")).map_err(|_| {
            InstallError::InstallFailed {
                detail: "non-UTF-8 Git config value".into(),
            }
        })?;
        if key == "core.worktree"
            || key == "core.hookspath"
            || key == "core.fsmonitor"
            || key == "core.attributesfile"
            || key == "extensions.worktreeconfig"
            || key.starts_with("include.")
            || key.starts_with("includeif.")
            || (key.starts_with("merge.") && key.ends_with(".driver"))
            || (key.starts_with("diff.")
                && (key.ends_with(".command") || key.ends_with(".textconv")))
            || (key.starts_with("remote.") && key.ends_with(".uploadpack"))
        {
            return Err(InstallError::InstallFailed {
                detail: format!("unsupported Git configuration for staged update: {key}"),
            });
        }
        if !key.starts_with("filter.") {
            config.push((original_key.to_owned(), value.to_owned()));
        }
    }
    Ok(config)
}

/// Freeze the effective configuration for the network operation. This keeps
/// auth and proxy settings while preventing a concurrently modified machine
/// configuration from adding an executable filter during the checkout.
struct StagedGitConfig {
    local: PathBuf,
    original: Vec<u8>,
    empty_global: PathBuf,
    config: Vec<(String, String)>,
}

impl StagedGitConfig {
    fn new(stage: &Path, config: &[(String, String)]) -> Result<Self, InstallError> {
        let local = stage.join(".git/config");
        if !std::fs::symlink_metadata(&local)
            .is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
        {
            return Err(InstallError::InstallFailed {
                detail: "Git config must be an independent regular file".into(),
            });
        }
        let io_error = |source| InstallError::Io {
            path: local.clone(),
            source,
        };
        let original = std::fs::read(&local).map_err(io_error)?;
        let empty_global = stage.join(".git/.open-grok-update-global");
        std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&empty_global)
            .map_err(|source| InstallError::Io {
                path: empty_global.clone(),
                source,
            })?;
        // The effective configuration may contain global credentials. Keep it
        // in the child environment, never in a crash-orphaned staging tree.
        std::fs::write(&local, b"").map_err(io_error)?;
        Ok(Self {
            local,
            original,
            empty_global,
            config: config.to_vec(),
        })
    }

    fn isolate(&self, command: &mut std::process::Command) {
        command
            .env("GIT_CONFIG_NOSYSTEM", "1")
            .env("GIT_CONFIG_GLOBAL", &self.empty_global);
        command.env("GIT_CONFIG_COUNT", self.config.len().to_string());
        for (i, (key, value)) in self.config.iter().enumerate() {
            command
                .env(format!("GIT_CONFIG_KEY_{i}"), key)
                .env(format!("GIT_CONFIG_VALUE_{i}"), value);
        }
    }

    fn validate_attributes(&self, stage: &Path) -> Result<(), InstallError> {
        use std::io::Write;
        use std::process::Stdio;
        let fail = || InstallError::InstallFailed {
            detail: "unable to inspect Git filter attributes for staged update".into(),
        };
        let mut files = staged_git_command(stage)?;
        self.isolate(&mut files);
        let files = files
            .args(["ls-files", "-z"])
            .output()
            .map_err(|_| fail())?;
        if !files.status.success() {
            return Err(fail());
        }
        let mut attrs = staged_git_command(stage)?;
        self.isolate(&mut attrs);
        let mut attrs = attrs
            .args(["check-attr", "-z", "--stdin", "filter"])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .spawn()
            .map_err(|_| fail())?;
        let mut stdin = attrs.stdin.take().ok_or_else(fail)?;
        let input = files.stdout;
        let writer = std::thread::spawn(move || stdin.write_all(&input));
        let output = attrs.wait_with_output().map_err(|_| fail())?;
        writer.join().map_err(|_| fail())?.map_err(|_| fail())?;
        if !output.status.success() {
            return Err(fail());
        }
        let fields: Vec<_> = output.stdout.split(|b| *b == 0).collect();
        if fields
            .chunks_exact(3)
            .any(|triple| triple[2] != b"unspecified" && triple[2] != b"unset")
        {
            return Err(InstallError::InstallFailed {
                detail: "Git filter attributes are unsupported for staged plugin updates".into(),
            });
        }
        Ok(())
    }

    fn restore(self) -> Result<(), InstallError> {
        std::fs::write(&self.local, self.original).map_err(|source| InstallError::Io {
            path: self.local,
            source,
        })?;
        std::fs::remove_file(&self.empty_global).map_err(|source| InstallError::Io {
            path: self.empty_global,
            source,
        })?;
        Ok(())
    }
}

fn staged_head_commit(stage: &Path) -> Result<Option<String>, InstallError> {
    let output = staged_git_command(stage)?
        .args(["rev-parse", "HEAD"])
        .output()
        .map_err(|source| InstallError::Io {
            path: stage.to_path_buf(),
            source,
        })?;
    if !output.status.success() {
        return Err(InstallError::InstallFailed {
            detail: "could not read plugin HEAD".into(),
        });
    }
    Ok(Some(
        String::from_utf8_lossy(&output.stdout).trim().to_string(),
    ))
}

/// Recursively copy a directory tree.
///
/// Symlinks are **not** followed: directory symlinks are skipped (not recursed),
/// file symlinks are skipped (not materialized as target contents). This keeps
/// refresh/install snapshots from pulling secrets outside the source tree.
/// Shared by install and refresh, so the skip applies to install-time copies too.
pub(super) fn copy_dir_recursive(src: &Path, dst: &Path) -> std::io::Result<()> {
    std::fs::create_dir_all(dst)?;
    for entry in std::fs::read_dir(src)? {
        let entry = entry?;
        let src_path = entry.path();
        let dst_path = dst.join(entry.file_name());
        let meta = std::fs::symlink_metadata(&src_path)?;
        if meta.file_type().is_symlink() {
            tracing::debug!(path = %src_path.display(), "copy_dir_recursive: skipping symlink (not followed)");
            continue;
        }
        if meta.is_dir() {
            copy_dir_recursive(&src_path, &dst_path)?;
        } else if meta.is_file() {
            std::fs::copy(&src_path, &dst_path)?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::process::Command;

    #[test]
    fn registered_local_install_save_failure_cleans_stage_and_allows_retry() {
        let tmp = tempfile::tempdir().unwrap();
        let source_dir = tmp.path().join("source");
        std::fs::create_dir_all(source_dir.join("skills/demo")).unwrap();
        std::fs::write(source_dir.join("plugin.json"), r#"{"name":"demo"}"#).unwrap();
        std::fs::write(source_dir.join("skills/demo/SKILL.md"), "first").unwrap();
        let source = InstallSource::Local {
            path: source_dir.clone(),
            subdir: None,
        };
        let mut registry = InstallRegistry::empty(tmp.path().join("installed"));
        let result = install_and_register_with(
            &source,
            &mut registry,
            false,
            None,
            None,
            |registry, key, repo, stage| {
                registry.publish_staged_with(key, repo, stage, |_| {
                    Err(InstallError::InstallFailed {
                        detail: "injected save failure".into(),
                    })
                })
            },
        );
        assert!(result.is_err());
        let key = InstallRegistry::repo_key(&repo_source_id(&source));
        assert!(!registry.install_dir().join(&key).exists());
        assert!(registry.list().is_empty());
        assert!(std::fs::read_dir(registry.install_dir()).unwrap().all(|e| {
            !e.unwrap()
                .file_name()
                .to_string_lossy()
                .starts_with(".staging-")
        }));

        std::fs::write(source_dir.join("skills/demo/SKILL.md"), "retry").unwrap();
        let installed = install_and_register(&source, &mut registry, false, None, None).unwrap();
        assert_eq!(
            std::fs::read_to_string(installed.repo_path.join("skills/demo/SKILL.md")).unwrap(),
            "retry"
        );
        assert!(
            InstallRegistry::try_load_from(registry.install_dir().to_path_buf())
                .unwrap()
                .get_repo(&key)
                .is_some()
        );
    }

    #[test]
    fn registered_install_preserves_unregistered_existing_path() {
        let tmp = tempfile::tempdir().unwrap();
        let source_dir = tmp.path().join("source");
        std::fs::create_dir_all(source_dir.join("skills/demo")).unwrap();
        std::fs::write(source_dir.join("plugin.json"), r#"{"name":"demo"}"#).unwrap();
        let source = InstallSource::Local {
            path: source_dir,
            subdir: None,
        };
        let mut registry = InstallRegistry::empty(tmp.path().join("installed"));
        let final_path = registry
            .install_dir()
            .join(InstallRegistry::repo_key(&repo_source_id(&source)));
        std::fs::create_dir_all(&final_path).unwrap();
        std::fs::write(final_path.join("unrelated"), "safe").unwrap();
        assert!(install_and_register(&source, &mut registry, false, None, None).is_err());
        assert_eq!(
            std::fs::read_to_string(final_path.join("unrelated")).unwrap(),
            "safe"
        );
    }

    #[test]
    fn registered_convention_plugin_uses_final_directory_name() {
        let tmp = tempfile::tempdir().unwrap();
        let source_dir = tmp.path().join("source");
        std::fs::create_dir_all(source_dir.join("skills/demo")).unwrap();
        let source = InstallSource::Local {
            path: source_dir,
            subdir: None,
        };
        let mut registry = InstallRegistry::empty(tmp.path().join("installed"));
        let installed = install_and_register(&source, &mut registry, false, None, None).unwrap();
        assert_eq!(
            installed.plugins[0].name,
            name_from_dirname(&installed.repo_path).unwrap()
        );
        assert!(
            registry
                .get_repo(&installed.repo_key)
                .unwrap()
                .plugins
                .contains_key(&installed.plugins[0].name)
        );
    }

    #[test]
    fn repo_key_distinct_per_git_subdir_and_bare_unchanged() {
        let url = "https://github.com/acme/agent-skills.git";
        let bare = InstallSource::Git {
            url: url.to_string(),
            git_ref: None,
            git_sha: None,
            subdir: None,
        };
        let acme = InstallSource::Git {
            url: url.to_string(),
            git_ref: None,
            git_sha: None,
            subdir: Some("plugins/acme".to_string()),
        };
        let cloud = InstallSource::Git {
            url: url.to_string(),
            git_ref: None,
            git_sha: None,
            subdir: Some("plugins/acme-cloud".to_string()),
        };

        let key_bare = InstallRegistry::repo_key(&repo_source_id(&bare));
        let key_acme = InstallRegistry::repo_key(&repo_source_id(&acme));
        let key_cloud = InstallRegistry::repo_key(&repo_source_id(&cloud));

        assert_ne!(key_acme, key_cloud);
        assert_ne!(key_bare, key_acme);
        assert_eq!(key_bare, InstallRegistry::repo_key(url));
    }

    #[test]
    fn repo_key_distinct_per_local_subdir_and_bare_unchanged() {
        let path_str = "/Users/me/workspace";
        let bare = InstallSource::Local {
            path: PathBuf::from(path_str),
            subdir: None,
        };
        let acme = InstallSource::Local {
            path: PathBuf::from(path_str),
            subdir: Some("plugins/acme".to_string()),
        };
        let cloud = InstallSource::Local {
            path: PathBuf::from(path_str),
            subdir: Some("plugins/acme-cloud".to_string()),
        };

        let key_bare = InstallRegistry::repo_key(&repo_source_id(&bare));
        let key_acme = InstallRegistry::repo_key(&repo_source_id(&acme));
        let key_cloud = InstallRegistry::repo_key(&repo_source_id(&cloud));

        assert_ne!(key_acme, key_cloud);
        assert_ne!(key_bare, key_acme);
        assert_eq!(key_bare, InstallRegistry::repo_key(path_str));
    }

    #[test]
    fn parse_https_url() {
        let source = parse_install_source("https://github.com/user/repo", Path::new("/tmp"));
        match source {
            InstallSource::Git {
                url,
                git_ref,
                git_sha,
                subdir,
            } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert!(git_ref.is_none());
                assert!(git_sha.is_none());
                assert!(subdir.is_none());
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_https_url_with_ref() {
        let source = parse_install_source("https://github.com/user/repo@v1.0.0", Path::new("/tmp"));
        match source {
            InstallSource::Git { url, git_ref, .. } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert_eq!(git_ref.as_deref(), Some("v1.0.0"));
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_https_url_with_subdir() {
        let source =
            parse_install_source("https://github.com/user/repo#my-plugin", Path::new("/tmp"));
        match source {
            InstallSource::Git { url, subdir, .. } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert_eq!(subdir.as_deref(), Some("my-plugin"));
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_https_url_with_ref_and_subdir() {
        let source = parse_install_source(
            "https://github.com/user/repo@main#deploy",
            Path::new("/tmp"),
        );
        match source {
            InstallSource::Git {
                url,
                git_ref,
                subdir,
                ..
            } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert_eq!(git_ref.as_deref(), Some("main"));
                assert_eq!(subdir.as_deref(), Some("deploy"));
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_ssh_url() {
        let source = parse_install_source("git@github.com:user/my-plugin.git", Path::new("/tmp"));
        match source {
            InstallSource::Git {
                url,
                git_ref,
                subdir,
                ..
            } => {
                assert_eq!(url, "git@github.com:user/my-plugin.git");
                assert!(git_ref.is_none());
                assert!(subdir.is_none());
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_github_shorthand() {
        let source = parse_install_source("user/repo", Path::new("/tmp"));
        match source {
            InstallSource::Git {
                url,
                git_ref,
                subdir,
                ..
            } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert!(git_ref.is_none());
                assert!(subdir.is_none());
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_github_shorthand_with_ref() {
        let source = parse_install_source("user/repo@v1.0.0", Path::new("/tmp"));
        match source {
            InstallSource::Git { url, git_ref, .. } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert_eq!(git_ref.as_deref(), Some("v1.0.0"));
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_github_shorthand_with_subdir() {
        let source = parse_install_source("user/repo#my-plugin", Path::new("/tmp"));
        match source {
            InstallSource::Git { url, subdir, .. } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert_eq!(subdir.as_deref(), Some("my-plugin"));
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_github_shorthand_with_ref_and_subdir() {
        let source = parse_install_source("user/repo@main#deploy", Path::new("/tmp"));
        match source {
            InstallSource::Git {
                url,
                git_ref,
                subdir,
                ..
            } => {
                assert_eq!(url, "https://github.com/user/repo");
                assert_eq!(git_ref.as_deref(), Some("main"));
                assert_eq!(subdir.as_deref(), Some("deploy"));
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn parse_github_shorthand_not_deep_path() {
        // Three segments like "a/b/c" should be treated as a local path, not shorthand.
        let source = parse_install_source("a/b/c", Path::new("/tmp"));
        match source {
            InstallSource::Local { .. } => {}
            _ => panic!("expected Local for deep relative path"),
        }
    }

    #[test]
    fn parse_local_absolute_path() {
        let source = parse_install_source("/home/user/my-plugin", Path::new("/tmp"));
        match source {
            InstallSource::Local { path, subdir } => {
                assert_eq!(path, PathBuf::from("/home/user/my-plugin"));
                assert!(subdir.is_none());
            }
            _ => panic!("expected Local"),
        }
    }

    #[test]
    fn parse_local_relative_path() {
        let source = parse_install_source("./my-plugin", Path::new("/projects"));
        match source {
            InstallSource::Local { path, subdir } => {
                assert_eq!(path, PathBuf::from("/projects/my-plugin"));
                assert!(subdir.is_none());
            }
            _ => panic!("expected Local"),
        }
    }

    #[test]
    fn parse_local_with_subdir() {
        let source = parse_install_source("/home/user/workspace#my-plugin", Path::new("/tmp"));
        match source {
            InstallSource::Local { path, subdir } => {
                assert_eq!(path, PathBuf::from("/home/user/workspace"));
                assert_eq!(subdir.as_deref(), Some("my-plugin"));
            }
            _ => panic!("expected Local"),
        }
    }

    #[test]
    fn discover_single_plugin_at_root() {
        let tmp = tempfile::tempdir().unwrap();
        let plugin_dir = tmp.path();
        std::fs::create_dir_all(plugin_dir.join("skills")).unwrap();

        let plugins = discover_plugins_in_dir(plugin_dir, None).unwrap();
        assert_eq!(plugins.len(), 1);
    }

    #[test]
    fn discover_multiple_plugins_in_subdirs() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path();

        // Create two plugin subdirectories
        std::fs::create_dir_all(root.join("linter/skills")).unwrap();
        std::fs::create_dir_all(root.join("formatter/agents")).unwrap();
        // Non-plugin dir (should be ignored)
        std::fs::create_dir_all(root.join("docs")).unwrap();

        let plugins = discover_plugins_in_dir(root, None).unwrap();
        assert_eq!(plugins.len(), 2);

        let names: Vec<_> = plugins.iter().map(|p| p.name.as_str()).collect();
        assert!(names.contains(&"linter"));
        assert!(names.contains(&"formatter"));
    }

    #[test]
    fn discover_with_subdir_selector() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path();

        std::fs::create_dir_all(root.join("packages/plugin-a/skills")).unwrap();
        std::fs::create_dir_all(root.join("packages/plugin-b/skills")).unwrap();

        // Only discover plugin-a
        let plugins = discover_plugins_in_dir(root, Some("packages/plugin-a")).unwrap();
        assert_eq!(plugins.len(), 1);
        assert_eq!(plugins[0].name, "plugin-a");
    }

    #[test]
    fn discover_plugins_in_dir_rejects_escaping_subdir() {
        let tmp = tempfile::tempdir().unwrap();
        for bad in ["../escape", "/etc"] {
            match discover_plugins_in_dir(tmp.path(), Some(bad)) {
                Err(InstallError::InstallFailed { detail }) => {
                    assert!(detail.contains("escapes"), "got detail: {detail}");
                }
                Err(e) => panic!("expected escapes InstallFailed, got: {e:?}"),
                Ok(_) => panic!("expected escapes error for subdir {bad:?}, got Ok"),
            }
        }
    }

    #[test]
    fn discover_with_manifest() {
        let tmp = tempfile::tempdir().unwrap();
        let plugin_dir = tmp.path();
        std::fs::write(
            plugin_dir.join("plugin.json"),
            r#"{"name": "my-tool", "version": "1.2.0"}"#,
        )
        .unwrap();

        let plugins = discover_plugins_in_dir(plugin_dir, None).unwrap();
        assert_eq!(plugins.len(), 1);
        assert_eq!(plugins[0].name, "my-tool");
        assert_eq!(plugins[0].version.as_deref(), Some("1.2.0"));
    }

    fn git_available() -> bool {
        Command::new("git")
            .arg("--version")
            .stdin(std::process::Stdio::null())
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status()
            .map(|s| s.success())
            .unwrap_or(false)
    }

    fn run_git_test(cwd: &Path, args: &[&str]) {
        let mut cmd = Command::new("git");
        cmd.args(args)
            .current_dir(cwd)
            .env("GIT_AUTHOR_NAME", "Test")
            .env("GIT_AUTHOR_EMAIL", "test@example.com")
            .env("GIT_COMMITTER_NAME", "Test")
            .env("GIT_COMMITTER_EMAIL", "test@example.com")
            .stdin(std::process::Stdio::null());
        let output = cmd.output().unwrap();
        assert!(
            output.status.success(),
            "git {args:?} failed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }

    fn make_local_repo() -> (tempfile::TempDir, String) {
        let tmp = tempfile::tempdir().unwrap();
        let repo = tmp.path();
        run_git_test(repo, &["init", "--initial-branch=main", "--quiet"]);
        std::fs::write(repo.join("plugin.json"), r#"{"name":"pinned-plugin"}"#).unwrap();
        run_git_test(repo, &["add", "plugin.json"]);
        run_git_test(repo, &["commit", "-m", "initial", "--quiet"]);
        run_git_test(repo, &["config", "uploadpack.allowAnySHA1InWant", "true"]);
        let sha = read_head_commit(repo).expect("read HEAD of local repo");
        (tmp, sha)
    }

    #[cfg(unix)]
    fn update_fixture() -> (
        tempfile::TempDir,
        tempfile::TempDir,
        InstallRegistry,
        InstalledRepo,
        String,
    ) {
        let (origin, _) = make_local_repo();
        std::fs::write(origin.path().join("tracked.txt"), "original").unwrap();
        std::fs::write(origin.path().join(".gitignore"), "ignored.txt\n").unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(origin.path(), &["commit", "-m", "fixture", "--quiet"]);
        let store = tempfile::tempdir().unwrap();
        let mut registry = InstallRegistry::empty(store.path().to_path_buf());
        let source = InstallSource::Git {
            url: format!("file://{}", origin.path().display()),
            git_ref: Some("main".into()),
            git_sha: None,
            subdir: None,
        };
        let result = install_and_register(&source, &mut registry, false, None, None).unwrap();
        let repo = registry.get_repo(&result.repo_key).unwrap().clone();
        std::fs::write(
            origin.path().join("plugin.json"),
            r#"{"name":"pinned-plugin","version":"2"}"#,
        )
        .unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(origin.path(), &["commit", "-m", "update", "--quiet"]);
        (origin, store, registry, repo, result.repo_key)
    }

    #[cfg(unix)]
    #[test]
    fn git_update_ignores_unused_filter_and_rejects_used_filter() {
        if !git_available() {
            return;
        }
        let (origin, store, mut registry, repo, key) = update_fixture();
        let marker = store.path().join("filter-executed");
        let filter = format!("touch '{}'", marker.display());
        run_git_test(&repo.path, &["config", "filter.unsafe.smudge", &filter]);
        assert!(matches!(
            update_registered_repo(&key, &repo, &mut registry, false),
            Ok(UpdateStatus::Updated(UpdateResult { changed: true, .. }))
        ));
        assert!(!marker.exists());
        let current = registry.get_repo(&key).unwrap().clone();
        let before = checkout_fingerprint(&current.path).unwrap();
        std::fs::write(
            origin.path().join(".gitattributes"),
            "plugin.json filter=unsafe\n",
        )
        .unwrap();
        std::fs::write(
            origin.path().join("plugin.json"),
            r#"{"name":"pinned-plugin","version":"3"}"#,
        )
        .unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(origin.path(), &["commit", "-m", "add filter", "--quiet"]);
        assert!(update_registered_repo(&key, &current, &mut registry, false).is_err());
        assert!(!marker.exists());
        assert_eq!(checkout_fingerprint(&current.path).unwrap(), before);
    }

    #[test]
    fn staged_git_config_never_writes_inherited_credentials_to_checkout() {
        let stage = tempfile::tempdir().unwrap();
        std::fs::create_dir(stage.path().join(".git")).unwrap();
        std::fs::write(stage.path().join(".git/config"), "[core]\n  bare = false\n").unwrap();
        let config = vec![(
            "http.https://example.com.extraheader".to_owned(),
            "test-secret-value".to_owned(),
        )];
        let staged = StagedGitConfig::new(stage.path(), &config).unwrap();
        // A crash here can leave both files behind.
        assert!(
            !std::fs::read_to_string(&staged.local)
                .unwrap()
                .contains("test-secret-value")
        );
        assert!(
            !std::fs::read_to_string(&staged.empty_global)
                .unwrap()
                .contains("test-secret-value")
        );
        staged.restore().unwrap();
        assert_eq!(
            std::fs::read_to_string(stage.path().join(".git/config")).unwrap(),
            "[core]\n  bare = false\n"
        );
    }

    #[cfg(windows)]
    #[test]
    fn git_checkout_copies_readonly_loose_objects_and_cleans_up() {
        if !git_available() {
            return;
        }
        let (origin, _) = make_local_repo();
        let objects = origin.path().join(".git/objects");
        let object = std::fs::read_dir(&objects)
            .unwrap()
            .flatten()
            .filter(|entry| entry.file_name().to_string_lossy().len() == 2)
            .find_map(|dir| std::fs::read_dir(dir.path()).ok()?.flatten().next())
            .expect("fresh Git repository should contain a loose object")
            .path();
        let mut permissions = std::fs::metadata(&object).unwrap().permissions();
        permissions.set_readonly(true);
        std::fs::set_permissions(&object, permissions).unwrap();
        let container = tempfile::tempdir().unwrap();
        let stage = container.path().join("stage");
        std::fs::create_dir(&stage).unwrap();
        if verify_readonly_cleanup(&stage).is_err() {
            return;
        }
        copy_git_checkout(origin.path(), &stage).unwrap();
        let staged_object = stage.join(object.strip_prefix(origin.path()).unwrap());
        assert!(
            std::fs::metadata(staged_object)
                .unwrap()
                .permissions()
                .readonly()
        );
        remove_repo_path(&stage).unwrap();
        assert!(!stage.exists());
        assert!(std::fs::metadata(object).unwrap().permissions().readonly());
    }

    #[cfg(unix)]
    #[test]
    fn git_update_save_failure_rolls_back_and_retry_preserves_local_state() {
        use std::os::unix::fs::PermissionsExt;
        if !git_available() {
            return;
        }
        let (_origin, _store, mut registry, repo, key) = update_fixture();
        std::fs::write(repo.path.join("tracked.txt"), "staged edit").unwrap();
        run_git_test(&repo.path, &["add", "tracked.txt"]);
        std::fs::write(repo.path.join("ignored.txt"), "ignored contents").unwrap();
        std::fs::write(repo.path.join("untracked"), "untracked contents").unwrap();
        std::fs::set_permissions(
            repo.path.join("untracked"),
            std::fs::Permissions::from_mode(0o751),
        )
        .unwrap();
        std::os::unix::fs::symlink("tracked.txt", repo.path.join("local-link")).unwrap();
        let before = checkout_fingerprint(&repo.path).unwrap();
        let old_record = serde_json::to_value(&repo).unwrap();
        let result = update_registered_repo_with(
            &key,
            &repo,
            &mut registry,
            false,
            || {},
            |_| {
                Err(InstallError::InstallFailed {
                    detail: "injected save failure".into(),
                })
            },
        );
        assert!(result.is_err());
        assert_eq!(checkout_fingerprint(&repo.path).unwrap(), before);
        let disk = InstallRegistry::try_load_from(registry.install_dir().to_path_buf()).unwrap();
        assert_eq!(
            serde_json::to_value(disk.get_repo(&key).unwrap()).unwrap(),
            old_record
        );

        assert!(matches!(
            update_registered_repo(&key, &repo, &mut registry, false),
            Ok(UpdateStatus::Updated(UpdateResult { changed: true, .. }))
        ));
        assert_eq!(
            std::fs::read_to_string(repo.path.join("tracked.txt")).unwrap(),
            "staged edit"
        );
        assert_eq!(
            std::fs::read_to_string(repo.path.join("ignored.txt")).unwrap(),
            "ignored contents"
        );
        assert_eq!(
            std::fs::read_to_string(repo.path.join("untracked")).unwrap(),
            "untracked contents"
        );
        assert_eq!(
            std::fs::metadata(repo.path.join("untracked"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o751
        );
        assert_eq!(
            std::fs::read_link(repo.path.join("local-link")).unwrap(),
            PathBuf::from("tracked.txt")
        );
        let output = Command::new("git")
            .args(["diff", "--cached", "--name-only"])
            .current_dir(&repo.path)
            .output()
            .unwrap();
        assert_eq!(
            String::from_utf8_lossy(&output.stdout).trim(),
            "tracked.txt"
        );
        let disk = InstallRegistry::try_load_from(registry.install_dir().to_path_buf()).unwrap();
        let updated = disk.get_repo(&key).unwrap();
        assert!(
            matches!(&updated.kind, InstallKind::Git { commit, url, git_ref, .. } if Some(commit) == read_head_commit(&repo.path).as_ref() && url.starts_with("file://") && git_ref.as_deref() == Some("main"))
        );
        assert_eq!(
            updated.plugins["pinned-plugin"].version.as_deref(),
            Some("2")
        );
    }

    #[cfg(unix)]
    #[test]
    fn git_update_rejects_concurrent_edit_and_uninstall() {
        if !git_available() {
            return;
        }
        let (_origin, _store, mut registry, repo, key) = update_fixture();
        let edit = repo.path.join("ignored.txt");
        let result = update_registered_repo_with(
            &key,
            &repo,
            &mut registry,
            false,
            || {
                std::fs::write(&edit, "new edit").unwrap();
            },
            InstallRegistry::save_atomic,
        );
        assert!(result.is_err());
        assert_eq!(std::fs::read_to_string(&edit).unwrap(), "new edit");
        assert_eq!(
            serde_json::to_value(registry.get_repo(&key).unwrap()).unwrap(),
            serde_json::to_value(&repo).unwrap()
        );
        let store = registry.install_dir().to_path_buf();
        let result = update_registered_repo_with(
            &key,
            &repo,
            &mut registry,
            false,
            || {
                let mut remover = InstallRegistry::try_load_from(store.clone()).unwrap();
                remover
                    .uninstall_registered_if_matches(&key, &repo, || {})
                    .unwrap();
            },
            InstallRegistry::save_atomic,
        );
        assert!(result.is_err());
        assert!(!repo.path.exists());
        assert!(
            InstallRegistry::try_load_from(store)
                .unwrap()
                .get_repo(&key)
                .is_none()
        );
    }

    #[cfg(unix)]
    #[test]
    fn git_update_rejects_a_concurrent_winner_and_identical_tree_replacement() {
        if !git_available() {
            return;
        }
        let (_origin, _store, mut registry, repo, key) = update_fixture();
        let store = registry.install_dir().to_path_buf();
        let result = update_registered_repo_with(
            &key,
            &repo,
            &mut registry,
            false,
            || {
                let mut winner = InstallRegistry::try_load_from(store.clone()).unwrap();
                assert!(matches!(
                    update_registered_repo(&key, &repo, &mut winner, false),
                    Ok(UpdateStatus::Updated(UpdateResult { changed: true, .. }))
                ));
            },
            InstallRegistry::save_atomic,
        );
        assert!(result.is_err());
        let disk = InstallRegistry::try_load_from(store.clone()).unwrap();
        let current = disk.get_repo(&key).unwrap().clone();
        assert!(
            matches!(&current.kind, InstallKind::Git { commit, .. } if Some(commit) == read_head_commit(&repo.path).as_ref())
        );

        // Same bytes and registry record, but a different directory identity.
        let moved_old = store.join("moved-old");
        let replacement = store.join("replacement");
        let result = update_registered_repo_with(
            &key,
            &current,
            &mut registry,
            false,
            || {
                copy_git_checkout(&current.path, &replacement).unwrap();
                assert_eq!(
                    checkout_fingerprint(&current.path).unwrap(),
                    checkout_fingerprint(&replacement).unwrap()
                );
                std::fs::rename(&current.path, &moved_old).unwrap();
                std::fs::rename(&replacement, &current.path).unwrap();
            },
            InstallRegistry::save_atomic,
        );
        assert!(result.is_err());
        assert!(current.path.is_dir());
        assert_eq!(
            serde_json::to_value(
                InstallRegistry::try_load_from(store)
                    .unwrap()
                    .get_repo(&key)
                    .unwrap()
            )
            .unwrap(),
            serde_json::to_value(&current).unwrap()
        );
    }

    #[cfg(unix)]
    #[test]
    fn git_update_missing_selected_subdir_keeps_original_checkout() {
        if !git_available() {
            return;
        }
        let origin = tempfile::tempdir().unwrap();
        run_git_test(origin.path(), &["init", "--initial-branch=main", "--quiet"]);
        std::fs::create_dir(origin.path().join("selected")).unwrap();
        std::fs::write(
            origin.path().join("selected/plugin.json"),
            r#"{"name":"selected"}"#,
        )
        .unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(origin.path(), &["commit", "-m", "old", "--quiet"]);
        let store = tempfile::tempdir().unwrap();
        let mut registry = InstallRegistry::empty(store.path().to_path_buf());
        let source = InstallSource::Git {
            url: format!("file://{}", origin.path().display()),
            git_ref: Some("main".into()),
            git_sha: None,
            subdir: Some("selected".into()),
        };
        let result = install_and_register(&source, &mut registry, false, None, None).unwrap();
        let repo = registry.get_repo(&result.repo_key).unwrap().clone();
        let before = checkout_fingerprint(&repo.path).unwrap();
        std::fs::remove_dir_all(origin.path().join("selected")).unwrap();
        std::fs::write(origin.path().join("elsewhere"), "new").unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(
            origin.path(),
            &["commit", "-m", "remove selected", "--quiet"],
        );
        assert!(update_registered_repo(&result.repo_key, &repo, &mut registry, false).is_err());
        assert_eq!(checkout_fingerprint(&repo.path).unwrap(), before);
        assert_eq!(
            serde_json::to_value(
                InstallRegistry::try_load_from(store.path().to_path_buf())
                    .unwrap()
                    .get_repo(&result.repo_key)
                    .unwrap()
            )
            .unwrap(),
            serde_json::to_value(&repo).unwrap()
        );
    }

    #[cfg(unix)]
    #[test]
    fn git_update_refuses_readonly_and_external_git_metadata() {
        use std::os::unix::fs::PermissionsExt;
        if !git_available() {
            return;
        }
        let (_origin, _store, mut registry, repo, key) = update_fixture();
        let locked = repo.path.join("locked");
        std::fs::create_dir(&locked).unwrap();
        std::fs::write(locked.join("keep"), "original").unwrap();
        std::fs::set_permissions(&locked, std::fs::Permissions::from_mode(0o555)).unwrap();
        assert!(update_registered_repo(&key, &repo, &mut registry, false).is_err());
        assert_eq!(
            std::fs::read_to_string(locked.join("keep")).unwrap(),
            "original"
        );
        std::fs::set_permissions(&locked, std::fs::Permissions::from_mode(0o755)).unwrap();
        std::fs::write(
            repo.path.join(".git/objects/info/alternates"),
            "/tmp/external\n",
        )
        .unwrap();
        assert!(update_registered_repo(&key, &repo, &mut registry, false).is_err());
        assert!(repo.path.join(".git/objects/info/alternates").exists());
        assert_eq!(
            serde_json::to_value(registry.get_repo(&key).unwrap()).unwrap(),
            serde_json::to_value(&repo).unwrap()
        );
    }

    #[test]
    fn sha_git_args_terminate_options_before_operands() {
        assert_eq!(
            remote_add_args("repo"),
            ["remote", "add", "--", "origin", "repo"]
        );
        assert_eq!(
            fetch_sha_args("0123456789abcdef0123456789abcdef01234567"),
            [
                "fetch",
                "--depth",
                "1",
                "--",
                "origin",
                "0123456789abcdef0123456789abcdef01234567",
            ]
        );
    }

    #[test]
    fn clone_at_correct_sha_succeeds() {
        if !git_available() {
            eprintln!("skipping: `git` binary not available in test sandbox");
            return;
        }
        let (repo, sha) = make_local_repo();
        let dest = tempfile::tempdir().unwrap();
        let url = format!("file://{}", repo.path().display());

        clone_repo_at_sha(&url, &sha, dest.path()).expect("clone at correct sha should succeed");

        assert_eq!(read_head_commit(dest.path()).as_deref(), Some(sha.as_str()));
        assert!(dest.path().join("plugin.json").exists());
    }

    #[test]
    fn clone_at_wrong_sha_returns_mismatch() {
        if !git_available() {
            eprintln!("skipping: `git` binary not available in test sandbox");
            return;
        }
        let (repo, _real_sha) = make_local_repo();
        let bogus_sha = "0000000000000000000000000000000000000000";
        let dest = tempfile::tempdir().unwrap();
        let url = format!("file://{}", repo.path().display());

        let err = clone_repo_at_sha(&url, bogus_sha, dest.path())
            .expect_err("clone at bogus sha should fail");

        match err {
            InstallError::InstallFailed { detail } => {
                assert!(
                    detail.contains("fetch-by-sha"),
                    "expected fetch-by-sha error, got: {detail}"
                );
            }
            other => panic!("expected InstallFailed, got: {other:?}"),
        }
        assert!(!dest.path().join(".git").exists());
    }

    #[test]
    fn clone_at_sha_rejects_malformed_pin_before_target_creation() {
        let root = tempfile::tempdir().unwrap();
        let bad_shas = [
            "deadbee",
            "--upload-pack=cmd",
            "gggggggggggggggggggggggggggggggggggggggg",
        ];
        for (index, bad) in bad_shas.into_iter().enumerate() {
            let target = root.path().join(index.to_string());
            let err = clone_repo_at_sha("file:///unused", bad, &target)
                .expect_err("malformed SHA must be rejected");
            assert!(matches!(err, InstallError::InstallFailed { .. }));
            assert!(
                !target.exists(),
                "validation must precede filesystem mutation"
            );
        }
    }

    #[test]
    fn update_repo_rediscovers_plugin_at_stored_subdir() {
        if !git_available() {
            eprintln!("skipping: `git` binary not available in test sandbox");
            return;
        }
        use std::collections::HashMap;

        let origin = tempfile::tempdir().unwrap();
        run_git_test(origin.path(), &["init", "--initial-branch=main", "--quiet"]);
        let plugin_dir = origin.path().join("plugins").join("acme");
        std::fs::create_dir_all(&plugin_dir).unwrap();
        std::fs::write(
            plugin_dir.join("plugin.json"),
            r#"{"name":"acme","version":"1.0.0"}"#,
        )
        .unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(origin.path(), &["commit", "-m", "init", "--quiet"]);

        let install = tempfile::tempdir().unwrap();
        let repo_path = install.path().join("acme-deadbeef");
        let origin_url = format!("file://{}", origin.path().display());
        run_git_test(
            install.path(),
            &[
                "clone",
                "--quiet",
                origin_url.as_str(),
                repo_path.to_str().unwrap(),
            ],
        );

        let commit = read_head_commit(&repo_path).expect("read HEAD of clone");
        let repo = InstalledRepo {
            kind: InstallKind::Git {
                url: origin_url,
                git_ref: Some("main".to_string()),
                commit,
                subdir: Some("plugins/acme".to_string()),
            },
            installed_at: String::new(),
            updated_at: String::new(),
            path: repo_path,
            plugins: HashMap::from([(
                "acme".to_string(),
                RepoPlugin {
                    subdir: Some("plugins/acme".to_string()),
                    version: Some("1.0.0".to_string()),
                },
            )]),
            marketplace: None,
        };

        let mut registry = InstallRegistry::empty(install.path().to_path_buf());
        registry.insert("acme-deadbeef".into(), repo.clone());
        registry.save().unwrap();
        match update_registered_repo("acme-deadbeef", &repo, &mut registry, false)
            .expect("update should succeed")
        {
            UpdateStatus::Updated(result) => {
                assert_eq!(result.plugins.len(), 1);
                assert_eq!(result.plugins[0].name, "acme");
                assert_eq!(result.plugins[0].subdir.as_deref(), Some("plugins/acme"));
            }
            _ => panic!("expected UpdateStatus::Updated"),
        }
    }

    #[test]
    fn update_repo_errors_when_stored_subdir_missing() {
        if !git_available() {
            eprintln!("skipping: `git` binary not available in test sandbox");
            return;
        }
        use std::collections::HashMap;

        let origin = tempfile::tempdir().unwrap();
        run_git_test(origin.path(), &["init", "--initial-branch=main", "--quiet"]);
        let plugin_dir = origin.path().join("plugins").join("acme");
        std::fs::create_dir_all(&plugin_dir).unwrap();
        std::fs::write(
            plugin_dir.join("plugin.json"),
            r#"{"name":"acme","version":"1.0.0"}"#,
        )
        .unwrap();
        run_git_test(origin.path(), &["add", "-A"]);
        run_git_test(origin.path(), &["commit", "-m", "init", "--quiet"]);

        let install = tempfile::tempdir().unwrap();
        let repo_path = install.path().join("acme-deadbeef");
        let origin_url = format!("file://{}", origin.path().display());
        run_git_test(
            install.path(),
            &[
                "clone",
                "--quiet",
                origin_url.as_str(),
                repo_path.to_str().unwrap(),
            ],
        );

        let commit = read_head_commit(&repo_path).expect("read HEAD of clone");
        let repo = InstalledRepo {
            kind: InstallKind::Git {
                url: origin_url,
                git_ref: Some("main".to_string()),
                commit,
                subdir: Some("plugins/does-not-exist".to_string()),
            },
            installed_at: String::new(),
            updated_at: String::new(),
            path: repo_path,
            plugins: HashMap::from([(
                "acme".to_string(),
                RepoPlugin {
                    subdir: Some("plugins/does-not-exist".to_string()),
                    version: Some("1.0.0".to_string()),
                },
            )]),
            marketplace: None,
        };

        let mut registry = InstallRegistry::empty(install.path().to_path_buf());
        registry.insert("acme-deadbeef".into(), repo.clone());
        registry.save().unwrap();
        match update_registered_repo("acme-deadbeef", &repo, &mut registry, false) {
            Err(InstallError::InstallFailed { .. }) => {}
            Err(e) => panic!("expected InstallFailed, got {e:?}"),
            Ok(_) => panic!("expected InstallFailed when stored subdir is missing, got Ok"),
        }
    }

    #[test]
    fn git_operand_validators_preserve_supported_inputs() {
        for url in [
            "https://example.com/repo.git",
            "ssh://git@example.com/repo.git",
            "git@example.com:repo.git",
            "file:///tmp/repo.git",
            "/tmp/repo.git",
            "./repo.git",
            "../repo.git",
            "ext::helper-specific-address",
        ] {
            assert_eq!(validate_git_url(&format!(" {url} ")).unwrap(), url);
        }
        for git_ref in [
            "main",
            "feature/topic",
            "refs/tags/v1.2.3",
            "release@{yesterday}",
        ] {
            assert_eq!(validate_git_ref(&format!(" {git_ref} ")).unwrap(), git_ref);
        }
        for bad in ["", "  ", "--upload-pack=cmd", "bad\0value"] {
            assert!(validate_git_url(bad).is_err(), "URL {bad:?} must fail");
            assert!(validate_git_ref(bad).is_err(), "ref {bad:?} must fail");
        }
    }

    #[test]
    fn supplied_sha_is_always_full_hex() {
        let sha1 = "a".repeat(40);
        let sha256 = "B".repeat(64);
        assert_eq!(validate_git_sha(&format!(" {sha1} ")).unwrap(), sha1);
        assert_eq!(validate_git_sha(&sha256).unwrap(), sha256);
        assert!(ensure_pinned(false, None, "p", "u").is_ok());
        assert!(ensure_pinned(true, Some(&sha1), "p", "u").is_ok());
        let nonhex = "g".repeat(40);
        for bad in [
            "",
            "deadbeef",
            nonhex.as_str(),
            "--upload-pack=cmd",
            "bad\0sha",
        ] {
            assert!(validate_git_sha(bad).is_err(), "SHA {bad:?} must fail");
        }
        assert!(matches!(
            ensure_pinned(true, None, "p", "u"),
            Err(InstallError::UnpinnedRemoteRefused { .. })
        ));
    }

    #[test]
    fn hoist_pin_slots_moves_full_sha_ref_into_sha_slot() {
        let sha = "a".repeat(40);
        assert_eq!(
            hoist_pin_slots(Some(sha.as_str()), None),
            (None, Some(sha.as_str()))
        );
        assert_eq!(
            hoist_pin_slots(Some("main"), Some(sha.as_str())),
            (Some("main"), Some(sha.as_str()))
        );
        assert_eq!(hoist_pin_slots(Some("main"), None), (Some("main"), None));
        assert_eq!(
            hoist_pin_slots(Some(sha.as_str()), Some("  ")),
            (Some(sha.as_str()), Some("")),
            "a supplied blank SHA remains a SHA field and must fail validation"
        );
    }

    #[test]
    fn normalized_git_kind_stays_pinned_in_durable_metadata() {
        for (git_ref, git_sha, expected_pin) in [
            (Some(" v1.2.3 "), None, "v1.2.3".to_string()),
            (None, Some(format!(" {} ", "a".repeat(40))), "a".repeat(40)),
        ] {
            let source = InstallSource::Git {
                url: " https://example.com/repo.git ".into(),
                git_ref: git_ref.map(str::to_owned),
                git_sha,
                subdir: None,
            };
            let normalized = normalize_install_source(&source).unwrap();
            let (url, git_ref) = match normalized {
                InstallSource::Git {
                    url,
                    git_ref,
                    git_sha,
                    ..
                } => (url, git_sha.or(git_ref)),
                InstallSource::Local { .. } => unreachable!(),
            };
            let repo_key = InstallRegistry::repo_key(&url);
            let result = InstallResult {
                repo_key: repo_key.clone(),
                repo_path: PathBuf::from("/unused"),
                plugins: Vec::new(),
                commit: Some("a".repeat(40)),
                kind: InstallKind::Git {
                    url,
                    git_ref,
                    commit: "a".repeat(40),
                    subdir: None,
                },
            };
            let repo = build_installed_repo(&result, &source);

            assert_eq!(
                repo_key,
                InstallRegistry::repo_key("https://example.com/repo.git")
            );
            match &repo.kind {
                InstallKind::Git { url, git_ref, .. } => {
                    assert_eq!(url, "https://example.com/repo.git");
                    assert_eq!(git_ref.as_deref(), Some(expected_pin.as_str()));
                }
                InstallKind::Local { .. } => panic!("expected Git"),
            }
            let mut registry = InstallRegistry::empty(PathBuf::from("/unused"));
            assert!(matches!(
                update_registered_repo(&repo_key, &repo, &mut registry, true),
                Ok(UpdateStatus::Pinned { ref_name }) if ref_name == expected_pin
            ));
        }
    }

    #[test]
    fn install_from_source_rejects_malformed_operands_before_install_dir_creation() {
        let root = tempfile::tempdir().unwrap();
        let install_dir = root.path().join("installed-plugins");
        let registry = InstallRegistry::empty(install_dir.clone());
        for (url, git_ref, git_sha) in [
            ("--upload-pack=cmd", None, None),
            ("file:///unused", Some("--upload-pack=cmd"), None),
            ("file:///unused", None, Some("deadbeef")),
        ] {
            let source = InstallSource::Git {
                url: url.into(),
                git_ref: git_ref.map(str::to_owned),
                git_sha: git_sha.map(str::to_owned),
                subdir: None,
            };
            assert!(matches!(
                install_from_source(&source, &registry, false),
                Err(InstallError::InstallFailed { .. })
            ));
            assert!(!install_dir.exists());
        }
    }

    #[test]
    fn install_from_source_gates_and_hoists_sha_pins() {
        let install = tempfile::tempdir().unwrap();
        let registry = InstallRegistry::empty(install.path().join("installed-plugins"));

        let unpinned = InstallSource::Git {
            url: "https://example.com/repo.git".into(),
            git_ref: Some("main".into()),
            git_sha: None,
            subdir: None,
        };
        assert!(
            matches!(
                install_from_source(&unpinned, &registry, true),
                Err(InstallError::UnpinnedRemoteRefused { .. })
            ),
            "unpinned git source must be refused before any fetch"
        );

        // Pinned path needs a real git binary (remote CI sandboxes often lack it).
        if !git_available() {
            eprintln!("skipping pin-hoist install: `git` binary not available in test sandbox");
            return;
        }

        // Real pinned install from a local origin. allowAnySHA1InWant matches
        // make_local_repo so fetch-by-sha against file:// succeeds.
        let (origin, sha) = make_local_repo();
        let pinned_via_ref = InstallSource::Git {
            url: format!("file://{}", origin.path().display()),
            git_ref: Some(sha.clone()), // full sha in the REF slot (url@sha syntax)
            git_sha: None,
            subdir: None,
        };
        let result = install_from_source(&pinned_via_ref, &registry, true)
            .expect("a full-sha ref satisfies the pin policy via the hoist");
        assert_eq!(
            result.commit.as_deref(),
            Some(sha.as_str()),
            "the installed checkout is the pinned commit"
        );
    }

    #[test]
    fn update_repo_gates_unpinned_branch_updates() {
        let repo = InstalledRepo {
            kind: InstallKind::Git {
                url: "https://example.com/repo.git".into(),
                git_ref: Some("main".into()),
                commit: "c0ffee".into(),
                subdir: None,
            },
            installed_at: String::new(),
            updated_at: String::new(),
            path: PathBuf::from("/nonexistent"),
            plugins: std::collections::HashMap::new(),
            marketplace: None,
        };
        let mut registry = InstallRegistry::empty(PathBuf::from("/unused"));
        assert!(
            matches!(
                update_registered_repo("acme-deadbeef", &repo, &mut registry, true),
                Err(InstallError::UnpinnedRemoteRefused { .. })
            ),
            "a mutable-ref update must be refused under the pin policy"
        );
    }
}

//! Refresh of copied local plugin installs from their live source.
//!
//! A local install is a full directory copy under `installed-plugins/` (not a
//! live symlink), so agents/skills added to the live source after install do not
//! surface until the snapshot is re-copied. This module re-copies refreshable
//! local installs (under-home or trusted) at session spawn and `/plugins reload`.

use std::collections::{BTreeMap, BTreeSet};
use std::io::Read;
use std::path::{Path, PathBuf};

use super::git_install::{
    StagingDir, copy_dir_recursive, create_staging_dir, discover_plugins_in_dir_as, repo_plugin_map,
};
use super::install_registry::{InstallError, InstallKind, InstallRegistry, InstalledRepo};
use super::trust::TrustStore;

/// Counts from a [`refresh_local_installs`] pass, for logging and tests.
#[derive(Debug, Default, Clone, Copy)]
pub(crate) struct RefreshSummary {
    pub refreshed: usize,
    pub skipped: usize,
    pub errors: usize,
}

/// Load the install registry, [`refresh_local_installs`], and persist it if a
/// snapshot changed.
///
/// Runs only at genuine session spawn (`force=false`, cheap skip-unchanged) and
/// explicit `/plugins reload` (`force=true`, always re-copies — the guaranteed
/// manual remedy). Refresh implies continuous re-consent for under-home / trusted
/// sources (install-time trust re-applies every spawn). Non-fatal on failure.
pub(crate) fn refresh_local_installs_from_disk(trust: &TrustStore, force: bool) -> RefreshSummary {
    let mut registry = InstallRegistry::load();
    refresh_local_installs(&mut registry, trust, force)
}

/// A local install snapshotted out of the registry so the refresh loop can mutate
/// the registry while iterating. `expected` is the recorded plugin set used to
/// guard against scope-changing rediscovery.
struct RefreshTarget {
    key: String,
    source_path: PathBuf,
    subdir: Option<String>,
    dest: PathBuf,
    expected: InstalledRepo,
}

/// Re-copy refreshable local installs from their live `source_path` into the
/// managed snapshot, rediscovering plugins so new components surface.
///
/// A source is refreshable when it is under the user's home (auto-trusted, same
/// rule as config-path plugins) or in the trust store; remote git installs are
/// handled by `update_repo`, not here. Unless `force`, matching snapshots are skipped.
fn refresh_local_installs(
    registry: &mut InstallRegistry,
    trust: &TrustStore,
    force: bool,
) -> RefreshSummary {
    let mut summary = RefreshSummary::default();
    let targets: Vec<RefreshTarget> = registry
        .list()
        .into_iter()
        .filter_map(|(key, repo)| match &repo.kind {
            InstallKind::Local {
                source_path,
                subdir,
            } => Some(RefreshTarget {
                key: key.to_string(),
                source_path: source_path.clone(),
                subdir: subdir.clone(),
                dest: repo.path.clone(),
                expected: repo.clone(),
            }),
            _ => None,
        })
        .collect();

    for RefreshTarget {
        key,
        source_path,
        subdir,
        dest,
        expected,
    } in targets
    {
        let refreshable =
            TrustStore::is_config_path_auto_trusted(&source_path) || trust.is_trusted(&source_path);
        if !source_path.is_dir() || !refreshable {
            summary.skipped += 1;
            continue;
        }
        // Skip if the snapshot already matches the source.
        // `force` (/plugins reload) bypasses the skip.
        if !force && snapshot_matches_source(&source_path, &dest) {
            summary.skipped += 1;
            continue;
        }

        match recopy_local_install(
            registry,
            &key,
            &source_path,
            subdir.as_deref(),
            &dest,
            &expected,
        ) {
            Ok(true) => {
                summary.refreshed += 1;
            }
            Ok(false) => {
                // Kept the snapshot: rediscovered plugin set/scope differs from
                // recorded (e.g. legacy install without a persisted `subdir`).
                tracing::debug!(
                    repo_key = %key,
                    "kept stale local plugin snapshot: rediscovered plugin set/scope differs from recorded"
                );
                summary.skipped += 1;
            }
            Err(e) => {
                tracing::warn!(repo_key = %key, error = %e, "local plugin refresh failed");
                summary.errors += 1;
            }
        }
    }

    summary
}

/// The set of `(relative_path, file_len)` for every non-symlink file under a
/// tree. Symlinks are skipped, matching [`copy_dir_recursive`]. Comparing two of
/// these detects add / remove / rename / size-change with no stored fingerprint
/// and no brittle src-vs-dst mtime compare (a copy does not preserve mtimes).
fn tree_file_set(root: &Path) -> Option<BTreeMap<PathBuf, u64>> {
    fn walk(base: &Path, dir: &Path, out: &mut BTreeMap<PathBuf, u64>) -> std::io::Result<()> {
        for entry in std::fs::read_dir(dir)? {
            let path = entry?.path();
            let meta = std::fs::symlink_metadata(&path)?;
            if meta.file_type().is_symlink() {
                continue;
            }
            if meta.is_file() {
                if let Ok(rel) = path.strip_prefix(base) {
                    out.insert(rel.to_path_buf(), meta.len());
                }
            } else if meta.is_dir() {
                walk(base, &path, out)?;
            }
        }
        Ok(())
    }
    let mut out = BTreeMap::new();
    walk(root, root, &mut out).ok()?;
    Some(out)
}

/// Compare file paths, lengths and contents. Copies do not preserve mtimes, so
/// comparing timestamps cannot determine whether the snapshot is current.
fn snapshot_matches_source(source: &Path, dest: &Path) -> bool {
    match (tree_file_set(source), tree_file_set(dest)) {
        (Some(src), Some(dst)) if src == dst => src
            .keys()
            .all(|path| files_match(&source.join(path), &dest.join(path)).unwrap_or(false)),
        _ => false,
    }
}

fn files_match(source: &Path, dest: &Path) -> std::io::Result<bool> {
    let mut source = std::fs::File::open(source)?;
    let mut dest = std::fs::File::open(dest)?;
    let mut left = [0; 8192];
    let mut right = [0; 8192];
    loop {
        let count = source.read(&mut left)?;
        dest.read_exact(&mut right[..count])?;
        if left[..count] != right[..count] {
            return Ok(false);
        }
        if count == 0 {
            return Ok(dest.read(&mut right)? == 0);
        }
    }
}

/// Stage a re-copy without following symlinks, retaining the recorded plugin
/// scope. The registry transaction publishes the tree and metadata together and
/// recovers an interrupted swap before the next discovery.
fn recopy_local_install(
    registry: &mut InstallRegistry,
    key: &str,
    source_path: &Path,
    subdir: Option<&str>,
    dest: &Path,
    expected: &InstalledRepo,
) -> Result<bool, InstallError> {
    let stage = StagingDir(create_staging_dir(registry)?);
    copy_dir_recursive(source_path, &stage.0).map_err(|source| InstallError::Io {
        path: stage.0.clone(),
        source,
    })?;
    let discovered = discover_plugins_in_dir_as(&stage.0, subdir, dest)?;
    let discovered_ids: BTreeSet<(&str, Option<&str>)> = discovered
        .iter()
        .map(|p| (p.name.as_str(), p.subdir.as_deref()))
        .collect();
    let expected_ids: BTreeSet<(&str, Option<&str>)> = expected
        .plugins
        .iter()
        .map(|(name, rp)| (name.as_str(), rp.subdir.as_deref()))
        .collect();
    if discovered.is_empty() || discovered_ids != expected_ids {
        return Ok(false);
    }
    let mut repo = expected.clone();
    repo.plugins = repo_plugin_map(&discovered);
    repo.updated_at = chrono::Utc::now().to_rfc3339();
    #[cfg(test)]
    if std::env::var_os("XAI_GROK_TEST_FAIL_REFRESH_PROMOTE").is_some() {
        registry.publish_staged_if_matches_checked(
            key,
            expected,
            repo,
            &stage.0,
            |_| Ok(()),
            |_| {
                Err(InstallError::InstallFailed {
                    detail: "test-injected refresh commit failure".into(),
                })
            },
        )?;
        return Ok(true);
    }
    registry.publish_staged_if_matches(key, expected, repo, &stage.0)?;
    Ok(true)
}

#[cfg(test)]
mod tests {
    use super::super::git_install::{InstallResult, InstallSource, install_from_source};
    use super::super::install_registry::{InstalledRepo, RepoPlugin};
    use super::*;
    use serial_test::serial;
    use std::collections::HashMap;

    /// RAII guard: sets an env var, restores the prior value (or unsets) on drop,
    /// so a test never leaves process-global env pointing at a dropped tempdir.
    struct EnvVarGuard {
        key: &'static str,
        prev: Option<std::ffi::OsString>,
    }

    impl EnvVarGuard {
        fn set(key: &'static str, value: impl AsRef<std::ffi::OsStr>) -> Self {
            let prev = std::env::var_os(key);
            unsafe { std::env::set_var(key, value) };
            Self { key, prev }
        }
    }

    impl Drop for EnvVarGuard {
        fn drop(&mut self) {
            match self.prev.take() {
                Some(v) => unsafe { std::env::set_var(self.key, v) },
                None => unsafe { std::env::remove_var(self.key) },
            }
        }
    }

    // Canonical home: under-home auto-trust canonicalizes the candidate but not
    // `$HOME` (macOS `/var` -> `/private/var`). The guard restores `$HOME` on drop.
    fn home_tempdir() -> (tempfile::TempDir, PathBuf, EnvVarGuard) {
        let tmp = tempfile::tempdir().unwrap();
        let home = dunce::canonicalize(tmp.path()).unwrap();
        let guard = EnvVarGuard::set("HOME", &home);
        (tmp, home, guard)
    }

    fn write_plugin_json(dir: &Path, name: &str) {
        std::fs::create_dir_all(dir).unwrap();
        std::fs::write(dir.join("plugin.json"), format!(r#"{{"name":"{name}"}}"#)).unwrap();
    }

    fn write_agent_md(plugin_dir: &Path, name: &str) {
        std::fs::create_dir_all(plugin_dir.join("agents")).unwrap();
        std::fs::write(
            plugin_dir.join("agents").join(format!("{name}.md")),
            format!("---\nname: {name}\ndescription: d\n---\n"),
        )
        .unwrap();
    }

    // Install `source` (optionally scoped to `subdir`) and record it in
    // `registry`, mirroring what the install command persists.
    fn register_local_install(
        registry: &mut InstallRegistry,
        source: &Path,
        subdir: Option<&str>,
    ) -> InstallResult {
        let installed = install_from_source(
            &InstallSource::Local {
                path: source.to_path_buf(),
                subdir: subdir.map(str::to_string),
            },
            registry,
            false,
        )
        .unwrap();
        let now = chrono::Utc::now().to_rfc3339();
        registry.insert(
            installed.repo_key.clone(),
            InstalledRepo {
                kind: InstallKind::Local {
                    source_path: source.to_path_buf(),
                    subdir: subdir.map(str::to_string),
                },
                installed_at: now.clone(),
                updated_at: now,
                path: installed.repo_path.clone(),
                plugins: repo_plugin_map(&installed.plugins),
                marketplace: None,
            },
        );
        registry.save().unwrap();
        installed
    }

    #[test]
    fn refresh_rejects_original_target_after_an_earlier_refresh_observes_replacement() {
        let tmp = tempfile::tempdir().unwrap();
        let source_a = tmp.path().join("source-a");
        let source_b = tmp.path().join("source-b");
        write_plugin_json(&source_a, "a");
        write_plugin_json(&source_b, "b");
        std::fs::write(source_b.join("content"), "old local").unwrap();
        let mut refresh = InstallRegistry::empty(tmp.path().join("installed"));
        let a = register_local_install(&mut refresh, &source_a, None);
        let b = register_local_install(&mut refresh, &source_b, None);
        let selected_a = refresh.get_repo(&a.repo_key).unwrap().clone();
        let selected_b = refresh.get_repo(&b.repo_key).unwrap().clone();

        let mut replacement =
            InstallRegistry::try_load_from(refresh.install_dir().to_path_buf()).unwrap();
        let stage = StagingDir(create_staging_dir(&replacement).unwrap());
        write_plugin_json(&stage.0, "b");
        std::fs::write(stage.0.join("content"), "new git").unwrap();
        let mut git_repo = selected_b.clone();
        git_repo.kind = InstallKind::Git {
            url: "file:///new-origin".into(),
            git_ref: Some("main".into()),
            commit: "new-commit".into(),
            subdir: None,
        };
        git_repo.updated_at = "new-owner".into();
        replacement
            .publish_staged_if_matches(&b.repo_key, &selected_b, git_repo, &stage.0)
            .unwrap();

        assert!(
            recopy_local_install(
                &mut refresh,
                &a.repo_key,
                &source_a,
                None,
                &a.repo_path,
                &selected_a
            )
            .unwrap()
        );
        assert!(matches!(
            &refresh.get_repo(&b.repo_key).unwrap().kind,
            InstallKind::Git { .. }
        ));
        assert!(
            recopy_local_install(
                &mut refresh,
                &b.repo_key,
                &source_b,
                None,
                &b.repo_path,
                &selected_b
            )
            .is_err()
        );
        assert_eq!(
            std::fs::read_to_string(b.repo_path.join("content")).unwrap(),
            "new git"
        );
        let disk = InstallRegistry::try_load_from(refresh.install_dir().to_path_buf()).unwrap();
        assert!(
            matches!(&disk.get_repo(&b.repo_key).unwrap().kind, InstallKind::Git { commit, .. } if commit == "new-commit")
        );
    }

    #[test]
    #[serial(home_env)]
    fn refresh_local_install_picks_up_new_agent() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let source = home.join(".claude").join("demo-plugin");
        write_plugin_json(&source, "demo-plugin");
        write_agent_md(&source, "old");

        let mut registry = InstallRegistry::empty(home.join(".opengrok").join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);

        write_agent_md(&source, "new");
        assert!(!installed.repo_path.join("agents/new.md").exists());

        let trust = TrustStore::load_from(home.join(".opengrok").join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.refreshed, 1, "{summary:?}");
        assert!(installed.repo_path.join("agents/new.md").exists());
    }

    #[test]
    #[serial(home_env)]
    fn refresh_skips_unchanged_source() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let source = home.join(".claude").join("demo-plugin");
        write_plugin_json(&source, "demo-plugin");
        write_agent_md(&source, "old");

        let mut registry = InstallRegistry::empty(home.join(".opengrok").join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);

        // No edit to the source: snapshot matches, so refresh skips the re-copy.
        let snapshot = installed.repo_path.join("agents/old.md");
        let before = std::fs::metadata(&snapshot).unwrap().modified().unwrap();
        let trust = TrustStore::load_from(home.join(".opengrok").join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.refreshed, 0, "{summary:?}");
        assert_eq!(summary.skipped, 1, "{summary:?}");
        let after = std::fs::metadata(&snapshot).unwrap().modified().unwrap();
        assert_eq!(before, after, "unchanged snapshot must not be re-copied");
    }

    #[test]
    #[serial(home_env)]
    fn refresh_picks_up_same_length_skill_and_hook_edits() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let source = home.join("plugin");
        write_plugin_json(&source, "demo-plugin");
        std::fs::create_dir_all(source.join("skills/demo")).unwrap();
        std::fs::create_dir_all(source.join("hooks")).unwrap();
        std::fs::write(source.join("skills/demo/SKILL.md"), "Use A").unwrap();
        std::fs::write(source.join("hooks/check.sh"), "old").unwrap();

        let mut registry = InstallRegistry::empty(home.join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);
        std::fs::write(source.join("skills/demo/SKILL.md"), "Use B").unwrap();
        std::fs::write(source.join("hooks/check.sh"), "new").unwrap();

        let trust = TrustStore::load_from(home.join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.refreshed, 1, "{summary:?}");
        assert_eq!(
            std::fs::read_to_string(installed.repo_path.join("skills/demo/SKILL.md")).unwrap(),
            "Use B"
        );
        assert_eq!(
            std::fs::read_to_string(installed.repo_path.join("hooks/check.sh")).unwrap(),
            "new"
        );
    }

    #[test]
    #[serial(home_env)]
    fn refresh_picks_up_content_preserving_rename() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let source = home.join(".claude").join("demo-plugin");
        write_plugin_json(&source, "demo-plugin");
        write_agent_md(&source, "old");

        let mut registry = InstallRegistry::empty(home.join(".opengrok").join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);

        // Rename keeps file count, total size, and the file's (old) mtime — the
        // old aggregate fingerprint skipped this; the structural file-set catches it.
        std::fs::rename(
            source.join("agents/old.md"),
            source.join("agents/renamed.md"),
        )
        .unwrap();

        let trust = TrustStore::load_from(home.join(".opengrok").join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(
            summary.refreshed, 1,
            "rename must trigger refresh: {summary:?}"
        );
        assert!(installed.repo_path.join("agents/renamed.md").exists());
        assert!(!installed.repo_path.join("agents/old.md").exists());
    }

    #[test]
    #[serial(home_env)]
    fn refresh_promote_failure_rolls_back_to_prior_snapshot() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let source = home.join(".claude").join("demo-plugin");
        write_plugin_json(&source, "demo-plugin");
        write_agent_md(&source, "old");

        let mut registry = InstallRegistry::empty(home.join(".opengrok").join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);

        // Change the source so a refresh attempts a re-copy, then force the
        // promote rename to fail and assert the prior snapshot is restored.
        write_agent_md(&source, "new");
        let trust = TrustStore::load_from(home.join(".opengrok").join("trusted-plugins"));
        let summary = {
            let _fail = EnvVarGuard::set("XAI_GROK_TEST_FAIL_REFRESH_PROMOTE", "1");
            refresh_local_installs(&mut registry, &trust, false)
        };

        assert_eq!(summary.errors, 1, "{summary:?}");
        assert_eq!(summary.refreshed, 0, "{summary:?}");
        // dest is never left missing and still holds the prior snapshot.
        assert!(installed.repo_path.join("agents/old.md").exists());
        assert!(!installed.repo_path.join("agents/new.md").exists());
    }

    #[test]
    #[serial(home_env)]
    fn refresh_skips_untrusted_source_outside_home() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let outside = tempfile::tempdir().unwrap();
        let source = outside.path().join("untrusted-plugin");
        write_plugin_json(&source, "untrusted-plugin");

        let mut registry = InstallRegistry::empty(home.join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);

        std::fs::write(source.join("extra.txt"), "x").unwrap();
        let trust = TrustStore::load_from(home.join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.skipped, 1, "{summary:?}");
        assert_eq!(summary.refreshed, 0, "{summary:?}");
        assert!(!installed.repo_path.join("extra.txt").exists());
    }

    #[test]
    #[serial(home_env)]
    fn refresh_trusted_source_outside_home() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let outside = tempfile::tempdir().unwrap();
        let source = outside.path().join("trusted-plugin");
        write_plugin_json(&source, "trusted-plugin");
        write_agent_md(&source, "old");

        let mut trust = TrustStore::load_from(home.join("trusted-plugins"));
        trust.grant_trust(&source).unwrap();

        let mut registry = InstallRegistry::empty(home.join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);

        write_agent_md(&source, "new");
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.refreshed, 1, "{summary:?}");
        assert!(installed.repo_path.join("agents/new.md").exists());
    }

    #[test]
    #[serial(home_env)]
    fn refresh_preserves_install_subdir_scope() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let workspace = home.join("workspace");
        write_plugin_json(&workspace.join("plugins/a"), "plugin-a");
        write_plugin_json(&workspace.join("plugins/b"), "plugin-b");

        let mut registry = InstallRegistry::empty(home.join(".opengrok/installed-plugins"));
        let installed = register_local_install(&mut registry, &workspace, Some("plugins/a"));

        write_agent_md(&workspace.join("plugins/a"), "x");

        let trust = TrustStore::load_from(home.join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.refreshed, 1, "{summary:?}");
        assert!(installed.repo_path.join("plugins/a/agents/x.md").exists());

        let repo = registry.get_repo(&installed.repo_key).unwrap();
        match &repo.kind {
            InstallKind::Local { subdir, .. } => assert_eq!(subdir.as_deref(), Some("plugins/a")),
            _ => panic!("expected Local"),
        }
        assert!(repo.plugins.contains_key("plugin-a"));
        assert!(!repo.plugins.contains_key("plugin-b"));
    }

    #[test]
    #[serial(home_env)]
    fn refresh_does_not_follow_directory_symlinks() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        let secret = home.join("secret-dir");
        std::fs::create_dir_all(&secret).unwrap();
        std::fs::write(secret.join("secret.txt"), "leak").unwrap();

        let source = home.join("plugin");
        write_plugin_json(&source, "plugin");
        #[cfg(unix)]
        std::os::unix::fs::symlink(&secret, source.join("link-out")).unwrap();

        let mut registry = InstallRegistry::empty(home.join("installed-plugins"));
        let installed = register_local_install(&mut registry, &source, None);
        assert!(!installed.repo_path.join("link-out/secret.txt").exists());

        std::fs::write(source.join("extra.txt"), "x").unwrap();
        let trust = TrustStore::load_from(home.join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, false);
        assert_eq!(summary.refreshed, 1, "{summary:?}");
        assert!(!installed.repo_path.join("link-out/secret.txt").exists());
        assert!(installed.repo_path.join("extra.txt").exists());
    }

    #[test]
    #[serial(home_env)]
    fn refresh_keeps_stale_when_legacy_subdir_scope_lost() {
        let (_home_tmp, home, _home_guard) = home_tempdir();
        // Legacy multi-package source: the real plugin is at plugins/foo;
        // other-dir is unrelated root-level content that root-scope discovery
        // would pick up.
        let workspace = home.join("workspace");
        write_plugin_json(&workspace.join("plugins/foo"), "foo");
        write_agent_md(&workspace.join("other-dir"), "noise");

        // Snapshot the full source (mirrors the install-time copy).
        let install_dir = home.join(".opengrok").join("installed-plugins");
        std::fs::create_dir_all(&install_dir).unwrap();
        let dest = install_dir.join("foo-legacy");
        copy_dir_recursive(&workspace, &dest).unwrap();

        // Legacy entry: install-level `subdir` was never persisted (None), but the
        // per-plugin RepoPlugin recorded the correct scope.
        let mut registry = InstallRegistry::empty(install_dir);
        let now = chrono::Utc::now().to_rfc3339();
        registry.insert(
            "foo-legacy".to_string(),
            InstalledRepo {
                kind: InstallKind::Local {
                    source_path: workspace.clone(),
                    subdir: None,
                },
                installed_at: now.clone(),
                updated_at: now,
                path: dest.clone(),
                plugins: HashMap::from([(
                    "foo".to_string(),
                    RepoPlugin {
                        subdir: Some("plugins/foo".to_string()),
                        version: None,
                    },
                )]),
                marketplace: None,
            },
        );

        // Edit the source under plugins/foo so a content refresh would trigger.
        write_agent_md(&workspace.join("plugins/foo"), "added");

        // force=true so the unchanged-skip can't mask the scope-identity guard.
        let trust = TrustStore::load_from(home.join(".opengrok").join("trusted-plugins"));
        let summary = refresh_local_installs(&mut registry, &trust, true);

        // Root-scope rediscovery would change the plugin set/scope, so keep stale:
        // no refresh, and repo.plugins / repo.kind must be untouched (no corruption).
        assert_eq!(
            summary.refreshed, 0,
            "scope change must keep stale: {summary:?}"
        );
        let repo = registry.get_repo("foo-legacy").unwrap();
        assert_eq!(repo.plugins.len(), 1);
        assert_eq!(
            repo.plugins.get("foo").and_then(|p| p.subdir.as_deref()),
            Some("plugins/foo")
        );
        match &repo.kind {
            InstallKind::Local {
                source_path,
                subdir,
            } => {
                assert_eq!(source_path, &workspace);
                assert!(subdir.is_none());
            }
            _ => panic!("expected Local"),
        }
    }
}

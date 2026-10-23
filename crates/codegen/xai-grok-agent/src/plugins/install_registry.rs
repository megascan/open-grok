//! Install registry for managing plugins installed from git repos or local directories.
//!
//! Tracks which repos have been cloned/symlinked into the managed install directory,
//! along with the plugins discovered within each repo.
//!
//! The registry is persisted as `registry.json` in the install directory.

use std::collections::HashMap;
use std::io::Write;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

/// Default install directory name under `~/.opengrok/`.
const DEFAULT_INSTALL_DIR_NAME: &str = "installed-plugins";

/// Registry of installed repos and their plugins.
#[derive(Debug, Serialize, Deserialize)]
pub struct InstallRegistry {
    /// Schema version for forward compatibility.
    pub version: u32,
    /// Installed repos, keyed by repo key (`<basename>-<hash8>`).
    pub repos: HashMap<String, InstalledRepo>,
    /// Absolute path to the install directory.
    #[serde(skip)]
    install_dir: PathBuf,
    /// Snapshot used to merge this handle's edits without dropping another writer's entries.
    #[serde(skip)]
    baseline: std::sync::Mutex<HashMap<String, InstalledRepo>>,
}

impl Clone for InstallRegistry {
    fn clone(&self) -> Self {
        Self {
            version: self.version,
            repos: self.repos.clone(),
            install_dir: self.install_dir.clone(),
            baseline: std::sync::Mutex::new(
                self.baseline
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .clone(),
            ),
        }
    }
}

/// How a repo was installed.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum InstallKind {
    /// Cloned from a remote git repo.
    Git {
        url: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        git_ref: Option<String>,
        commit: String,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        subdir: Option<String>,
    },
    /// Copied from a local directory (full tree snapshot under installed-plugins).
    Local {
        source_path: PathBuf,
        /// Optional plugin subdirectory selector used at install time (e.g.
        /// multi-package `path#plugins/foo`). Preserved so refresh rediscovers
        /// the same scope.
        #[serde(default, skip_serializing_if = "Option::is_none")]
        subdir: Option<String>,
    },
}

/// A single installed repo, which may contain one or more plugins.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct InstalledRepo {
    pub kind: InstallKind,
    pub installed_at: String,
    pub updated_at: String,
    /// Absolute path to the repo directory (or symlink) in the install dir.
    pub path: PathBuf,
    /// Plugins discovered within this repo.
    pub plugins: HashMap<String, RepoPlugin>,
    /// Marketplace provenance (None for non-marketplace installs).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub marketplace: Option<MarketplaceProvenance>,
}

/// Marketplace provenance — tracks which marketplace a plugin was installed from.
/// Lives here (not in xai-grok-plugin-marketplace) to keep dependency direction sane.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MarketplaceProvenance {
    /// Canonical source identity (git URL or local path).
    pub source_url_or_path: String,
    /// User-facing source name (display only, not used for matching).
    pub source_display_name: String,
    /// Plugin subdirectory within marketplace (e.g., "plugins/xai-code-review").
    pub plugin_subdir: String,
}

/// A plugin discovered within an installed repo.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RepoPlugin {
    /// Subdirectory within the repo (None if plugin is at repo root).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub subdir: Option<String>,
    /// Plugin version from manifest (if available).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
}

// The lock is held across a directory swap and the registry commit. The journal
// is written before either rename, so a later reader can recover after a crash.
const TRANSACTION_FILE: &str = ".plugin-transaction.json";
const TRANSACTION_MARKER: &str = ".open-grok-transaction";
const BACKUP_MARKER: &str = ".open-grok-transaction-original";

#[derive(Serialize, Deserialize)]
pub(super) struct DirectoryIdentity {
    #[cfg(unix)]
    device: u64,
    #[cfg(unix)]
    inode: u64,
    // Older Windows journals serialized an empty identity. They must remain
    // readable, but cannot authorize recovery based on an unknown identity.
    #[cfg(windows)]
    #[serde(default)]
    volume_serial: Option<u64>,
    #[cfg(windows)]
    #[serde(default)]
    file_id: Option<[u8; 16]>,
}

#[derive(Serialize, Deserialize)]
struct InstallTransaction {
    repo_key: String,
    old_repo: Option<InstalledRepo>,
    new_repo: Option<InstalledRepo>,
    stage_name: String,
    backup_name: String,
    token: String,
    had_old: bool,
    old_identity: Option<DirectoryIdentity>,
}

fn registry_lock(install_dir: &Path) -> Result<std::fs::File, InstallError> {
    std::fs::create_dir_all(install_dir).map_err(|source| InstallError::Io {
        path: install_dir.to_path_buf(),
        source,
    })?;
    let path = install_dir.join(".registry.lock");
    let file = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(&path)
        .map_err(|source| InstallError::Io {
            path: path.clone(),
            source,
        })?;
    file.lock()
        .map_err(|source| InstallError::Io { path, source })?;
    Ok(file)
}

fn valid_component(name: &str) -> bool {
    let mut parts = Path::new(name).components();
    matches!(parts.next(), Some(std::path::Component::Normal(_))) && parts.next().is_none()
}

fn path_exists(path: &Path) -> bool {
    std::fs::symlink_metadata(path).is_ok()
}

fn owned_tree(path: &Path, token: &str) -> bool {
    std::fs::symlink_metadata(path).is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
        && std::fs::symlink_metadata(path.join(TRANSACTION_MARKER))
            .is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
        && std::fs::read_to_string(path.join(TRANSACTION_MARKER)).is_ok_and(|s| s == token)
}

pub(super) fn directory_identity(path: &Path) -> Option<DirectoryIdentity> {
    let metadata = std::fs::symlink_metadata(path).ok()?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return None;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        Some(DirectoryIdentity {
            device: metadata.dev(),
            inode: metadata.ino(),
        })
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        use std::os::windows::io::AsRawHandle;

        #[repr(C)]
        struct FileIdInfo {
            volume_serial_number: u64,
            file_id: [u8; 16],
        }
        #[repr(C)]
        struct FileAttributeTagInfo {
            file_attributes: u32,
            reparse_tag: u32,
        }
        unsafe extern "system" {
            fn GetFileInformationByHandleEx(
                handle: *mut core::ffi::c_void,
                class: i32,
                info: *mut core::ffi::c_void,
                size: u32,
            ) -> i32;
        }
        let file = std::fs::OpenOptions::new()
            .access_mode(0x80)
            .custom_flags(0x0200_0000 | 0x0020_0000)
            .open(path)
            .ok()?;
        let mut tag = FileAttributeTagInfo {
            file_attributes: 0,
            reparse_tag: 0,
        };
        let mut id = FileIdInfo {
            volume_serial_number: 0,
            file_id: [0; 16],
        };
        // SAFETY: the handle remains open and both buffers have the layouts
        // required by the corresponding Win32 information classes.
        let valid = unsafe {
            GetFileInformationByHandleEx(
                file.as_raw_handle(),
                9,
                (&raw mut tag).cast(),
                std::mem::size_of::<FileAttributeTagInfo>() as u32,
            ) != 0
                && GetFileInformationByHandleEx(
                    file.as_raw_handle(),
                    18,
                    (&raw mut id).cast(),
                    std::mem::size_of::<FileIdInfo>() as u32,
                ) != 0
        };
        if !valid
            || tag.file_attributes & 0x400 != 0
            || tag.file_attributes & 0x10 == 0
            || !windows_file_id_is_usable(id.file_id)
        {
            return None;
        }
        Some(DirectoryIdentity {
            volume_serial: Some(id.volume_serial_number),
            file_id: Some(id.file_id),
        })
    }
    #[cfg(not(any(unix, windows)))]
    {
        Some(DirectoryIdentity {})
    }
}

// Windows reports all-zero IDs on filesystems that do not support FILE_ID_INFO.
#[cfg(any(windows, test))]
fn windows_file_id_is_usable(file_id: [u8; 16]) -> bool {
    file_id != [0; 16]
}

pub(super) fn same_directory(path: &Path, expected: &DirectoryIdentity) -> bool {
    let Some(actual) = directory_identity(path) else {
        return false;
    };
    #[cfg(unix)]
    {
        actual.device == expected.device && actual.inode == expected.inode
    }
    #[cfg(windows)]
    {
        expected.volume_serial.is_some()
            && expected.file_id.is_some()
            && actual.volume_serial == expected.volume_serial
            && actual.file_id == expected.file_id
    }
    #[cfg(not(any(unix, windows)))]
    {
        let _ = (actual, expected);
        true
    }
}

fn backup_marker(tx: &InstallTransaction) -> String {
    format!("{BACKUP_MARKER}-{}", &tx.token[".staging-".len()..])
}

fn owned_backup(path: &Path, tx: &InstallTransaction) -> bool {
    tx.old_identity
        .as_ref()
        .is_some_and(|id| same_directory(path, id))
        && std::fs::symlink_metadata(path.join(backup_marker(tx)))
            .is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
        && std::fs::read_to_string(path.join(backup_marker(tx))).is_ok_and(|s| s == tx.token)
}

fn remove_tree(path: &Path) -> Result<(), InstallError> {
    std::fs::remove_dir_all(path).map_err(|source| InstallError::Io {
        path: path.to_path_buf(),
        source,
    })
}

fn rename_tree(from: &Path, to: &Path) -> Result<(), InstallError> {
    std::fs::rename(from, to).map_err(|source| InstallError::Io {
        path: from.to_path_buf(),
        source,
    })
}

fn same_repo(left: Option<&InstalledRepo>, right: &InstalledRepo) -> bool {
    left.is_some_and(|repo| {
        serde_json::to_value(repo)
            .ok()
            .zip(serde_json::to_value(right).ok())
            .is_some_and(|(left, right)| left == right)
    })
}

fn same_optional_repo(left: Option<&InstalledRepo>, right: Option<&InstalledRepo>) -> bool {
    match right {
        Some(right) => same_repo(left, right),
        None => left.is_none(),
    }
}

/// Recover only paths explicitly owned by the journal. An unexpected final
/// tree is retained rather than overwriting a newer or external installation.
fn recover_transaction(registry: &InstallRegistry) -> Result<(), InstallError> {
    let dir = registry.install_dir();
    let journal_path = dir.join(TRANSACTION_FILE);
    let metadata = match std::fs::symlink_metadata(&journal_path) {
        Ok(metadata) => metadata,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(source) => {
            return Err(InstallError::Io {
                path: journal_path,
                source,
            });
        }
    };
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return Err(InstallError::InstallFailed {
            detail: "invalid plugin transaction journal".into(),
        });
    }
    let content = std::fs::read(&journal_path).map_err(|source| InstallError::Io {
        path: journal_path.clone(),
        source,
    })?;
    let tx: InstallTransaction =
        serde_json::from_slice(&content).map_err(|e| InstallError::Json {
            detail: e.to_string(),
        })?;
    if !valid_component(&tx.repo_key)
        || !valid_component(&tx.stage_name)
        || !valid_component(&tx.backup_name)
        || !tx.stage_name.starts_with(".staging-")
        || tx.backup_name != format!(".backup-{}", &tx.stage_name[".staging-".len()..])
        || tx.token != tx.stage_name
        || tx
            .new_repo
            .as_ref()
            .is_some_and(|repo| repo.path != dir.join(&tx.repo_key))
        || (tx.had_old && tx.old_repo.is_none())
        || tx.had_old != tx.old_identity.is_some()
        || tx
            .old_repo
            .as_ref()
            .is_some_and(|repo| repo.path != dir.join(&tx.repo_key))
    {
        return Err(InstallError::InstallFailed {
            detail: "invalid plugin transaction paths".into(),
        });
    }
    let final_path = dir.join(&tx.repo_key);
    let stage = dir.join(&tx.stage_name);
    let backup = dir.join(&tx.backup_name);
    let committed = same_optional_repo(registry.get_repo(&tx.repo_key), tx.new_repo.as_ref());
    let old_matches = match (&tx.old_repo, registry.get_repo(&tx.repo_key)) {
        (None, None) => true,
        (Some(old), current) => same_repo(current, old),
        _ => false,
    };
    if !committed && !old_matches {
        return Err(InstallError::InstallFailed {
            detail:
                "plugin registry changed since the interrupted transaction; preserving its files"
                    .into(),
        });
    }
    if committed {
        if tx.new_repo.is_some() && !path_exists(&final_path) {
            return Err(InstallError::InstallFailed {
                detail: "committed plugin transaction is missing its install".into(),
            });
        }
        if tx.new_repo.is_none() && path_exists(&final_path) && !owned_tree(&final_path, &tx.token)
        {
            return Err(InstallError::InstallFailed {
                detail: "refusing to remove an unowned plugin during uninstall recovery".into(),
            });
        }
        if path_exists(&backup) {
            if tx.new_repo.is_some() && !owned_tree(&final_path, &tx.token) {
                return Err(InstallError::InstallFailed {
                    detail: "refusing to discard backup for an unowned plugin".into(),
                });
            }
            if !owned_backup(&backup, &tx) {
                return Err(InstallError::InstallFailed {
                    detail: "refusing to discard an unowned plugin backup".into(),
                });
            }
            let metadata =
                std::fs::symlink_metadata(&backup).map_err(|source| InstallError::Io {
                    path: backup.clone(),
                    source,
                })?;
            if !metadata.is_dir() || metadata.file_type().is_symlink() {
                return Err(InstallError::InstallFailed {
                    detail: "invalid plugin transaction backup".into(),
                });
            }
            remove_tree(&backup)?;
        }
        if owned_tree(&final_path, &tx.token) {
            if tx.new_repo.is_none() {
                remove_tree(&final_path)?;
            } else {
                let _ = std::fs::remove_file(final_path.join(TRANSACTION_MARKER));
            }
        }
    } else if path_exists(&backup) {
        if !owned_backup(&backup, &tx) {
            return Err(InstallError::InstallFailed {
                detail: "refusing to restore an unowned plugin backup".into(),
            });
        }
        let metadata = std::fs::symlink_metadata(&backup).map_err(|source| InstallError::Io {
            path: backup.clone(),
            source,
        })?;
        if !tx.had_old || !metadata.is_dir() || metadata.file_type().is_symlink() {
            return Err(InstallError::InstallFailed {
                detail: "invalid plugin transaction backup".into(),
            });
        }
        if path_exists(&final_path) {
            if !owned_tree(&final_path, &tx.token) {
                return Err(InstallError::InstallFailed {
                    detail: "refusing to replace an unowned plugin during recovery".into(),
                });
            }
            remove_tree(&final_path)?;
        }
        rename_tree(&backup, &final_path)?;
    } else if !tx.had_old && path_exists(&final_path) && owned_tree(&final_path, &tx.token) {
        remove_tree(&final_path)?;
    } else if tx.had_old && !path_exists(&final_path) {
        return Err(InstallError::InstallFailed {
            detail: "plugin transaction lost its original install".into(),
        });
    }
    if !committed && tx.had_old {
        if !tx
            .old_identity
            .as_ref()
            .is_some_and(|id| same_directory(&final_path, id))
        {
            return Err(InstallError::InstallFailed {
                detail: "original plugin directory changed during recovery".into(),
            });
        }
        // The unique marker name was reserved before journaling. A crash may
        // have left only a prefix of its contents before the original rename.
        let marker_path = final_path.join(backup_marker(&tx));
        if path_exists(&marker_path) {
            let valid = std::fs::symlink_metadata(&marker_path)
                .is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
                && std::fs::read(&marker_path)
                    .is_ok_and(|bytes| tx.token.as_bytes().starts_with(&bytes));
            if !valid {
                return Err(InstallError::InstallFailed {
                    detail: "refusing to remove an unowned plugin marker".into(),
                });
            }
            std::fs::remove_file(&marker_path).map_err(|source| InstallError::Io {
                path: marker_path,
                source,
            })?;
        }
    }
    if path_exists(&stage) {
        if !owned_tree(&stage, &tx.token) {
            return Err(InstallError::InstallFailed {
                detail: "refusing to remove unowned plugin staging directory".into(),
            });
        }
        remove_tree(&stage)?;
    }
    std::fs::remove_file(&journal_path).map_err(|source| InstallError::Io {
        path: journal_path,
        source,
    })?;
    Ok(())
}

fn paths_match_plugin_root(
    installed_plugin_root: &Path,
    plugin_root: &Path,
    plugin_canonical_root: &Path,
) -> bool {
    installed_plugin_root == plugin_root
        || installed_plugin_root == plugin_canonical_root
        || dunce::canonicalize(installed_plugin_root)
            .ok()
            .is_some_and(|canonical| canonical == plugin_root || canonical == plugin_canonical_root)
}

impl InstallRegistry {
    /// Load the registry from the resolved install directory.
    ///
    /// If the registry file doesn't exist, returns an empty registry.
    pub fn load() -> Self {
        Self::load_from(Self::resolve_install_dir())
    }

    /// Load the registry from an explicit install directory.
    ///
    /// Missing file → empty registry. Read/parse errors → empty registry after a warning.
    pub fn load_from(install_dir: PathBuf) -> Self {
        match Self::try_load_from(install_dir.clone()) {
            Ok(reg) => reg,
            Err(e) => {
                tracing::warn!(
                    path = %install_dir.join("registry.json").display(),
                    error = %e,
                    "failed to load install registry; starting fresh"
                );
                Self::empty(install_dir)
            }
        }
    }

    /// Fallible load: missing `registry.json` is empty; read/parse errors are `Err`.
    pub fn try_load_from(install_dir: PathBuf) -> Result<Self, InstallError> {
        if !install_dir.exists() {
            return Ok(Self::empty(install_dir));
        }
        let _lock = registry_lock(&install_dir)?;
        let registry = Self::read_unlocked(install_dir.clone())?;
        recover_transaction(&registry)?;
        Self::read_unlocked(install_dir)
    }

    fn read_unlocked(install_dir: PathBuf) -> Result<Self, InstallError> {
        let registry_path = install_dir.join("registry.json");
        match std::fs::read_to_string(&registry_path) {
            Ok(content) => {
                let mut reg: InstallRegistry =
                    serde_json::from_str(&content).map_err(|e| InstallError::Json {
                        detail: e.to_string(),
                    })?;
                reg.install_dir = install_dir;
                *reg.baseline.lock().unwrap_or_else(|e| e.into_inner()) = reg.repos.clone();
                Ok(reg)
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Self::empty(install_dir)),
            Err(e) => Err(InstallError::Io {
                path: registry_path,
                source: e,
            }),
        }
    }

    /// Create an empty registry for the given install directory.
    pub fn empty(install_dir: PathBuf) -> Self {
        Self {
            version: 1,
            repos: HashMap::new(),
            install_dir,
            baseline: std::sync::Mutex::new(HashMap::new()),
        }
    }

    /// Save the registry to disk.
    pub fn save(&self) -> Result<(), InstallError> {
        let _lock = registry_lock(&self.install_dir)?;
        let mut current = Self::read_unlocked(self.install_dir.clone())?;
        recover_transaction(&current)?;
        current = Self::read_unlocked(self.install_dir.clone())?;
        let mut baseline = self.baseline.lock().unwrap_or_else(|e| e.into_inner());
        let keys: std::collections::HashSet<_> = baseline.keys().chain(self.repos.keys()).collect();
        for key in keys {
            let old = baseline.get(key);
            let desired = self.repos.get(key);
            if same_optional_repo(old, desired) {
                continue;
            }
            let disk = current.repos.get(key);
            if !same_optional_repo(disk, old) && !same_optional_repo(disk, desired) {
                return Err(InstallError::InstallFailed {
                    detail: format!("plugin registry entry '{key}' changed concurrently"),
                });
            }
            match desired {
                Some(repo) => {
                    current.repos.insert(key.clone(), repo.clone());
                }
                None => {
                    current.repos.remove(key);
                }
            }
        }
        current.save_atomic()?;
        *baseline = self.repos.clone();
        Ok(())
    }

    pub(super) fn save_atomic(&self) -> Result<(), InstallError> {
        std::fs::create_dir_all(&self.install_dir).map_err(|e| InstallError::Io {
            path: self.install_dir.clone(),
            source: e,
        })?;

        let registry_path = self.install_dir.join("registry.json");
        let content = serde_json::to_string_pretty(self).map_err(|e| InstallError::Json {
            detail: e.to_string(),
        })?;
        if std::env::var_os("XAI_GROK_TEST_FAIL_REGISTRY_SAVE_AFTER_SERIALIZE").is_some() {
            return Err(InstallError::InstallFailed {
                detail: "test-injected registry save failure".into(),
            });
        }
        let temp_path = self.install_dir.join(format!(
            ".registry.json.tmp-{}-{}",
            std::process::id(),
            chrono::Utc::now().timestamp_nanos_opt().unwrap_or_default()
        ));
        std::fs::write(&temp_path, content).map_err(|e| InstallError::Io {
            path: temp_path.clone(),
            source: e,
        })?;

        if let Err(e) = std::fs::rename(&temp_path, &registry_path) {
            let _ = std::fs::remove_file(&temp_path);
            return Err(InstallError::Io {
                path: registry_path,
                source: e,
            });
        }

        Ok(())
    }

    /// Atomically publish an owned staging tree and its registry record. The
    /// stage must be a fresh, direct child of the install directory.
    pub fn publish_staged(
        &mut self,
        repo_key: &str,
        repo: InstalledRepo,
        stage: &Path,
    ) -> Result<(), InstallError> {
        self.publish_staged_with(repo_key, repo, stage, Self::save_atomic)
    }

    /// Publish only if the originally selected record is still current.
    pub fn publish_staged_if_matches(
        &mut self,
        repo_key: &str,
        expected: &InstalledRepo,
        repo: InstalledRepo,
        stage: &Path,
    ) -> Result<(), InstallError> {
        self.publish_staged_if_matches_checked(
            repo_key,
            expected,
            repo,
            stage,
            |_| Ok(()),
            Self::save_atomic,
        )
    }

    /// Validate an unchanged result against the selected record under the same
    /// lock used by publication and removal.
    pub(super) fn validate_selected(
        &self,
        repo_key: &str,
        expected: &InstalledRepo,
        validate: impl FnOnce(&Path) -> Result<(), InstallError>,
    ) -> Result<(), InstallError> {
        let _lock = registry_lock(&self.install_dir)?;
        let registry = Self::read_unlocked(self.install_dir.clone())?;
        recover_transaction(&registry)?;
        let registry = Self::read_unlocked(self.install_dir.clone())?;
        if !same_repo(registry.get_repo(repo_key), expected) {
            return Err(InstallError::InstallFailed {
                detail: "plugin registry changed during update".into(),
            });
        }
        validate(&expected.path)
    }

    pub(super) fn publish_staged_if_matches_checked(
        &mut self,
        repo_key: &str,
        expected: &InstalledRepo,
        repo: InstalledRepo,
        stage: &Path,
        validate: impl FnOnce(&Path) -> Result<(), InstallError>,
        save: impl FnOnce(&Self) -> Result<(), InstallError>,
    ) -> Result<(), InstallError> {
        self.transact_staged_checked(
            repo_key,
            Some(expected),
            Some(repo),
            stage,
            validate,
            save,
            || {},
        )
    }

    /// Remove an unchanged registry entry and its tree under the install lock.
    pub fn uninstall_registered(&mut self, repo_key: &str) -> Result<(), InstallError> {
        self.uninstall_registered_with(repo_key, || {})
    }

    /// Run data cleanup under the same lock after successful removal, so a new
    /// install cannot appear between removal and cleanup.
    pub fn uninstall_registered_with(
        &mut self,
        repo_key: &str,
        after_commit: impl FnOnce(),
    ) -> Result<(), InstallError> {
        let expected =
            self.get_repo(repo_key)
                .cloned()
                .ok_or_else(|| InstallError::PluginNotFound {
                    name: repo_key.to_string(),
                })?;
        self.uninstall_registered_if_matches(repo_key, &expected, after_commit)
    }

    /// Remove only the originally selected entry, even if this handle has since
    /// been refreshed by another successful transaction.
    pub fn uninstall_registered_if_matches(
        &mut self,
        repo_key: &str,
        expected: &InstalledRepo,
        after_commit: impl FnOnce(),
    ) -> Result<(), InstallError> {
        let stage = super::git_install::StagingDir(super::git_install::create_staging_dir(self)?);
        self.transact_staged_with(
            repo_key,
            Some(expected),
            None,
            &stage.0,
            Self::save_atomic,
            after_commit,
        )
    }

    pub(super) fn publish_staged_with(
        &mut self,
        repo_key: &str,
        repo: InstalledRepo,
        stage: &Path,
        save: impl FnOnce(&Self) -> Result<(), InstallError>,
    ) -> Result<(), InstallError> {
        let expected = self.get_repo(repo_key).cloned();
        self.transact_staged_with(repo_key, expected.as_ref(), Some(repo), stage, save, || {})
    }

    fn transact_staged_with(
        &mut self,
        repo_key: &str,
        prior: Option<&InstalledRepo>,
        repo: Option<InstalledRepo>,
        stage: &Path,
        save: impl FnOnce(&Self) -> Result<(), InstallError>,
        after_commit: impl FnOnce(),
    ) -> Result<(), InstallError> {
        self.transact_staged_checked(repo_key, prior, repo, stage, |_| Ok(()), save, after_commit)
    }

    fn transact_staged_checked(
        &mut self,
        repo_key: &str,
        prior: Option<&InstalledRepo>,
        mut repo: Option<InstalledRepo>,
        stage: &Path,
        validate: impl FnOnce(&Path) -> Result<(), InstallError>,
        save: impl FnOnce(&Self) -> Result<(), InstallError>,
        after_commit: impl FnOnce(),
    ) -> Result<(), InstallError> {
        let dir = self.install_dir.clone();
        let _lock = registry_lock(&dir)?;
        let mut current = Self::read_unlocked(dir.clone())?;
        recover_transaction(&current)?;
        current = Self::read_unlocked(dir.clone())?;
        let disk_prior = current.get_repo(repo_key);
        if prior.is_some() != disk_prior.is_some()
            || prior.is_some_and(|expected| !same_repo(disk_prior, expected))
        {
            return Err(InstallError::InstallFailed {
                detail: "plugin registry changed during install".into(),
            });
        }
        if repo.is_none() && disk_prior.is_none() {
            return Err(InstallError::PluginNotFound {
                name: repo_key.to_string(),
            });
        }
        let stage_name = stage.file_name().and_then(|n| n.to_str()).unwrap_or("");
        if !valid_component(repo_key)
            || repo
                .as_ref()
                .is_some_and(|repo| repo.path != dir.join(repo_key))
            || stage.parent() != Some(dir.as_path())
            || !valid_component(stage_name)
            || !stage_name.starts_with(".staging-")
            || !std::fs::symlink_metadata(stage)
                .is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
        {
            return Err(InstallError::InstallFailed {
                detail: "invalid plugin staging or install path".into(),
            });
        }
        let final_path = dir.join(repo_key);
        let had_old = disk_prior.is_some() && path_exists(&final_path);
        if disk_prior.is_some() && !had_old && repo.is_some() {
            return Err(InstallError::InstallFailed {
                detail: format!(
                    "installed plugin directory not found: {}",
                    final_path.display()
                ),
            });
        }
        // Recovery must distinguish the pre-commit registry from the new record,
        // even if a refresh preserves every other piece of metadata.
        if let Some(repo) = repo.as_mut().filter(|repo| same_repo(disk_prior, repo)) {
            let timestamp = chrono::DateTime::parse_from_rfc3339(&repo.updated_at)
                .map(|t| t.with_timezone(&chrono::Utc))
                .unwrap_or_else(|_| chrono::Utc::now());
            repo.updated_at = (timestamp + chrono::Duration::nanoseconds(1))
                .to_rfc3339_opts(chrono::SecondsFormat::Nanos, true);
        }
        if had_old {
            if !std::fs::symlink_metadata(&final_path)
                .is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
            {
                return Err(InstallError::InstallFailed {
                    detail: format!(
                        "installed plugin directory not found or unsafe: {}",
                        final_path.display()
                    ),
                });
            }
        } else if path_exists(&final_path) {
            return Err(InstallError::InstallFailed {
                detail: format!(
                    "unregistered plugin path already exists: {}",
                    final_path.display()
                ),
            });
        }
        validate(&final_path)?;
        let suffix = stage_name.strip_prefix(".staging-").unwrap();
        let backup_name = format!(".backup-{suffix}");
        let backup = dir.join(&backup_name);
        if path_exists(&backup) {
            return Err(InstallError::InstallFailed {
                detail: "plugin transaction backup already exists".into(),
            });
        }
        let token = stage_name.to_string();
        let marker_path = stage.join(TRANSACTION_MARKER);
        let mut marker = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&marker_path)
            .map_err(|source| InstallError::Io {
                path: marker_path.clone(),
                source,
            })?;
        marker
            .write_all(token.as_bytes())
            .map_err(|source| InstallError::Io {
                path: marker_path,
                source,
            })?;
        drop(marker);
        let old_identity = if had_old {
            Some(
                directory_identity(&final_path).ok_or_else(|| InstallError::InstallFailed {
                    detail: "original plugin directory changed before update".into(),
                })?,
            )
        } else {
            None
        };
        let tx = InstallTransaction {
            repo_key: repo_key.to_string(),
            old_repo: disk_prior.cloned(),
            new_repo: repo.clone(),
            stage_name: stage_name.to_string(),
            backup_name,
            token,
            had_old,
            old_identity,
        };
        if had_old && path_exists(&final_path.join(backup_marker(&tx))) {
            return Err(InstallError::InstallFailed {
                detail: "plugin transaction marker already exists".into(),
            });
        }
        let bytes = serde_json::to_vec(&tx).map_err(|e| InstallError::Json {
            detail: e.to_string(),
        })?;
        let journal_tmp = dir.join(format!(".journal-{suffix}"));
        let journal = dir.join(TRANSACTION_FILE);
        let mut file = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&journal_tmp)
            .map_err(|source| InstallError::Io {
                path: journal_tmp.clone(),
                source,
            })?;
        let journal_result = file.write_all(&bytes).and_then(|_| file.sync_all());
        if let Err(source) = journal_result {
            let _ = std::fs::remove_file(&journal_tmp);
            return Err(InstallError::Io {
                path: journal_tmp,
                source,
            });
        }
        drop(file);
        if let Err(e) = rename_tree(&journal_tmp, &journal) {
            let _ = std::fs::remove_file(&journal_tmp);
            return Err(e);
        }
        let result = (|| {
            if had_old {
                let marker_path = final_path.join(backup_marker(&tx));
                let mut marker = std::fs::OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .open(&marker_path)
                    .map_err(|source| InstallError::Io {
                        path: marker_path.clone(),
                        source,
                    })?;
                if let Err(source) = marker.write_all(tx.token.as_bytes()) {
                    drop(marker);
                    let _ = std::fs::remove_file(&marker_path);
                    return Err(InstallError::Io {
                        path: marker_path,
                        source,
                    });
                }
                drop(marker);
                if !tx
                    .old_identity
                    .as_ref()
                    .is_some_and(|id| same_directory(&final_path, id))
                {
                    return Err(InstallError::InstallFailed {
                        detail: "original plugin directory changed before replacement".into(),
                    });
                }
                rename_tree(&final_path, &backup)?;
            }
            rename_tree(stage, &final_path)?;
            match repo {
                Some(repo) => current.insert(repo_key.to_string(), repo),
                None => {
                    current.remove(repo_key);
                }
            }
            save(&current)?;
            Ok(())
        })();
        let disk = Self::read_unlocked(dir)?;
        if let Err(recovery) = recover_transaction(&disk) {
            return Err(InstallError::InstallFailed {
                detail: format!(
                    "plugin transaction recovery failed: {recovery}; original result: {result:?}"
                ),
            });
        }
        *self = disk;
        if result.is_ok() {
            after_commit();
        }
        result
    }

    /// Get a repo by its repo key.
    pub fn get_repo(&self, repo_key: &str) -> Option<&InstalledRepo> {
        self.repos.get(repo_key)
    }

    /// Get a mutable reference to a repo by its repo key.
    pub fn get_repo_mut(&mut self, repo_key: &str) -> Option<&mut InstalledRepo> {
        self.repos.get_mut(repo_key)
    }

    /// Find which repo a plugin belongs to.
    ///
    /// Returns `(repo_key, repo, plugin)` if found.
    pub fn find_plugin(&self, plugin_name: &str) -> Option<(&str, &InstalledRepo, &RepoPlugin)> {
        for (repo_key, repo) in &self.repos {
            if let Some(plugin) = repo.plugins.get(plugin_name) {
                return Some((repo_key, repo, plugin));
            }
        }
        None
    }

    pub fn find_repo_key_by_plugin_root(
        &self,
        plugin_root: &Path,
        plugin_canonical_root: &Path,
    ) -> Option<&str> {
        self.list().into_iter().find_map(|(repo_key, repo)| {
            repo.plugins.values().find_map(|plugin| {
                let installed_plugin_root = match plugin.subdir.as_deref() {
                    Some(subdir) => repo.path.join(subdir),
                    None => repo.path.clone(),
                };
                paths_match_plugin_root(&installed_plugin_root, plugin_root, plugin_canonical_root)
                    .then_some(repo_key)
            })
        })
    }

    /// Insert a repo into the registry.
    pub fn insert(&mut self, repo_key: String, repo: InstalledRepo) {
        self.repos.insert(repo_key, repo);
    }

    /// Remove a repo from the registry.
    pub fn remove(&mut self, repo_key: &str) -> Option<InstalledRepo> {
        self.repos.remove(repo_key)
    }

    /// List all installed repos.
    pub fn list(&self) -> Vec<(&str, &InstalledRepo)> {
        let mut entries: Vec<_> = self.repos.iter().map(|(k, v)| (k.as_str(), v)).collect();
        entries.sort_by_key(|(k, _)| *k);
        entries
    }

    /// Get the install directory path.
    pub fn install_dir(&self) -> &Path {
        &self.install_dir
    }

    /// Resolve the install directory from config or default.
    ///
    /// Resolution order:
    /// 1. `[plugins].install_dir` from effective config (requirements > config > managed)
    /// 2. Default: `~/.opengrok/installed-plugins/`
    pub fn resolve_install_dir() -> PathBuf {
        if let Some(dir) = Self::read_install_dir_from_config() {
            return dir;
        }

        xai_grok_config::grok_home().join(DEFAULT_INSTALL_DIR_NAME)
    }

    /// Read `[plugins].install_dir` from the effective config
    /// (managed_config.toml merged under config.toml — user wins).
    fn read_install_dir_from_config() -> Option<PathBuf> {
        let root = xai_grok_config::load_effective_config_disk_only().ok()?;
        let value = root.get("plugins")?.get("install_dir")?.as_str()?;
        let expanded = if let Some(stripped) = value.strip_prefix("~/") {
            xai_dirs::home_dir()?.join(stripped)
        } else {
            PathBuf::from(value)
        };
        Some(expanded)
    }

    /// Generate a unique repo key from a source identifier.
    ///
    /// Format: `<basename>-<hash8>` where hash8 = first 8 hex chars of
    /// SHA-256(normalized source).
    ///
    /// Examples:
    /// - `https://github.com/org-a/tools` → `tools-a1b2c3d4`
    /// - `/Users/me/projects/my-plugin` → `my-plugin-e5f6g7h8`
    pub fn repo_key(source: &str) -> String {
        let basename = source
            .trim_end_matches('/')
            .trim_end_matches(".git")
            .rsplit('/')
            .next()
            .unwrap_or("plugin");

        // Sanitize basename to kebab-case
        let sanitized: String = basename
            .to_ascii_lowercase()
            .chars()
            .map(|c| {
                if c.is_ascii_alphanumeric() || c == '-' {
                    c
                } else {
                    '-'
                }
            })
            .collect();
        let trimmed = sanitized.trim_matches('-');

        // Hash the full source for uniqueness
        use std::hash::{Hash, Hasher};
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        source.hash(&mut hasher);
        let hash = hasher.finish();
        let hash8 = format!("{:08x}", hash & 0xFFFFFFFF);

        format!("{trimmed}-{hash8}")
    }
}

// ── Errors ────────────────────────────────────────────────────────────

#[derive(Debug, thiserror::Error)]
pub enum InstallError {
    #[error("I/O error on {path}: {source}")]
    Io {
        path: PathBuf,
        source: std::io::Error,
    },

    #[error("JSON error: {detail}")]
    Json { detail: String },

    #[error("plugin '{name}' not found in install registry")]
    PluginNotFound { name: String },

    #[error("repo '{key}' already installed")]
    AlreadyInstalled { key: String },

    #[error("SHA verification failed: expected {expected}, got {actual}")]
    ShaMismatch { expected: String, actual: String },

    #[error(
        "refusing unpinned remote plugin code for '{plugin}' from {url}: \
         marketplace.require_sha / OPENGROK_MARKETPLACE_REQUIRE_SHA is enabled and \
         no full commit sha (40/64 hex) is pinned"
    )]
    UnpinnedRemoteRefused { plugin: String, url: String },

    #[error("install failed: {detail}")]
    InstallFailed { detail: String },
}

// ── Tests ─────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn windows_zero_file_id_is_not_an_identity() {
        assert!(!windows_file_id_is_usable([0; 16]));
        let mut id = [0; 16];
        id[15] = 1;
        assert!(windows_file_id_is_usable(id));
    }

    #[cfg(any(unix, windows))]
    #[test]
    fn directory_identity_distinguishes_identical_replacement() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("current");
        let old = tmp.path().join("old");
        std::fs::create_dir(&path).unwrap();
        std::fs::write(path.join("same"), "same").unwrap();
        let identity = directory_identity(&path).unwrap();
        std::fs::rename(&path, old).unwrap();
        std::fs::create_dir(&path).unwrap();
        std::fs::write(path.join("same"), "same").unwrap();
        assert!(!same_directory(&path, &identity));
    }

    fn transaction_repo(dir: &Path, version: &str) -> InstalledRepo {
        InstalledRepo {
            kind: InstallKind::Local {
                source_path: dir.join("source"),
                subdir: None,
            },
            installed_at: "2026-01-01T00:00:00Z".into(),
            updated_at: version.into(),
            path: dir.join("demo"),
            plugins: HashMap::from([(
                "demo".into(),
                RepoPlugin {
                    subdir: None,
                    version: Some(version.into()),
                },
            )]),
            marketplace: None,
        }
    }

    fn interrupted_transaction(dir: &Path, promoted: bool, committed: bool) -> (PathBuf, PathBuf) {
        std::fs::create_dir_all(dir.join("demo")).unwrap();
        std::fs::write(dir.join("demo/content"), "old").unwrap();
        let old = transaction_repo(dir, "old");
        let new = transaction_repo(dir, "new");
        let mut registry = InstallRegistry::empty(dir.to_path_buf());
        registry.insert("demo".into(), old.clone());
        registry.save().unwrap();
        let stage = dir.join(".staging-fixture");
        let backup = dir.join(".backup-fixture");
        let old_identity = directory_identity(&dir.join("demo")).unwrap();
        std::fs::create_dir(&stage).unwrap();
        std::fs::write(stage.join("content"), "new").unwrap();
        std::fs::write(stage.join(TRANSACTION_MARKER), ".staging-fixture").unwrap();
        let tx = InstallTransaction {
            repo_key: "demo".into(),
            old_repo: Some(old),
            new_repo: Some(new.clone()),
            stage_name: ".staging-fixture".into(),
            backup_name: ".backup-fixture".into(),
            token: ".staging-fixture".into(),
            had_old: true,
            old_identity: Some(old_identity),
        };
        std::fs::write(dir.join(TRANSACTION_FILE), serde_json::to_vec(&tx).unwrap()).unwrap();
        std::fs::write(
            dir.join("demo").join(backup_marker(&tx)),
            ".staging-fixture",
        )
        .unwrap();
        std::fs::rename(dir.join("demo"), &backup).unwrap();
        if promoted {
            std::fs::rename(&stage, dir.join("demo")).unwrap();
        }
        if committed {
            registry.insert("demo".into(), new);
            registry.save_atomic().unwrap();
        }
        (stage, backup)
    }

    #[test]
    fn plugin_transaction_recovers_interrupted_swap_boundaries() {
        for (promoted, committed) in [(false, false), (true, false), (true, true)] {
            let tmp = tempfile::tempdir().unwrap();
            let dir = tmp.path();
            let (stage, backup) = interrupted_transaction(dir, promoted, committed);
            let registry = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
            let expected = if committed { "new" } else { "old" };
            assert_eq!(
                std::fs::read_to_string(dir.join("demo/content")).unwrap(),
                expected
            );
            assert_eq!(registry.get_repo("demo").unwrap().updated_at, expected);
            assert!(!stage.exists());
            assert!(!backup.exists());
            assert!(!dir.join(TRANSACTION_FILE).exists());
            assert!(!dir.join("demo").join(TRANSACTION_MARKER).exists());
        }
    }

    #[test]
    fn plugin_transaction_recovers_partial_original_marker_and_retries() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let (_stage, backup) = interrupted_transaction(dir, false, false);
        std::fs::rename(&backup, dir.join("demo")).unwrap();
        let tx: InstallTransaction =
            serde_json::from_slice(&std::fs::read(dir.join(TRANSACTION_FILE)).unwrap()).unwrap();
        let marker = dir.join("demo").join(backup_marker(&tx));
        std::fs::write(&marker, b".sta").unwrap();
        let mut registry = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        assert!(!marker.exists());
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "old"
        );
        let retry = super::super::git_install::create_staging_dir(&registry).unwrap();
        registry
            .publish_staged("demo", transaction_repo(dir, "retry"), &retry)
            .unwrap();
        assert_eq!(registry.get_repo("demo").unwrap().updated_at, "retry");
    }

    #[test]
    fn plugin_transaction_uninstall_recovers_swap_and_commit() {
        for (promoted, committed, removed_placeholder) in [
            (false, false, false),
            (true, false, false),
            (true, true, false),
            (true, true, true),
        ] {
            let tmp = tempfile::tempdir().unwrap();
            let dir = tmp.path();
            let (stage, backup) = interrupted_transaction(dir, promoted, false);
            let mut tx: InstallTransaction =
                serde_json::from_slice(&std::fs::read(dir.join(TRANSACTION_FILE)).unwrap())
                    .unwrap();
            tx.new_repo = None;
            std::fs::write(dir.join(TRANSACTION_FILE), serde_json::to_vec(&tx).unwrap()).unwrap();
            if committed {
                let mut disk = InstallRegistry::read_unlocked(dir.to_path_buf()).unwrap();
                disk.remove("demo");
                disk.save_atomic().unwrap();
            }
            if removed_placeholder {
                std::fs::remove_dir_all(dir.join("demo")).unwrap();
            }
            let disk = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
            assert_eq!(disk.get_repo("demo").is_none(), committed);
            assert_eq!(dir.join("demo").exists(), !committed);
            if !committed {
                assert_eq!(
                    std::fs::read_to_string(dir.join("demo/content")).unwrap(),
                    "old"
                );
            }
            assert!(!backup.exists());
            assert!(!stage.exists());
            assert!(!dir.join(TRANSACTION_FILE).exists());
        }
    }

    #[test]
    fn plugin_transaction_uninstall_rejects_stale_registry() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        std::fs::create_dir(dir.join("demo")).unwrap();
        std::fs::write(dir.join("demo/content"), "old").unwrap();
        let mut initial = InstallRegistry::empty(dir.to_path_buf());
        initial.insert("demo".into(), transaction_repo(dir, "old"));
        initial.save().unwrap();
        let mut stale = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let mut updater = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let stage = super::super::git_install::create_staging_dir(&updater).unwrap();
        std::fs::write(stage.join("content"), "new").unwrap();
        updater
            .publish_staged("demo", transaction_repo(dir, "new"), &stage)
            .unwrap();
        assert!(stale.uninstall_registered("demo").is_err());
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "new"
        );
        assert_eq!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("demo")
                .unwrap()
                .updated_at,
            "new"
        );
    }

    #[test]
    fn plugin_transaction_bulk_uninstall_keeps_original_expectations() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let mut initial = InstallRegistry::empty(dir.to_path_buf());
        for key in ["first", "second"] {
            std::fs::create_dir(dir.join(key)).unwrap();
            let mut repo = transaction_repo(dir, "old");
            repo.path = dir.join(key);
            initial.insert(key.into(), repo);
        }
        initial.save().unwrap();
        let mut remover = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let first = remover.get_repo("first").unwrap().clone();
        let second = remover.get_repo("second").unwrap().clone();
        let mut updater = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let stage = super::super::git_install::create_staging_dir(&updater).unwrap();
        std::fs::write(stage.join("content"), "new").unwrap();
        let mut new_second = second.clone();
        new_second.updated_at = "new".into();
        updater
            .publish_staged("second", new_second, &stage)
            .unwrap();
        remover
            .uninstall_registered_if_matches("first", &first, || {})
            .unwrap();
        assert!(
            remover
                .uninstall_registered_if_matches("second", &second, || {})
                .is_err()
        );
        assert_eq!(
            std::fs::read_to_string(dir.join("second/content")).unwrap(),
            "new"
        );
        assert_eq!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("second")
                .unwrap()
                .updated_at,
            "new"
        );
    }

    #[test]
    fn plugin_transaction_uninstall_save_failure_restores_original() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        std::fs::create_dir(dir.join("demo")).unwrap();
        std::fs::write(dir.join("demo/content"), "old").unwrap();
        let mut registry = InstallRegistry::empty(dir.to_path_buf());
        registry.insert("demo".into(), transaction_repo(dir, "old"));
        registry.save().unwrap();
        let stage = super::super::git_install::create_staging_dir(&registry).unwrap();
        let expected = registry.get_repo("demo").cloned();
        let result = registry.transact_staged_with(
            "demo",
            expected.as_ref(),
            None,
            &stage,
            |_| {
                Err(InstallError::InstallFailed {
                    detail: "injected failure".into(),
                })
            },
            || {},
        );
        assert!(result.is_err());
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "old"
        );
        assert!(registry.get_repo("demo").is_some());
        registry.uninstall_registered("demo").unwrap();
        assert!(!dir.join("demo").exists());
        assert!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("demo")
                .is_none()
        );
    }

    #[test]
    fn plugin_transaction_uninstall_can_remove_missing_install_record() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let mut registry = InstallRegistry::empty(dir.to_path_buf());
        registry.insert("demo".into(), transaction_repo(dir, "old"));
        registry.save().unwrap();
        registry.uninstall_registered("demo").unwrap();
        assert!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("demo")
                .is_none()
        );
        assert!(!dir.join("demo").exists());
    }

    #[test]
    fn plugin_transaction_preserves_unowned_final_and_backup() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let (stage, backup) = interrupted_transaction(dir, false, false);
        std::fs::create_dir(dir.join("demo")).unwrap();
        std::fs::write(dir.join("demo/content"), "unrelated").unwrap();
        assert!(InstallRegistry::try_load_from(dir.to_path_buf()).is_err());
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "unrelated"
        );
        assert_eq!(
            std::fs::read_to_string(backup.join("content")).unwrap(),
            "old"
        );
        assert!(stage.exists());
        assert!(dir.join(TRANSACTION_FILE).exists());
    }

    #[test]
    fn plugin_transaction_preserves_newer_registry_entry() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let (_stage, backup) = interrupted_transaction(dir, true, false);
        let mut newer = InstallRegistry::read_unlocked(dir.to_path_buf()).unwrap();
        newer.insert("demo".into(), transaction_repo(dir, "newer"));
        newer.save_atomic().unwrap();
        assert!(InstallRegistry::try_load_from(dir.to_path_buf()).is_err());
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "new"
        );
        assert_eq!(
            std::fs::read_to_string(backup.join("content")).unwrap(),
            "old"
        );
        assert!(dir.join(TRANSACTION_FILE).exists());
    }

    #[test]
    fn plugin_transaction_preserves_replaced_backup() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let (_stage, backup) = interrupted_transaction(dir, true, false);
        std::fs::remove_dir_all(&backup).unwrap();
        std::fs::create_dir(&backup).unwrap();
        std::fs::write(backup.join("external"), "safe").unwrap();
        assert!(InstallRegistry::try_load_from(dir.to_path_buf()).is_err());
        assert_eq!(
            std::fs::read_to_string(backup.join("external")).unwrap(),
            "safe"
        );
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "new"
        );
    }

    #[test]
    fn registry_save_merges_distinct_concurrent_updates() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let mut initial = InstallRegistry::empty(dir.to_path_buf());
        initial.insert("local".into(), transaction_repo(dir, "old"));
        initial.save().unwrap();
        let mut refresh = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let mut installer = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let stage = super::super::git_install::create_staging_dir(&installer).unwrap();
        let new_repo = transaction_repo(dir, "new");
        installer.publish_staged("demo", new_repo, &stage).unwrap();
        refresh.get_repo_mut("local").unwrap().updated_at = "refreshed".into();
        refresh.save().unwrap();
        let disk = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        assert_eq!(disk.get_repo("local").unwrap().updated_at, "refreshed");
        assert!(disk.get_repo("demo").is_some());
        assert!(dir.join("demo").is_dir());
    }

    #[test]
    fn registry_save_rejects_stale_change_to_same_entry() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let mut initial = InstallRegistry::empty(dir.to_path_buf());
        initial.insert("demo".into(), transaction_repo(dir, "old"));
        initial.save().unwrap();
        let mut first = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        let mut second = InstallRegistry::try_load_from(dir.to_path_buf()).unwrap();
        first.get_repo_mut("demo").unwrap().updated_at = "first".into();
        first.save().unwrap();
        second.get_repo_mut("demo").unwrap().updated_at = "second".into();
        assert!(second.save().is_err());
        assert_eq!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("demo")
                .unwrap()
                .updated_at,
            "first"
        );
    }

    #[test]
    fn plugin_transaction_save_failure_rolls_back_new_install_and_retries() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let mut registry = InstallRegistry::empty(dir.to_path_buf());
        let stage = super::super::git_install::create_staging_dir(&registry).unwrap();
        std::fs::write(stage.join("content"), "first").unwrap();
        let repo = transaction_repo(dir, "new");
        let error = registry.publish_staged_with("demo", repo.clone(), &stage, |_| {
            Err(InstallError::InstallFailed {
                detail: "injected save failure".into(),
            })
        });
        assert!(error.is_err());
        assert!(!dir.join("demo").exists());
        assert!(!stage.exists());
        assert!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("demo")
                .is_none()
        );

        let retry = super::super::git_install::create_staging_dir(&registry).unwrap();
        std::fs::write(retry.join("content"), "retry").unwrap();
        registry.publish_staged("demo", repo, &retry).unwrap();
        assert_eq!(
            std::fs::read_to_string(dir.join("demo/content")).unwrap(),
            "retry"
        );
        assert!(
            InstallRegistry::try_load_from(dir.to_path_buf())
                .unwrap()
                .get_repo("demo")
                .is_some()
        );
    }

    #[cfg(unix)]
    #[test]
    fn plugin_transaction_never_follows_staged_marker_symlink() {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path();
        let external = dir.join("external");
        std::fs::write(&external, "safe").unwrap();
        let mut registry = InstallRegistry::empty(dir.to_path_buf());
        let stage = super::super::git_install::create_staging_dir(&registry).unwrap();
        std::os::unix::fs::symlink(&external, stage.join(TRANSACTION_MARKER)).unwrap();
        assert!(
            registry
                .publish_staged("demo", transaction_repo(dir, "new"), &stage)
                .is_err()
        );
        assert_eq!(std::fs::read_to_string(external).unwrap(), "safe");
        assert!(!dir.join("demo").exists());
    }

    #[test]
    fn repo_key_from_https_url() {
        let key = InstallRegistry::repo_key("https://github.com/user/my-linter");
        assert!(key.starts_with("my-linter-"));
        assert_eq!(key.len(), "my-linter-".len() + 8);
    }

    #[test]
    fn repo_key_from_ssh_url() {
        let key = InstallRegistry::repo_key("git@github.com:user/my-plugin.git");
        assert!(key.starts_with("my-plugin-"));
    }

    #[test]
    fn repo_key_from_local_path() {
        let key = InstallRegistry::repo_key("/Users/me/projects/my-tools");
        assert!(key.starts_with("my-tools-"));
    }

    #[test]
    fn repo_key_collision_safety() {
        let key_a = InstallRegistry::repo_key("https://github.com/org-a/tools");
        let key_b = InstallRegistry::repo_key("https://github.com/org-b/tools");
        assert_ne!(
            key_a, key_b,
            "different sources should produce different keys"
        );
        assert!(key_a.starts_with("tools-"));
        assert!(key_b.starts_with("tools-"));
    }

    #[test]
    fn empty_registry_crud() {
        let tmp = tempfile::tempdir().unwrap();
        let mut reg = InstallRegistry::empty(tmp.path().to_path_buf());
        assert!(reg.repos.is_empty());
        assert!(reg.list().is_empty());

        // Insert
        reg.insert(
            "test-repo-12345678".to_string(),
            InstalledRepo {
                kind: InstallKind::Git {
                    url: "https://github.com/user/test".to_string(),
                    git_ref: Some("main".to_string()),
                    commit: "abc123".to_string(),
                    subdir: None,
                },
                installed_at: "2026-01-01T00:00:00Z".to_string(),
                updated_at: "2026-01-01T00:00:00Z".to_string(),
                path: tmp.path().join("test-repo-12345678"),
                plugins: HashMap::from([(
                    "my-plugin".to_string(),
                    RepoPlugin {
                        subdir: None,
                        version: Some("1.0.0".to_string()),
                    },
                )]),
                marketplace: None,
            },
        );

        assert_eq!(reg.repos.len(), 1);
        assert!(reg.get_repo("test-repo-12345678").is_some());
        assert!(reg.find_plugin("my-plugin").is_some());
        assert!(reg.find_plugin("nonexistent").is_none());

        // Save and reload
        reg.save().unwrap();
        let registry_path = tmp.path().join("registry.json");
        assert!(registry_path.exists());

        // Remove
        let removed = reg.remove("test-repo-12345678");
        assert!(removed.is_some());
        assert!(reg.repos.is_empty());
    }

    #[test]
    fn save_and_load_roundtrip() {
        let tmp = tempfile::tempdir().unwrap();
        let mut reg = InstallRegistry::empty(tmp.path().to_path_buf());

        reg.insert(
            "my-linter-aabbccdd".to_string(),
            InstalledRepo {
                kind: InstallKind::Local {
                    source_path: PathBuf::from("/home/user/plugins/linter"),
                    subdir: None,
                },
                installed_at: "2026-03-26T12:00:00Z".to_string(),
                updated_at: "2026-03-26T12:00:00Z".to_string(),
                path: tmp.path().join("my-linter-aabbccdd"),
                plugins: HashMap::from([
                    (
                        "lint-check".to_string(),
                        RepoPlugin {
                            subdir: Some("lint-check".to_string()),
                            version: None,
                        },
                    ),
                    (
                        "lint-fix".to_string(),
                        RepoPlugin {
                            subdir: Some("lint-fix".to_string()),
                            version: Some("2.0.0".to_string()),
                        },
                    ),
                ]),
                marketplace: None,
            },
        );

        reg.save().unwrap();

        // Read the JSON back and parse
        let content = std::fs::read_to_string(tmp.path().join("registry.json")).unwrap();
        let loaded: InstallRegistry = serde_json::from_str(&content).unwrap();

        assert_eq!(loaded.version, 1);
        assert_eq!(loaded.repos.len(), 1);
        let repo = loaded.get_repo("my-linter-aabbccdd").unwrap();
        assert_eq!(repo.plugins.len(), 2);
        assert!(repo.plugins.contains_key("lint-check"));
        assert!(repo.plugins.contains_key("lint-fix"));
    }

    #[test]
    fn find_plugin_across_repos() {
        let tmp = tempfile::tempdir().unwrap();
        let mut reg = InstallRegistry::empty(tmp.path().to_path_buf());

        reg.insert(
            "repo-a-11111111".to_string(),
            InstalledRepo {
                kind: InstallKind::Git {
                    url: "https://example.com/a".to_string(),
                    git_ref: None,
                    commit: "aaa".to_string(),
                    subdir: None,
                },
                installed_at: String::new(),
                updated_at: String::new(),
                path: tmp.path().join("repo-a-11111111"),
                plugins: HashMap::from([(
                    "alpha".to_string(),
                    RepoPlugin {
                        subdir: None,
                        version: None,
                    },
                )]),
                marketplace: None,
            },
        );

        reg.insert(
            "repo-b-22222222".to_string(),
            InstalledRepo {
                kind: InstallKind::Git {
                    url: "https://example.com/b".to_string(),
                    git_ref: None,
                    commit: "bbb".to_string(),
                    subdir: None,
                },
                installed_at: String::new(),
                updated_at: String::new(),
                path: tmp.path().join("repo-b-22222222"),
                plugins: HashMap::from([(
                    "beta".to_string(),
                    RepoPlugin {
                        subdir: Some("beta".to_string()),
                        version: None,
                    },
                )]),
                marketplace: None,
            },
        );

        let (key, _, _) = reg.find_plugin("alpha").unwrap();
        assert_eq!(key, "repo-a-11111111");

        let (key, _, plugin) = reg.find_plugin("beta").unwrap();
        assert_eq!(key, "repo-b-22222222");
        assert_eq!(plugin.subdir.as_deref(), Some("beta"));

        assert!(reg.find_plugin("gamma").is_none());
    }

    #[test]
    fn find_repo_key_by_plugin_root_handles_subdir_plugins() {
        let tmp = tempfile::tempdir().unwrap();
        let repo_root = tmp.path().join("repo-a-11111111");
        let plugin_root = repo_root.join("plugins").join("nested");
        std::fs::create_dir_all(&plugin_root).unwrap();
        let canonical_plugin_root = dunce::canonicalize(&plugin_root).unwrap();
        let mut reg = InstallRegistry::empty(tmp.path().to_path_buf());
        reg.insert(
            "repo-a-11111111".to_string(),
            InstalledRepo {
                kind: InstallKind::Local {
                    source_path: repo_root.clone(),
                    subdir: None,
                },
                installed_at: String::new(),
                updated_at: String::new(),
                path: repo_root,
                plugins: HashMap::from([(
                    "nested".to_string(),
                    RepoPlugin {
                        subdir: Some("plugins/nested".to_string()),
                        version: None,
                    },
                )]),
                marketplace: None,
            },
        );

        assert_eq!(
            reg.find_repo_key_by_plugin_root(&plugin_root, &canonical_plugin_root),
            Some("repo-a-11111111")
        );
    }

    #[test]
    fn git_kind_without_subdir_field_deserializes_to_none() {
        let json = r#"{"type":"Git","url":"https://example.com/r","commit":"abc"}"#;
        let kind: InstallKind = serde_json::from_str(json).unwrap();
        match kind {
            InstallKind::Git { url, subdir, .. } => {
                assert_eq!(url, "https://example.com/r");
                assert!(subdir.is_none());
            }
            _ => panic!("expected Git"),
        }
    }

    #[test]
    fn local_kind_without_subdir_field_deserializes_to_none() {
        let json = r#"{"type":"Local","source_path":"/home/user/plugin"}"#;
        let kind: InstallKind = serde_json::from_str(json).unwrap();
        match kind {
            InstallKind::Local {
                source_path,
                subdir,
            } => {
                assert_eq!(source_path, PathBuf::from("/home/user/plugin"));
                assert!(subdir.is_none());
            }
            _ => panic!("expected Local"),
        }
    }

    #[test]
    fn local_kind_with_subdir_round_trips() {
        let kind = InstallKind::Local {
            source_path: PathBuf::from("/home/user/workspace"),
            subdir: Some("plugins/foo".to_string()),
        };
        let json = serde_json::to_string(&kind).unwrap();
        let back: InstallKind = serde_json::from_str(&json).unwrap();
        match back {
            InstallKind::Local { subdir, .. } => {
                assert_eq!(subdir.as_deref(), Some("plugins/foo"));
            }
            _ => panic!("expected Local"),
        }
    }
}

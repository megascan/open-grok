//! Recovery-aware point-in-time view of local session storage.
use super::{RelocationError, RelocationJournal, RelocationStorage, Result, journal};
use std::collections::{HashMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};
type SessionCandidates = HashMap<String, Vec<PathBuf>>;
#[derive(Default)]
struct ScanIssues {
    unknown_workspace_entry: bool,
    incomplete_workspaces: HashSet<PathBuf>,
    unreadable_workspaces: HashSet<PathBuf>,
    incomplete_ids: HashSet<String>,
}

pub(crate) struct RelocationView {
    grok_home: PathBuf,
    sessions_root: PathBuf,
    journals: HashMap<String, RelocationJournal>,
    all_candidates: SessionCandidates,
    persisted_candidates: SessionCandidates,
    scan_issues: ScanIssues,
}
impl RelocationView {
    pub(crate) fn load(grok_home: &Path) -> Result<Self> {
        Self::load_for_sessions_root(&grok_home.join("sessions"))
    }
    pub(crate) fn journal_ids(grok_home: &Path) -> Result<Vec<String>> {
        Ok(load_journals(grok_home)?.into_keys().collect())
    }
    pub(crate) fn load_for_sessions_root(sessions_root: &Path) -> Result<Self> {
        Self::load_for_sessions_root_with(sessions_root, |path| fs::read_dir(path))
    }
    pub(super) fn load_for_sessions_root_with(
        sessions_root: &Path,
        read_dir: impl FnMut(&Path) -> std::io::Result<fs::ReadDir>,
    ) -> Result<Self> {
        let grok_home = sessions_root
            .parent()
            .ok_or_else(|| RelocationError::Inconsistent("sessions root has no parent".into()))?;
        let journals = load_journals(grok_home)?;
        let (all_candidates, persisted_candidates, scan_issues) =
            load_candidates(sessions_root, read_dir)?;
        Ok(Self {
            grok_home: grok_home.into(),
            sessions_root: sessions_root.into(),
            journals,
            all_candidates,
            persisted_candidates,
            scan_issues,
        })
    }
    pub(crate) fn protects_cwd_dir(&self, cwd_dir: &Path) -> bool {
        self.journals.values().any(|journal| {
            [&journal.source_cwd, &journal.target_cwd]
                .into_iter()
                .any(|cwd| {
                    self.sessions_root
                        .join(xai_grok_config::encode_cwd_dirname(cwd))
                        == cwd_dir
                })
        })
    }
    pub(crate) fn has_journals(&self) -> bool {
        !self.journals.is_empty()
    }
    pub(crate) fn session_dirs(&self, cwd: Option<&str>) -> Result<Vec<PathBuf>> {
        self.session_dirs_inner(cwd, None)
    }
    /// Best-effort listing omits any unverified relocation, while strict
    /// lookup/replay and mutation continue to propagate authority failures.
    pub(crate) fn listing_dirs_with_completeness(
        &self,
        cwd: Option<&str>,
        excluded: &HashSet<String>,
    ) -> Result<(Vec<PathBuf>, bool)> {
        let mut complete = excluded.is_empty()
            && !self.scan_issues.unknown_workspace_entry
            && self.scan_issues.incomplete_workspaces.is_empty()
            && self.scan_issues.incomplete_ids.is_empty();
        let dirs = self.session_dirs_inner_impl(cwd, Some(excluded), &mut complete)?;
        Ok((dirs, complete))
    }
    fn session_dirs_inner(
        &self,
        cwd: Option<&str>,
        excluded: Option<&HashSet<String>>,
    ) -> Result<Vec<PathBuf>> {
        self.session_dirs_inner_impl(cwd, excluded, &mut true)
    }
    fn session_dirs_inner_impl(
        &self,
        cwd: Option<&str>,
        excluded: Option<&HashSet<String>>,
        complete: &mut bool,
    ) -> Result<Vec<PathBuf>> {
        let cwd_parent = cwd.map(|cwd| {
            self.sessions_root
                .join(xai_grok_config::encode_cwd_dirname(cwd))
        });
        if let Some(parent) = cwd_parent.as_deref()
            && (self.scan_issues.unknown_workspace_entry
                || self.scan_issues.unreadable_workspaces.contains(parent))
        {
            return Err(RelocationError::Inconsistent(format!(
                "cannot list sessions for unreadable workspace {}",
                parent.display()
            )));
        }
        let mut ids = self
            .persisted_candidates
            .iter()
            .filter_map(|(id, paths)| match self.journals.get(id) {
                Some(relocation) => cwd
                    .is_none_or(|cwd| authoritative_cwd(relocation) == cwd)
                    .then_some(id),
                None => cwd_parent
                    .as_deref()
                    .is_none_or(|parent| paths.iter().any(|path| path.parent() == Some(parent)))
                    .then_some(id),
            })
            .collect::<Vec<_>>();
        ids.extend(self.journals.iter().filter_map(|(id, relocation)| {
            (!self.persisted_candidates.contains_key(id)
                && cwd.is_none_or(|cwd| authoritative_cwd(relocation) == cwd))
            .then_some(id)
        }));
        let mut selected = Vec::new();
        for id in ids {
            if excluded.is_some_and(|ids| ids.contains(id)) {
                continue;
            }
            let paths = self
                .persisted_candidates
                .get(id)
                .map(Vec::as_slice)
                .unwrap_or(&[]);
            match self.select(id, paths, cwd_parent.as_deref()) {
                Ok(Some(path)) => selected.push(path),
                Ok(None) => {
                    *complete = false;
                }
                Err(error) if excluded.is_some() => {
                    *complete = false;
                    tracing::warn!(session_id = %id, %error, "skipping unverified relocated session in listing");
                }
                Err(error) => return Err(error),
            }
        }
        Ok(selected)
    }
    pub(crate) fn find_persisted_session_dir(&self, session_id: &str) -> Result<Option<PathBuf>> {
        self.find_session_dir(session_id, &self.persisted_candidates)
    }
    pub(crate) fn find_any_session_dir(&self, session_id: &str) -> Result<Option<PathBuf>> {
        self.find_session_dir(session_id, &self.all_candidates)
    }
    fn find_session_dir(
        &self,
        session_id: &str,
        candidates: &SessionCandidates,
    ) -> Result<Option<PathBuf>> {
        journal::validate_component("session id", session_id)?;
        let paths = candidates.get(session_id).map(Vec::as_slice).unwrap_or(&[]);
        // A journal identifies the authoritative path even when another
        // workspace is unreadable. Otherwise an incomplete scan must not turn
        // a potentially ambiguous ID into a unique result (or a false miss).
        if !self.journals.contains_key(session_id) {
            self.ensure_id_scan_complete(session_id)?;
            if paths.len() > 1 {
                return Err(RelocationError::Inconsistent(format!(
                    "session ID {session_id} exists in multiple workspaces"
                )));
            }
        }
        if paths.is_empty() && !self.journals.contains_key(session_id) {
            return Ok(None);
        }
        self.select(session_id, paths, None)
    }
    fn ensure_id_scan_complete(&self, session_id: &str) -> Result<()> {
        if self.scan_issues.unknown_workspace_entry
            || self.scan_issues.incomplete_ids.contains(session_id)
        {
            return Err(RelocationError::Inconsistent(format!(
                "session discovery is incomplete for {session_id}"
            )));
        }
        for cwd in &self.scan_issues.incomplete_workspaces {
            let candidate = cwd.join(session_id);
            match fs::symlink_metadata(&candidate) {
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
                _ => {
                    return Err(RelocationError::Inconsistent(format!(
                        "session discovery cannot rule out {}",
                        candidate.display()
                    )));
                }
            }
        }
        Ok(())
    }
    fn select(
        &self,
        session_id: &str,
        paths: &[PathBuf],
        cwd_parent: Option<&Path>,
    ) -> Result<Option<PathBuf>> {
        let selected = if let Some(relocation) = self.journals.get(session_id) {
            let expected =
                journal::session_dir_at(&self.grok_home, authoritative_cwd(relocation), session_id);
            let path = self
                .all_candidates
                .get(session_id)
                .and_then(|paths| paths.iter().find(|path| **path == expected))
                .cloned()
                .ok_or_else(|| {
                    RelocationError::Inconsistent(format!(
                        "authoritative {:?} session path is missing: {}",
                        relocation.phase,
                        expected.display()
                    ))
                })?;
            RelocationStorage::new(self.grok_home.clone())
                .validate_authoritative_dir(relocation, &path)?;
            Some(path)
        } else if paths.len() == 1 {
            Some(paths[0].clone())
        } else {
            None
        };
        Ok(selected.filter(|path| cwd_parent.is_none_or(|parent| path.parent() == Some(parent))))
    }
}
fn authoritative_cwd(relocation: &RelocationJournal) -> &str {
    if super::has_target_authority(relocation.phase) {
        &relocation.target_cwd
    } else {
        &relocation.source_cwd
    }
}
// Keep failure of the root scan fatal: an unavailable root is not an empty
// store. Once it is open, failures confined to an individual workspace or
// entry should not hide sessions in other workspaces. Journal authority is
// checked separately by `select` and is never inferred from this partial scan.
fn load_candidates(
    sessions_root: &Path,
    mut read_dir: impl FnMut(&Path) -> std::io::Result<fs::ReadDir>,
) -> Result<(SessionCandidates, SessionCandidates, ScanIssues)> {
    let entries = match read_dir(sessions_root) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok((HashMap::new(), HashMap::new(), ScanIssues::default()));
        }
        Err(error) => return Err(super::fs::io_error("read", sessions_root, error)),
    };
    let mut all = SessionCandidates::new();
    let mut persisted = SessionCandidates::new();
    let mut scan_issues = ScanIssues::default();
    for cwd_entry in entries {
        let cwd_entry = match cwd_entry {
            Ok(entry) => entry,
            Err(error) => {
                tracing::warn!(path = %sessions_root.display(), %error, "skipping unreadable workspace entry during session discovery");
                scan_issues.unknown_workspace_entry = true;
                continue;
            }
        };
        let cwd_path = cwd_entry.path();
        let cwd_type = match cwd_entry.file_type() {
            Ok(file_type) => file_type,
            Err(error) => {
                tracing::warn!(path = %cwd_path.display(), %error, "skipping inaccessible workspace during session discovery");
                scan_issues.unreadable_workspaces.insert(cwd_path.clone());
                scan_issues.incomplete_workspaces.insert(cwd_path);
                continue;
            }
        };
        if !cwd_type.is_dir() || cwd_type.is_symlink() {
            continue;
        }
        let session_entries = match read_dir(&cwd_path) {
            Ok(entries) => entries,
            Err(error) => {
                tracing::warn!(path = %cwd_path.display(), %error, "skipping unreadable workspace during session discovery");
                scan_issues.unreadable_workspaces.insert(cwd_path.clone());
                scan_issues.incomplete_workspaces.insert(cwd_path);
                continue;
            }
        };
        for session_entry in session_entries {
            let session_entry = match session_entry {
                Ok(entry) => entry,
                Err(error) => {
                    tracing::warn!(path = %cwd_path.display(), %error, "skipping unreadable session entry during discovery");
                    scan_issues.incomplete_workspaces.insert(cwd_path.clone());
                    continue;
                }
            };
            let path = session_entry.path();
            let Some(id) = session_entry.file_name().to_str().map(str::to_owned) else {
                continue;
            };
            let file_type = match session_entry.file_type() {
                Ok(file_type) => file_type,
                Err(error) => {
                    tracing::warn!(path = %path.display(), %error, "skipping inaccessible session during discovery");
                    scan_issues.incomplete_ids.insert(id);
                    continue;
                }
            };
            if !file_type.is_dir() || file_type.is_symlink() || id.starts_with('.') {
                continue;
            }
            all.entry(id.clone()).or_default().push(path.clone());
            let summary = path.join(super::super::SUMMARY_FILE);
            match fs::symlink_metadata(&summary) {
                Ok(metadata)
                    if metadata.file_type().is_file() && !metadata.file_type().is_symlink() =>
                {
                    persisted.entry(id).or_default().push(path);
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                Err(error) => {
                    tracing::warn!(path = %summary.display(), %error, "skipping inaccessible session summary during discovery");
                    scan_issues.incomplete_ids.insert(id);
                }
                Ok(_) => {}
            }
        }
    }
    Ok((all, persisted, scan_issues))
}
fn load_journals(grok_home: &Path) -> Result<HashMap<String, RelocationJournal>> {
    let dir = journal::relocation_dir(grok_home);
    let entries = match fs::read_dir(&dir) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(HashMap::new()),
        Err(error) => return Err(super::fs::io_error("read", &dir, error)),
    };
    let mut journals = HashMap::new();
    for entry in entries {
        let entry = entry.map_err(|error| super::fs::io_error("read", &dir, error))?;
        let path = entry.path();
        if path.extension().is_none_or(|extension| extension != "json") {
            continue;
        }
        let session_id = path
            .file_stem()
            .and_then(|name| name.to_str())
            .ok_or_else(|| RelocationError::Inconsistent("journal name is not UTF-8".into()))?;
        journals.insert(session_id.to_owned(), journal::read(grok_home, session_id)?);
    }
    Ok(journals)
}

//! Skills listings and rows for folders a backend could not scan.

use super::{ExtensionsModalState, TabDataState};
use xai_grok_shell::extensions::skills::{SkillScanError, SkillsListResponse};

impl ExtensionsModalState {
    /// IDs remain unique across modal close/reopen and session changes.
    pub fn next_skills_fetch(&mut self) -> u64 {
        static NEXT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(1);
        self.skills_fetch = NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        self.skills_fetch
    }

    pub fn show_skills_listing(
        &mut self,
        fetch: u64,
        result: Result<SkillsListResponse, String>,
    ) -> bool {
        if fetch != self.skills_fetch {
            return false;
        }
        match result {
            Ok(listing) => self.apply_skills_listing(listing),
            Err(error) => self.skills_data = TabDataState::Error(error),
        }
        true
    }

    pub fn apply_skills_listing(&mut self, listing: SkillsListResponse) {
        self.seed_skills_groups_once(&listing.skills);
        self.skills_scan_errors = listing.scan_errors;
        self.skills_data = TabDataState::Loaded(listing.skills);
    }
}

pub(super) fn scan_error_rows(errors: &[SkillScanError], query: &str) -> Vec<String> {
    if errors.is_empty() || !query.is_empty() {
        return Vec::new();
    }
    let header = match errors.len() {
        1 => "Couldn't scan (1 folder) · r to reload".to_owned(),
        count => format!("Couldn't scan ({count} folders) · r to reload"),
    };
    std::iter::once(header)
        .chain(
            errors
                .iter()
                .map(|error| format!("{}: {}", error.path, error.message)),
        )
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::views::extensions_modal::ExtensionsTab;

    #[test]
    fn latest_fetch_wins_across_modal_reopen() {
        let mut old = ExtensionsModalState::new(ExtensionsTab::Skills);
        let first = old.next_skills_fetch();
        let mut current = ExtensionsModalState::new(ExtensionsTab::Skills);
        let second = current.next_skills_fetch();
        assert_ne!(first, second);
        assert!(!current.show_skills_listing(first, Err("old".into())));
        assert!(current.show_skills_listing(second, Err("new".into())));
        assert!(matches!(&current.skills_data, TabDataState::Error(error) if error == "new"));
    }

    #[test]
    fn scan_errors_are_visible_when_not_searching() {
        let errors = vec![SkillScanError {
            path: "/work/locked".into(),
            message: "permission denied".into(),
        }];
        assert_eq!(
            scan_error_rows(&errors, ""),
            vec![
                "Couldn't scan (1 folder) · r to reload",
                "/work/locked: permission denied"
            ]
        );
        assert!(scan_error_rows(&errors, "abc").is_empty());
    }
}

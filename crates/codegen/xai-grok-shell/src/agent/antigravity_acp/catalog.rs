//! Parse only model selectors advertised by an ACP session.

use serde_json::Value;

#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct ModelCatalog {
    pub config_id: Option<String>,
    pub current: Option<String>,
    pub models: Vec<String>,
}

const MAX_CACHE_BYTES: usize = 64 * 1024;
const MAX_MODELS: usize = 256;
const ROUTE_PREFIX: &str = "antigravity-acp:";

impl ModelCatalog {
    fn valid(&self) -> bool {
        !self.models.is_empty()
            && self.models.len() <= MAX_MODELS
            && self
                .models
                .iter()
                .all(|id| !id.is_empty() && id.len() <= 256 && !id.chars().any(char::is_control))
    }

    /// Explicit child model tokens are matched only against this typed catalog.
    pub fn child_model_names(&self) -> Vec<String> {
        self.models
            .iter()
            .map(|id| format!("{ROUTE_PREFIX}{id}"))
            .collect()
    }

    pub(crate) fn resolve_child_model<'a>(&'a self, selected: &str) -> Option<&'a str> {
        if !self.valid() {
            return None;
        }
        self.models
            .iter()
            .find(|id| selected == format!("{ROUTE_PREFIX}{id}"))
            .map(String::as_str)
    }
}

fn cache_path(home: &std::path::Path) -> std::path::PathBuf {
    home.join("antigravity-acp").join("model-catalog.json")
}

/// This cache contains model IDs only, never credentials. A child revalidates
/// its chosen ID against the live server catalog before sending its prompt.
pub fn cached_catalog(home: &std::path::Path) -> Option<ModelCatalog> {
    use std::io::Read;
    let file = std::fs::File::open(cache_path(home)).ok()?;
    let mut bytes = Vec::new();
    file.take((MAX_CACHE_BYTES + 1) as u64)
        .read_to_end(&mut bytes)
        .ok()?;
    if bytes.len() > MAX_CACHE_BYTES {
        return None;
    }
    let catalog: ModelCatalog = serde_json::from_slice(&bytes).ok()?;
    catalog.valid().then_some(catalog)
}

pub(super) fn save_catalog(home: &std::path::Path, catalog: &ModelCatalog) -> super::Result<()> {
    if !catalog.valid() {
        return Err(super::Error::ModelUnavailable);
    }
    let data = serde_json::to_string(catalog).map_err(|_| super::Error::Protocol)?;
    if data.len() > MAX_CACHE_BYTES {
        return Err(super::Error::ModelUnavailable);
    }
    xai_grok_config::fs_atomic::write_atomically(&cache_path(home), &data, Some(0o600))?;
    Ok(())
}

pub(super) fn clear_catalog(home: &std::path::Path) -> super::Result<()> {
    match std::fs::remove_file(cache_path(home)) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e.into()),
    }
}

#[cfg(test)]
mod cache_tests {
    use super::*;

    #[test]
    fn cached_child_route_is_explicit_and_account_local() {
        let home = tempfile::tempdir().unwrap();
        let other = tempfile::tempdir().unwrap();
        std::fs::create_dir(home.path().join("antigravity-acp")).unwrap();
        let catalog = ModelCatalog {
            config_id: Some("model".into()),
            current: Some("claude".into()),
            models: vec!["claude".into(), "gemini".into()],
        };
        save_catalog(home.path(), &catalog).unwrap();
        let cached = cached_catalog(home.path()).unwrap();
        assert_eq!(
            cached.resolve_child_model("antigravity-acp:claude"),
            Some("claude")
        );
        assert_eq!(cached.resolve_child_model("claude"), None);
        assert_eq!(cached.resolve_child_model("antigravity-acp:unknown"), None);
        assert!(cached_catalog(other.path()).is_none());
        clear_catalog(home.path()).unwrap();
        assert!(cached_catalog(home.path()).is_none());
    }

    #[test]
    fn invalid_and_oversized_catalogs_fail_closed() {
        let home = tempfile::tempdir().unwrap();
        std::fs::create_dir(home.path().join("antigravity-acp")).unwrap();
        let invalid = ModelCatalog {
            config_id: None,
            current: None,
            models: vec!["bad\nmodel".into()],
        };
        assert!(save_catalog(home.path(), &invalid).is_err());
        std::fs::write(cache_path(home.path()), vec![b'a'; MAX_CACHE_BYTES + 1]).unwrap();
        assert!(cached_catalog(home.path()).is_none());
    }
}

impl ModelCatalog {
    pub fn from_session(session: &Value) -> Option<Self> {
        if let Some(options) = session.get("configOptions").and_then(Value::as_array) {
            // The semantic category is optional in ACP; the conventional ID is
            // useful only when it does not conflict with another category.
            let selector = options.iter().find(|item| {
                item.get("type").and_then(Value::as_str) == Some("select")
                    && (item.get("category").and_then(Value::as_str) == Some("model")
                        || (item.get("category").is_none()
                            && item.get("id").and_then(Value::as_str) == Some("model")))
            });
            if let Some(selector) = selector {
                let mut models = Vec::new();
                if let Some(options) = selector.get("options").and_then(Value::as_array) {
                    for option in options {
                        if let Some(value) = option.get("value").and_then(Value::as_str) {
                            models.push(value.to_owned());
                        } else if let Some(group) = option.get("options").and_then(Value::as_array)
                        {
                            models.extend(group.iter().filter_map(|value| {
                                value
                                    .get("value")
                                    .and_then(Value::as_str)
                                    .map(str::to_owned)
                            }));
                        }
                    }
                }
                return Some(Self {
                    config_id: selector
                        .get("id")
                        .and_then(Value::as_str)
                        .map(str::to_owned),
                    current: selector
                        .get("currentValue")
                        .and_then(Value::as_str)
                        .map(str::to_owned),
                    models,
                });
            }
        }
        // Older ACP servers may still return the deprecated models shape.
        let models = session.get("models")?;
        Some(Self {
            config_id: None,
            current: models
                .get("currentModelId")
                .and_then(Value::as_str)
                .map(str::to_owned),
            models: models
                .get("availableModels")
                .and_then(Value::as_array)?
                .iter()
                .filter_map(|m| m.get("modelId").and_then(Value::as_str).map(str::to_owned))
                .collect(),
        })
    }
}

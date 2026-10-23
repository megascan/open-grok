use super::*;

#[test]
fn token_path_metadata_round_trips_and_keeps_the_configured_spelling() {
    let path = tempfile::tempdir().unwrap().path().join("token");
    let text = path.to_str().unwrap().to_owned();
    let meta = bearer_token_file_meta(&text);
    assert_eq!(
        Ok(Some(BearerTokenPath(path))),
        BearerTokenPath::from_meta(Some(&meta))
    );
    assert_eq!(Some(text), configured_bearer_token_file(Some(&meta)));
    assert_eq!(Ok(None), BearerTokenPath::from_meta(None));
}

#[test]
fn token_path_validates_type_and_absolute_or_home_relative_path() {
    let home = tempfile::tempdir().unwrap();
    assert_eq!(
        Ok(BearerTokenPath(home.path().join(".tok"))),
        BearerTokenPath::parse("~/.tok", Some(home.path()))
    );
    assert_eq!(
        Err(BearerTokenPathError::NoHomeDir("~/.tok".into())),
        BearerTokenPath::parse("~/.tok", None)
    );
    assert_eq!(
        Err(BearerTokenPathError::NotAbsolute("tok".into())),
        BearerTokenPath::from_meta(Some(&bearer_token_file_meta("tok")))
    );
    let wrong_type = acp::Meta::from_iter([(META_KEY.to_owned(), serde_json::json!(7))]);
    assert_eq!(
        Err(BearerTokenPathError::NotAString),
        BearerTokenPath::from_meta(Some(&wrong_type))
    );
}

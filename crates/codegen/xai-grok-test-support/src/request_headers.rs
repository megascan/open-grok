use axum::http::HeaderMap;

pub(crate) fn authorization_header(headers: &HeaderMap) -> Option<String> {
    headers
        .get("authorization")
        .and_then(|value| value.to_str().ok())
        .map(String::from)
}

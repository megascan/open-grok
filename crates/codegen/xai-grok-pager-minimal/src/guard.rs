//! Compile-time guard for minimal mode's resize strategy (design K6 / risk #2).
//!
//! Minimal reprints history from scrollback entries through the commit renderer.
//! It must not use the inline crate's string-history resize helpers, which
//! would replay stale or hard-wrapped text.

/// The forbidden inline-crate helpers. Scanned against the minimal sources via
/// `include_str!` (this guard file is intentionally not scanned, since it names
/// the identifiers here).
#[test]
fn minimal_never_uses_ris_rerender_or_emit_to_scrollback() {
    const FORBIDDEN: &[&str] = &[
        "resize_purge_rerender",
        "emit_to_scrollback",
        "resize_viewport_height",
    ];
    // EVERY module of this crate except this guard file (which names the
    // forbidden identifiers). Keep in sync with `lib.rs`'s module list — a
    // module missing here is a hole in the K6 guard.
    let sources = [
        ("lib.rs", include_str!("lib.rs")),
        ("auth.rs", include_str!("auth.rs")),
        ("commit.rs", include_str!("commit.rs")),
        ("full_view.rs", include_str!("full_view.rs")),
        ("live.rs", include_str!("live.rs")),
        ("overlay.rs", include_str!("overlay.rs")),
        ("panel.rs", include_str!("panel.rs")),
        ("plan.rs", include_str!("plan.rs")),
        ("reprint.rs", include_str!("reprint.rs")),
        ("todo.rs", include_str!("todo.rs")),
        ("welcome.rs", include_str!("welcome.rs")),
    ];
    for (name, src) in sources {
        for needle in FORBIDDEN {
            assert!(
                !src.contains(needle),
                "minimal/{name} references forbidden resize helper `{needle}`; reprint history from scrollback entries instead"
            );
        }
    }
}

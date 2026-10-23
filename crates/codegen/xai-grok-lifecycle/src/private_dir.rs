//! Validate lifecycle storage paths before using their contents.
//!
//! The host must first protect and validate the trusted parent directories;
//! owner-only permissions do not isolate untrusted commands with the same UID.

use std::fs;
use std::io;
use std::path::Path;

#[cfg(unix)]
pub(crate) fn validate(path: &Path) -> io::Result<()> {
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt};

    let opened = fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW)
        .open(path)?
        .metadata()?;
    let entry = fs::symlink_metadata(path)?;
    if !opened.is_dir()
        || opened.uid() != unsafe { libc::geteuid() }
        || opened.mode() & 0o077 != 0
        || !entry.is_dir()
        || entry.dev() != opened.dev()
        || entry.ino() != opened.ino()
    {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "lifecycle directory is not private or changed",
        ));
    }
    Ok(())
}

#[cfg(not(unix))]
pub(crate) fn validate(path: &Path) -> io::Result<()> {
    if fs::symlink_metadata(path)?.is_dir() {
        Ok(())
    } else {
        Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "lifecycle directory is not a directory",
        ))
    }
}

pub(crate) fn create_and_validate(path: &Path) -> io::Result<()> {
    let mut builder = fs::DirBuilder::new();
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        builder.mode(0o700);
    }
    match builder.create(path) {
        Ok(()) => {}
        Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {}
        Err(error) => return Err(error),
    }
    validate(path)
}

//! A per-invocation filesystem pinned to one session plan file.
//!
//! The directory descriptor is opened without following symlinks. Reads use
//! `openat` with `O_NOFOLLOW`; writes create a new inode and rename it within
//! that descriptor, so a swapped symlink or hard link is never written through.

use std::{
    ffi::{CStr, CString},
    fs::File,
    io::{self, Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd, RawFd},
        unix::{ffi::OsStrExt, fs::MetadataExt},
    },
    path::{Component, Path, PathBuf},
    sync::Arc,
};

use crate::computer::types::{AsyncFileSystem, ComputerError};
use crate::types::resources::{PlanFile, PlanFileRoot};
use nix::libc;

const LEAF: &CStr = c"plan.md";

fn open_at(dir: RawFd, name: &CStr, flags: libc::c_int, mode: libc::mode_t) -> io::Result<File> {
    // SAFETY: `name` is NUL-terminated, `dir` is held open by the caller, and
    // the variadic mode argument uses a C integer wide enough for mode bits.
    let fd = unsafe { libc::openat(dir, name.as_ptr(), flags, mode as libc::c_uint) };
    if fd < 0 {
        Err(io::Error::last_os_error())
    } else {
        // SAFETY: a successful openat returns a new owned file descriptor.
        Ok(unsafe { File::from_raw_fd(fd) })
    }
}

fn invalid(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

fn open_existing(dir: RawFd) -> io::Result<File> {
    let file = open_at(
        dir,
        LEAF,
        libc::O_RDONLY | libc::O_NONBLOCK | libc::O_NOFOLLOW | libc::O_CLOEXEC,
        0,
    )?;
    let metadata = file.metadata()?;
    if !metadata.is_file() || metadata.nlink() != 1 {
        return Err(invalid("plan file must be a regular file with one link"));
    }
    Ok(file)
}

/// Open each directory component independently, without following any link.
fn open_directory(path: &Path) -> io::Result<File> {
    let mut components = path.components();
    if components.next() != Some(Component::RootDir) {
        return Err(invalid("plan directory must be absolute"));
    }
    let dir = File::open("/")?;
    open_relative_directory(&dir, components.as_path())
}

fn open_relative_directory(root: &File, path: &Path) -> io::Result<File> {
    let mut dir = root.try_clone()?;
    for component in path.components() {
        let Component::Normal(name) = component else {
            return Err(invalid("plan directory contains an unsafe component"));
        };
        let name = CString::new(name.as_bytes()).map_err(|_| invalid("invalid plan directory"))?;
        dir = open_at(
            dir.as_raw_fd(),
            &name,
            libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC,
            0,
        )?;
    }
    Ok(dir)
}

pub(super) fn capture_root(path: &Path) -> io::Result<PlanFileRoot> {
    let canonical = dunce::canonicalize(path)?;
    let directory = open_directory(&canonical)?;
    let resolved = std::fs::metadata(path)?;
    let pinned = directory.metadata()?;
    if resolved.dev() != pinned.dev() || resolved.ino() != pinned.ino() {
        return Err(invalid("trusted plan root changed while opening"));
    }
    Ok(PlanFileRoot {
        logical: path.to_path_buf(),
        canonical,
        directory: Arc::new(directory),
    })
}

pub(super) fn open(path: PathBuf, root: PlanFileRoot) -> io::Result<PlanFile> {
    if path.file_name() != Some(std::ffi::OsStr::new("plan.md")) {
        return Err(invalid("plan capability requires the plan.md leaf"));
    }
    let parent = path
        .parent()
        .ok_or_else(|| invalid("missing plan directory"))?;
    let relative = parent
        .strip_prefix(&root.logical)
        .map_err(|_| invalid("plan file is outside the trusted root"))?;
    let dir = open_relative_directory(&root.directory, relative)?;
    match open_existing(dir.as_raw_fd()) {
        Ok(_) => {}
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => return Err(error),
    }
    // Used solely for accepting equivalent authored parent spellings; all I/O
    // below is relative to the already-pinned descriptor.
    let canonical_parent = root.canonical.join(relative);
    let fs = Arc::new(AnchoredPlanFs {
        dir: Arc::new(dir),
        path: path.clone(),
    });
    Ok(PlanFile::new(path, canonical_parent, fs))
}

struct AnchoredPlanFs {
    dir: Arc<File>,
    path: PathBuf,
}

impl AnchoredPlanFs {
    fn validate(&self, path: &Path) -> Result<(), ComputerError> {
        if path == self.path {
            Ok(())
        } else {
            Err(ComputerError::io_with_kind(
                "target is outside the pinned plan file",
                io::ErrorKind::PermissionDenied,
            ))
        }
    }
}

fn read(dir: RawFd, max_bytes: Option<usize>) -> io::Result<Vec<u8>> {
    let mut file = open_existing(dir)?;
    let mut bytes = Vec::new();
    match max_bytes {
        Some(max) => {
            let limit = max
                .checked_add(1)
                .and_then(|n| u64::try_from(n).ok())
                .ok_or_else(|| invalid("file read limit overflow"))?;
            file.take(limit).read_to_end(&mut bytes)?;
            if bytes.len() > max {
                return Err(io::Error::new(
                    io::ErrorKind::FileTooLarge,
                    "file exceeds byte limit",
                ));
            }
        }
        None => {
            file.read_to_end(&mut bytes)?;
        }
    }
    Ok(bytes)
}

fn write(dir: RawFd, data: &[u8]) -> io::Result<()> {
    write_before_rename(dir, data, || Ok(()))
}

fn write_before_rename(
    dir: RawFd,
    data: &[u8],
    before_rename: impl FnOnce() -> io::Result<()>,
) -> io::Result<()> {
    let mode = match open_existing(dir) {
        Ok(file) => (file.metadata()?.mode() & 0o777) as libc::mode_t,
        Err(error) if error.kind() == io::ErrorKind::NotFound => 0o600,
        Err(error) => return Err(error),
    };
    let name =
        CString::new(format!(".plan-{}.tmp", uuid::Uuid::now_v7())).expect("UUID has no NUL");
    let mut temp = open_at(
        dir,
        &name,
        libc::O_WRONLY | libc::O_CREAT | libc::O_EXCL | libc::O_NOFOLLOW | libc::O_CLOEXEC,
        mode,
    )?;
    let result = (|| {
        // Preserve the old mode even when the process umask is restrictive.
        // SAFETY: `temp` is an open descriptor and mode only contains permission bits.
        if unsafe { libc::fchmod(temp.as_raw_fd(), mode) } != 0 {
            return Err(io::Error::last_os_error());
        }
        temp.write_all(data)?;
        // Refuse a symlink or shared inode observed after the temporary write.
        match open_existing(dir) {
            Ok(_) => {}
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(error),
        }
        before_rename()?;
        // SAFETY: names are NUL-terminated and both operations use the pinned directory.
        if unsafe { libc::renameat(dir, name.as_ptr(), dir, LEAF.as_ptr()) } != 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    })();
    if result.is_err() {
        // SAFETY: the name is NUL-terminated and the directory descriptor is live.
        unsafe { libc::unlinkat(dir, name.as_ptr(), 0) };
    }
    result
}

#[async_trait::async_trait]
impl AsyncFileSystem for AnchoredPlanFs {
    async fn read_file(&self, path: &Path) -> Result<Vec<u8>, ComputerError> {
        self.validate(path)?;
        let dir = self.dir.clone();
        tokio::task::spawn_blocking(move || read(dir.as_raw_fd(), None))
            .await
            .map_err(|error| ComputerError::io(error.to_string()))?
            .map_err(ComputerError::from)
    }

    fn supports_bounded_read(&self) -> bool {
        true
    }

    async fn read_file_bounded(
        &self,
        path: &Path,
        max_bytes: usize,
    ) -> Result<Vec<u8>, ComputerError> {
        self.validate(path)?;
        let dir = self.dir.clone();
        tokio::task::spawn_blocking(move || read(dir.as_raw_fd(), Some(max_bytes)))
            .await
            .map_err(|error| ComputerError::io(error.to_string()))?
            .map_err(ComputerError::from)
    }

    async fn write_file(&self, path: &Path, data: &[u8]) -> Result<(), ComputerError> {
        self.validate(path)?;
        let dir = self.dir.clone();
        let data = data.to_vec();
        tokio::task::spawn_blocking(move || write(dir.as_raw_fd(), &data))
            .await
            .map_err(|error| ComputerError::io(error.to_string()))?
            .map_err(ComputerError::from)
    }

    async fn delete_file(&self, _path: &Path) -> Result<(), ComputerError> {
        Err(ComputerError::io_with_kind(
            "plan file deletion is not permitted",
            io::ErrorKind::PermissionDenied,
        ))
    }

    async fn file_exists(&self, path: &Path) -> Result<bool, ComputerError> {
        self.validate(path)?;
        let dir = self.dir.clone();
        tokio::task::spawn_blocking(move || match open_existing(dir.as_raw_fd()) {
            Ok(_) => Ok(true),
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(false),
            Err(error) => Err(error),
        })
        .await
        .map_err(|error| ComputerError::io(error.to_string()))?
        .map_err(ComputerError::from)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::computer::local::{LocalFs, MockFs};
    use std::os::unix::fs::symlink;

    fn dir() -> tempfile::TempDir {
        // On macOS the system temp directory can be spelled through a symlink;
        // the capability deliberately requires a no-follow parent path.
        tempfile::tempdir().unwrap()
    }

    fn canonical_dir(dir: &tempfile::TempDir) -> PathBuf {
        dunce::canonicalize(dir.path()).unwrap()
    }

    #[tokio::test]
    async fn plan_capability_writes_new_and_existing_and_rejects_other_paths() {
        let temp = dir();
        let plan_path = canonical_dir(&temp).join("plan.md");
        let plan = LocalFs.open_plan_file(&plan_path).await.unwrap();
        let fs = plan.filesystem();
        assert_eq!(
            fs.read_file(&plan_path).await.unwrap_err().io_error_kind(),
            Some(io::ErrorKind::NotFound)
        );
        fs.write_file(&plan_path, b"first").await.unwrap();
        assert_eq!(fs.read_file(&plan_path).await.unwrap(), b"first");
        assert_eq!(
            fs.read_file_bounded(&plan_path, 3)
                .await
                .unwrap_err()
                .io_error_kind(),
            Some(io::ErrorKind::FileTooLarge)
        );
        fs.write_file(&plan_path, b"second").await.unwrap();
        assert_eq!(std::fs::read(&plan_path).unwrap(), b"second");
        assert!(
            fs.write_file(&canonical_dir(&temp).join("other"), b"x")
                .await
                .is_err()
        );
        assert!(fs.delete_file(&plan_path).await.is_err());
    }

    #[tokio::test]
    async fn plan_capability_rejects_symlink_and_late_leaf_swap() {
        let temp = dir();
        let root = canonical_dir(&temp);
        let plan_path = root.join("plan.md");
        let target = root.join("workspace-file");
        std::fs::write(&target, b"protected").unwrap();
        symlink(&target, &plan_path).unwrap();
        assert!(LocalFs.open_plan_file(&plan_path).await.is_err());
        std::fs::remove_file(&plan_path).unwrap();
        symlink(root.join("missing"), &plan_path).unwrap();
        assert!(LocalFs.open_plan_file(&plan_path).await.is_err());
        std::fs::remove_file(&plan_path).unwrap();
        std::fs::write(&plan_path, b"old plan").unwrap();
        let plan = LocalFs.open_plan_file(&plan_path).await.unwrap();
        std::fs::remove_file(&plan_path).unwrap();
        symlink(&target, &plan_path).unwrap();
        assert!(
            plan.filesystem()
                .write_file(&plan_path, b"bad")
                .await
                .is_err()
        );
        assert_eq!(std::fs::read(target).unwrap(), b"protected");
    }

    #[tokio::test]
    async fn plan_capability_rejects_hardlinks_and_parent_links() {
        let temp = dir();
        let root = canonical_dir(&temp);
        let target = root.join("workspace-file");
        let plan_path = root.join("plan.md");
        std::fs::write(&target, b"protected").unwrap();
        std::fs::hard_link(&target, &plan_path).unwrap();
        assert!(LocalFs.open_plan_file(&plan_path).await.is_err());
        std::fs::remove_file(&plan_path).unwrap();
        let plan = LocalFs.open_plan_file(&plan_path).await.unwrap();
        std::fs::hard_link(&target, &plan_path).unwrap();
        assert!(
            plan.filesystem()
                .write_file(&plan_path, b"bad")
                .await
                .is_err()
        );
        assert_eq!(std::fs::read(&target).unwrap(), b"protected");
        let alias = root.join("alias");
        symlink(&root, &alias).unwrap();
        assert!(
            LocalFs
                .open_plan_file(&alias.join("plan.md"))
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn plan_capability_pins_parent_across_replacement() {
        let temp = dir();
        let root = canonical_dir(&temp);
        let session = root.join("session");
        let old_session = root.join("old-session");
        let outside = root.join("outside");
        std::fs::create_dir(&session).unwrap();
        std::fs::create_dir(&outside).unwrap();
        let path = session.join("plan.md");
        std::fs::write(outside.join("plan.md"), b"protected").unwrap();
        let plan = LocalFs.open_plan_file(&path).await.unwrap();
        std::fs::rename(&session, &old_session).unwrap();
        symlink(&outside, &session).unwrap();
        plan.filesystem().write_file(&path, b"plan").await.unwrap();
        assert_eq!(std::fs::read(old_session.join("plan.md")).unwrap(), b"plan");
        assert_eq!(
            std::fs::read(outside.join("plan.md")).unwrap(),
            b"protected"
        );
    }

    #[tokio::test]
    async fn trusted_symlinked_root_stays_pinned_when_swapped() {
        let temp = dir();
        let root = canonical_dir(&temp);
        let actual = root.join("home");
        let outside = root.join("outside");
        let alias = root.join("home-alias");
        for base in [&actual, &outside] {
            std::fs::create_dir_all(base.join("sessions/cwd/id")).unwrap();
        }
        let outside_plan = outside.join("sessions/cwd/id/plan.md");
        std::fs::write(&outside_plan, b"protected").unwrap();
        symlink(&actual, &alias).unwrap();
        let trusted = PlanFileRoot::capture(&alias).unwrap();
        let path = alias.join("sessions/cwd/id/plan.md");
        let first = LocalFs.open_plan_file_at(&path, &trusted).await.unwrap();
        first
            .filesystem()
            .write_file(&path, b"first")
            .await
            .unwrap();
        assert_eq!(
            std::fs::read(actual.join("sessions/cwd/id/plan.md")).unwrap(),
            b"first"
        );

        std::fs::remove_file(&alias).unwrap();
        symlink(&outside, &alias).unwrap();
        let after_swap = LocalFs.open_plan_file_at(&path, &trusted).await.unwrap();
        after_swap
            .filesystem()
            .write_file(&path, b"second")
            .await
            .unwrap();
        assert_eq!(
            std::fs::read(actual.join("sessions/cwd/id/plan.md")).unwrap(),
            b"second"
        );
        assert_eq!(std::fs::read(outside_plan).unwrap(), b"protected");
    }

    #[tokio::test]
    async fn trusted_root_rejects_a_symlink_in_session_descendants() {
        let temp = dir();
        let root = canonical_dir(&temp);
        let actual = root.join("home");
        let alias = root.join("home-alias");
        let outside = root.join("outside");
        std::fs::create_dir_all(actual.join("sessions/cwd")).unwrap();
        std::fs::create_dir(&outside).unwrap();
        symlink(&outside, actual.join("sessions/cwd/id")).unwrap();
        symlink(&actual, &alias).unwrap();
        let trusted = PlanFileRoot::capture(&alias).unwrap();
        assert!(
            LocalFs
                .open_plan_file_at(&alias.join("sessions/cwd/id/plan.md"), &trusted)
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn trusted_relative_root_remains_supported() {
        let temp = tempfile::tempdir_in(".").unwrap();
        // tempfile returns an absolute path even for a relative parent. The
        // directory is a direct child of cwd, so its name is the equivalent
        // relative spelling used for this test.
        let logical_root = PathBuf::from(temp.path().file_name().unwrap());
        assert!(!logical_root.is_absolute());
        std::fs::create_dir_all(logical_root.join("sessions/cwd/id")).unwrap();
        let root = PlanFileRoot::capture(&logical_root).unwrap();
        let path = logical_root.join("sessions/cwd/id/plan.md");
        let plan = LocalFs.open_plan_file_at(&path, &root).await.unwrap();
        plan.filesystem().write_file(&path, b"plan").await.unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), b"plan");
    }

    #[test]
    fn late_symlink_and_hardlink_swaps_do_not_change_the_target() {
        let temp = dir();
        let root = canonical_dir(&temp);
        let directory = open_directory(&root).unwrap();
        let plan_path = root.join("plan.md");
        let target = root.join("workspace-file");
        std::fs::write(&target, b"protected").unwrap();
        for hardlink in [false, true] {
            std::fs::write(&plan_path, b"old plan").unwrap();
            write_before_rename(directory.as_raw_fd(), b"new plan", || {
                std::fs::remove_file(&plan_path)?;
                if hardlink {
                    std::fs::hard_link(&target, &plan_path)
                } else {
                    symlink(&target, &plan_path)
                }
            })
            .unwrap();
            assert_eq!(std::fs::read(&target).unwrap(), b"protected");
            assert_eq!(std::fs::read(&plan_path).unwrap(), b"new plan");
        }
    }

    #[tokio::test]
    async fn unsupported_backend_fails_closed() {
        let backend = MockFs::new();
        let result = backend.open_plan_file(Path::new("/session/plan.md")).await;
        assert_eq!(
            result.unwrap_err().io_error_kind(),
            Some(io::ErrorKind::Unsupported)
        );
    }
}

//! Checked output for command-line messages, not terminal rendering.

use std::fmt;
use std::io::{self, Write};

/// A closed downstream pipe is a normal end to CLI output. All other write
/// errors remain errors so callers can return a failing exit status.
pub fn write_checked(mut writer: impl Write, args: fmt::Arguments<'_>) -> io::Result<()> {
    ignore_broken_pipe(writer.write_fmt(args).and_then(|()| writer.flush()))
}

pub fn ignore_broken_pipe(result: io::Result<()>) -> io::Result<()> {
    match result {
        Err(error) if error.kind() == io::ErrorKind::BrokenPipe => Ok(()),
        result => result,
    }
}

/// A writer for non-interactive CLI output that treats a closed pipe as a
/// successful sink while preserving every other I/O error.
pub struct PipeTolerant<W>(pub W);

impl<W: Write> Write for PipeTolerant<W> {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        match self.0.write(bytes) {
            Err(error) if error.kind() == io::ErrorKind::BrokenPipe => Ok(bytes.len()),
            result => result,
        }
    }

    fn flush(&mut self) -> io::Result<()> {
        ignore_broken_pipe(self.0.flush())
    }
}

/// Print a CLI message, stopping with an error if the output fails for a
/// reason other than a closed pipe. This is used where existing CLI helpers
/// cannot return an I/O result directly.
pub fn write_or_exit(writer: impl Write, args: fmt::Arguments<'_>) {
    if write_checked(writer, args).is_err() {
        std::process::exit(1);
    }
}

#[macro_export]
macro_rules! cli_print {
    ($($arg:tt)*) => {{
        $crate::cli_output::write_or_exit(std::io::stdout().lock(), format_args!($($arg)*));
    }};
}

#[macro_export]
macro_rules! cli_println {
    () => { $crate::cli_print!("\n") };
    ($($arg:tt)*) => {{
        $crate::cli_output::write_or_exit(std::io::stdout().lock(), format_args!("{}\n", format_args!($($arg)*)));
    }};
}

#[macro_export]
macro_rules! cli_eprint {
    ($($arg:tt)*) => {{
        $crate::cli_output::write_or_exit(std::io::stderr().lock(), format_args!($($arg)*));
    }};
}

#[macro_export]
macro_rules! cli_eprintln {
    () => { $crate::cli_eprint!("\n") };
    ($($arg:tt)*) => {{
        $crate::cli_output::write_or_exit(std::io::stderr().lock(), format_args!("{}\n", format_args!($($arg)*)));
    }};
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Failing(io::ErrorKind);
    impl Write for Failing {
        fn write(&mut self, _: &[u8]) -> io::Result<usize> {
            Err(io::Error::from(self.0))
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    struct FailingFlush(io::ErrorKind);
    impl Write for FailingFlush {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            Ok(bytes.len())
        }
        fn flush(&mut self) -> io::Result<()> {
            Err(io::Error::from(self.0))
        }
    }

    #[test]
    fn checked_cli_output_ignores_only_broken_pipe() {
        assert!(write_checked(Failing(io::ErrorKind::BrokenPipe), format_args!("x")).is_ok());
        assert_eq!(
            write_checked(Failing(io::ErrorKind::PermissionDenied), format_args!("x"))
                .unwrap_err()
                .kind(),
            io::ErrorKind::PermissionDenied
        );
        let mut bytes = Vec::new();
        write_checked(&mut bytes, format_args!("hello {}", 42)).unwrap();
        assert_eq!(bytes, b"hello 42");
        assert!(write_checked(FailingFlush(io::ErrorKind::BrokenPipe), format_args!("x")).is_ok());
        assert_eq!(
            write_checked(
                FailingFlush(io::ErrorKind::PermissionDenied),
                format_args!("x")
            )
            .unwrap_err()
            .kind(),
            io::ErrorKind::PermissionDenied
        );
    }

    #[test]
    fn pipe_tolerant_writer_preserves_other_errors() {
        let mut closed = PipeTolerant(Failing(io::ErrorKind::BrokenPipe));
        closed.write_all(b"output").unwrap();
        let mut denied = PipeTolerant(Failing(io::ErrorKind::PermissionDenied));
        assert_eq!(
            denied.write_all(b"output").unwrap_err().kind(),
            io::ErrorKind::PermissionDenied
        );
        assert!(
            PipeTolerant(FailingFlush(io::ErrorKind::BrokenPipe))
                .flush()
                .is_ok()
        );
        assert_eq!(
            PipeTolerant(FailingFlush(io::ErrorKind::PermissionDenied))
                .flush()
                .unwrap_err()
                .kind(),
            io::ErrorKind::PermissionDenied
        );
    }
}

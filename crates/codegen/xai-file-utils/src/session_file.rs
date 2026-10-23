//! Transparent reads of plain or gzip-encoded session artifacts.
//!
//! Encoding is detected from the opened file, not its name. Seek positions and
//! read limits refer to decoded bytes. Rewinds retain the same opened inode.

use std::fs::File;
use std::io::{self, BufReader, Read, Seek, SeekFrom};
use std::path::Path;

use flate2::read::MultiGzDecoder;

enum Source {
    Plain(File),
    Gzip(MultiGzDecoder<BufReader<File>>),
}

pub struct DecodedFile {
    source: Source,
    position: u64,
}

pub fn open(path: &Path) -> io::Result<DecodedFile> {
    from_file(File::open(path)?)
}

/// Decode the already-opened file, preserving its identity across path changes.
pub fn from_file(mut file: File) -> io::Result<DecodedFile> {
    file.rewind()?;
    let mut magic = [0; 2];
    let gzip = match file.read_exact(&mut magic) {
        Ok(()) => magic == [0x1f, 0x8b],
        Err(error) if error.kind() == io::ErrorKind::UnexpectedEof => false,
        Err(error) => return Err(error),
    };
    file.rewind()?;
    Ok(DecodedFile {
        source: if gzip {
            Source::Gzip(MultiGzDecoder::new(BufReader::new(file)))
        } else {
            Source::Plain(file)
        },
        position: 0,
    })
}

impl DecodedFile {
    pub fn is_compressed(&self) -> bool {
        matches!(self.source, Source::Gzip(_))
    }

    fn rewind_gzip(&mut self) -> io::Result<()> {
        if let Source::Gzip(decoder) = &mut self.source {
            let mut file = decoder.get_ref().get_ref().try_clone()?;
            file.rewind()?;
            *decoder = MultiGzDecoder::new(BufReader::new(file));
            self.position = 0;
        }
        Ok(())
    }
}

impl Read for DecodedFile {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        let count = match &mut self.source {
            Source::Plain(file) => file.read(buffer),
            Source::Gzip(decoder) => decoder.read(buffer),
        }?;
        self.position += count as u64;
        Ok(count)
    }
}

impl Seek for DecodedFile {
    fn seek(&mut self, from: SeekFrom) -> io::Result<u64> {
        if let Source::Plain(file) = &mut self.source {
            self.position = file.seek(from)?;
            return Ok(self.position);
        }
        let target = match from {
            SeekFrom::Start(position) => Some(position),
            SeekFrom::Current(delta) => self.position.checked_add_signed(delta),
            SeekFrom::End(delta) => {
                io::copy(self, &mut io::sink())?;
                self.position.checked_add_signed(delta)
            }
        }
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "invalid decoded seek"))?;
        if target < self.position {
            self.rewind_gzip()?;
        }
        let remaining = target - self.position;
        let copied = io::copy(&mut self.take(remaining), &mut io::sink())?;
        if copied != remaining {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "decoded seek exceeds session contents",
            ));
        }
        Ok(self.position)
    }

    fn stream_position(&mut self) -> io::Result<u64> {
        Ok(self.position)
    }
}

pub fn read(path: &Path) -> io::Result<Vec<u8>> {
    let mut bytes = Vec::new();
    open(path)?.read_to_end(&mut bytes)?;
    Ok(bytes)
}

pub fn read_to_string(path: &Path) -> io::Result<String> {
    let mut text = String::new();
    open(path)?.read_to_string(&mut text)?;
    Ok(text)
}

pub fn read_bounded(path: &Path, max_bytes: usize) -> io::Result<Vec<u8>> {
    let mut bytes = Vec::new();
    open(path)?
        .take((max_bytes as u64).saturating_add(1))
        .read_to_end(&mut bytes)?;
    if bytes.len() > max_bytes {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "decoded session exceeds byte limit",
        ));
    }
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn gzip(bytes: &[u8]) -> Vec<u8> {
        let mut writer = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::fast());
        writer.write_all(bytes).unwrap();
        writer.finish().unwrap()
    }

    #[test]
    fn plain_gzip_and_concatenated_members_have_identical_reads() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("updates.jsonl");
        for bytes in [
            b"alpha\nbeta\n".to_vec(),
            gzip(b"alpha\nbeta\n"),
            [gzip(b"alpha\n"), gzip(b"beta\n")].concat(),
        ] {
            std::fs::write(&path, bytes).unwrap();
            assert_eq!(read_to_string(&path).unwrap(), "alpha\nbeta\n");
            assert_eq!(read_bounded(&path, 11).unwrap(), b"alpha\nbeta\n");
            assert!(read_bounded(&path, 10).is_err());
        }
    }

    #[test]
    fn gzip_seek_and_rewind_keep_original_inode_after_replacement() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("updates.jsonl");
        std::fs::write(&path, gzip(b"alpha\nbeta\n")).unwrap();
        let mut reader = open(&path).unwrap();
        assert_eq!(reader.seek(SeekFrom::Start(6)).unwrap(), 6);
        let replacement = dir.path().join("replacement");
        std::fs::write(&replacement, gzip(b"wrong\n")).unwrap();
        std::fs::rename(replacement, &path).unwrap();
        reader.rewind().unwrap();
        let mut text = String::new();
        reader.read_to_string(&mut text).unwrap();
        assert_eq!(text, "alpha\nbeta\n");
        assert_eq!(reader.seek(SeekFrom::End(-5)).unwrap(), 6);
        assert_eq!(reader.stream_position().unwrap(), 6);
        assert!(reader.seek(SeekFrom::Current(-7)).is_err());
    }

    #[test]
    fn truncated_or_corrupt_gzip_is_an_error_and_limits_are_decoded() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("chat_history.jsonl");
        let bytes = gzip(&vec![b'a'; 200_000]);
        std::fs::write(&path, &bytes).unwrap();
        assert!(read_bounded(&path, 1024).is_err());
        std::fs::write(&path, &bytes[..bytes.len() - 1]).unwrap();
        assert!(read(&path).is_err());
        let mut corrupt = bytes;
        let at = corrupt.len() - 8;
        corrupt[at] ^= 1;
        std::fs::write(&path, corrupt).unwrap();
        assert!(read(&path).is_err());
    }
}

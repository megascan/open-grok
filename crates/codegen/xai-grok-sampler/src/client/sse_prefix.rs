//! Remove an initial UTF-8 BOM before handing bytes to eventsource-stream.
//!
//! eventsource-stream 0.2.3 slices a BOM at byte 1, which panics because the
//! character occupies three bytes. The prefix can span transport chunks.

use futures_util::StreamExt;
use futures_util::stream::Stream;

const UTF8_BOM: [u8; 3] = [0xEF, 0xBB, 0xBF];

/// Retain the original transport chunk except for the at-most-two bytes that
/// must be held while deciding whether the stream begins with a BOM.
enum PrefixChunk<B> {
    Original { bytes: B, offset: usize },
    Buffered { bytes: [u8; 3], len: usize },
    Preamble,
}

impl<B: AsRef<[u8]>> AsRef<[u8]> for PrefixChunk<B> {
    fn as_ref(&self) -> &[u8] {
        match self {
            Self::Original { bytes, offset } => &bytes.as_ref()[*offset..],
            Self::Buffered { bytes, len } => &bytes[..*len],
            Self::Preamble => b":\n",
        }
    }
}

/// Strip exactly one initial BOM, without copying ordinary transport chunks.
/// A mismatching or truncated prefix is passed through unchanged. After a BOM,
/// an inert SSE comment starts the decoder before any second BOM, which must be
/// treated as content rather than triggering its broken initial-BOM handling.
pub(super) fn strip_utf8_bom<S, B, E>(stream: S) -> impl Stream<Item = Result<impl AsRef<[u8]>, E>>
where
    S: Stream<Item = Result<B, E>>,
    B: AsRef<[u8]>,
{
    async_stream::stream! {
        futures_util::pin_mut!(stream);
        let mut prefix = [0u8; 3];
        let mut prefix_len = 0;
        let mut decided = false;

        while let Some(chunk) = stream.next().await {
            let bytes = match chunk {
                Ok(bytes) => bytes,
                Err(error) => {
                    // Preserve byte/error order even if the source resumes after
                    // an error. There can no longer be a continuous initial BOM.
                    if !decided {
                        // The source can be polled after an error. A resumed
                        // stream could begin with or complete a BOM, so start
                        // the SSE decoder before forwarding any pending bytes.
                        yield Ok(PrefixChunk::Preamble);
                    }
                    if prefix_len != 0 {
                        yield Ok(PrefixChunk::Buffered { bytes: prefix, len: prefix_len });
                        prefix_len = 0;
                    }
                    decided = true;
                    yield Err(error);
                    continue;
                }
            };

            if decided || bytes.as_ref().is_empty() {
                yield Ok(PrefixChunk::Original { bytes, offset: 0 });
                continue;
            }

            let data = bytes.as_ref();
            let mut matched = 0;
            while prefix_len < UTF8_BOM.len()
                && matched < data.len()
                && data[matched] == UTF8_BOM[prefix_len]
            {
                prefix[prefix_len] = data[matched];
                prefix_len += 1;
                matched += 1;
            }

            if prefix_len == UTF8_BOM.len() {
                decided = true;
                prefix_len = 0;
                yield Ok(PrefixChunk::Preamble);
                yield Ok(PrefixChunk::Original { bytes, offset: matched });
            } else if matched < data.len() {
                decided = true;
                if prefix_len != 0 {
                    yield Ok(PrefixChunk::Buffered { bytes: prefix, len: prefix_len });
                    prefix_len = 0;
                }
                yield Ok(PrefixChunk::Original { bytes, offset: matched });
            }
        }

        if prefix_len != 0 {
            yield Ok(PrefixChunk::Buffered { bytes: prefix, len: prefix_len });
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use eventsource_stream::Eventsource;
    use futures_util::stream;

    async fn strip(
        chunks: Vec<Result<Vec<u8>, &'static str>>,
    ) -> Vec<Result<Vec<u8>, &'static str>> {
        strip_utf8_bom(stream::iter(chunks))
            .map(|result| result.map(|chunk| chunk.as_ref().to_vec()))
            .collect()
            .await
    }

    #[tokio::test]
    async fn strips_split_bom_before_eventsource_decoding() {
        let frame = b"data: {\"ok\":true}\n\n";
        let cases = [
            vec![[vec![0xEF, 0xBB, 0xBF], frame.to_vec()].concat()],
            vec![vec![0xEF, 0xBB, 0xBF], frame.to_vec()],
            vec![vec![0xEF], [vec![0xBB, 0xBF], frame.to_vec()].concat()],
            vec![vec![0xEF, 0xBB], [vec![0xBF], frame.to_vec()].concat()],
            vec![vec![0xEF], vec![0xBB], vec![0xBF], frame.to_vec()],
            vec![vec![], vec![0xEF], vec![], vec![0xBB, 0xBF], frame.to_vec()],
            vec![frame.to_vec()],
        ];

        for chunks in cases {
            let source = stream::iter(chunks.into_iter().map(Ok::<_, &'static str>));
            let events = strip_utf8_bom(source)
                .eventsource()
                .collect::<Vec<_>>()
                .await;
            assert_eq!(events.len(), 1);
            assert_eq!(events[0].as_ref().unwrap().data, "{\"ok\":true}");
        }
    }

    #[tokio::test]
    async fn second_bom_reaches_decoder_as_content_without_panicking() {
        // Only the first BOM is a signature. The second is part of the first
        // field name, so that field is ignored and the later data event survives.
        let source = stream::iter(vec![
            Ok::<_, &'static str>(vec![0xEF]),
            Ok(vec![0xBB, 0xBF, 0xEF]),
            Ok([
                vec![0xBB, 0xBF],
                b"data: ignored\n\ndata: intact\n\n".to_vec(),
            ]
            .concat()),
        ]);
        let events = strip_utf8_bom(source)
            .eventsource()
            .collect::<Vec<_>>()
            .await;
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].as_ref().unwrap().data, "intact");
    }

    #[tokio::test]
    async fn preserves_non_bom_prefixes_and_only_strips_one_bom() {
        for chunks in [
            vec![vec![0xEF], vec![0xBB], vec![0xBE, b'x']],
            vec![vec![0xEF], vec![b'x']],
            vec![vec![0xEF]],
            vec![vec![0xEF, 0xBB]],
            vec![vec![], b"data: x\n\n".to_vec()],
        ] {
            let expected = chunks.concat();
            let output = strip(chunks.into_iter().map(Ok).collect()).await;
            let actual: Vec<u8> = output.into_iter().flat_map(Result::unwrap).collect();
            assert_eq!(actual, expected);
        }

        let output = strip(vec![
            Ok(vec![0xEF]),
            Ok(vec![0xBB, 0xBF, 0xEF, 0xBB, 0xBF, b'x']),
        ])
        .await;
        let actual: Vec<u8> = output.into_iter().flat_map(Result::unwrap).collect();
        assert_eq!(actual, vec![b':', b'\n', 0xEF, 0xBB, 0xBF, b'x']);
    }

    #[tokio::test]
    async fn incomplete_utf8_and_transport_errors_remain_decoder_errors() {
        let truncated = stream::iter(vec![Ok::<_, &'static str>(vec![0xEF]), Ok(vec![0xBB])]);
        let events = strip_utf8_bom(truncated)
            .eventsource()
            .collect::<Vec<_>>()
            .await;
        assert_eq!(events.len(), 1);
        assert!(events[0].is_err());

        let interrupted = stream::iter(vec![Ok(vec![0xEF]), Err("transport error")]);
        let events = strip_utf8_bom(interrupted)
            .eventsource()
            .collect::<Vec<_>>()
            .await;
        assert!(events[0].is_err());
    }

    #[tokio::test]
    async fn decoder_can_be_polled_after_error_during_bom_prefix() {
        let cases = [
            vec![
                Ok(vec![0xEF]),
                Err("transport error"),
                Ok([
                    vec![0xBB, 0xBF],
                    b"data: ignored\n\ndata: intact\n\n".to_vec(),
                ]
                .concat()),
            ],
            vec![
                Err("transport error"),
                Ok([
                    vec![0xEF, 0xBB, 0xBF],
                    b"data: ignored\n\ndata: intact\n\n".to_vec(),
                ]
                .concat()),
            ],
        ];
        for chunks in cases {
            let events = strip_utf8_bom(stream::iter(chunks))
                .eventsource()
                .collect::<Vec<_>>()
                .await;
            assert_eq!(events.len(), 2);
            assert!(events[0].is_err());
            assert_eq!(events[1].as_ref().unwrap().data, "intact");
        }
    }

    #[tokio::test]
    async fn preserves_prefix_before_error_and_does_not_resume_bom_detection() {
        let output = strip(vec![
            Ok(vec![0xEF]),
            Err("transport error"),
            Ok(vec![0xBB, 0xBF, b'x']),
        ])
        .await;
        assert_eq!(
            output,
            vec![
                Ok(b":\n".to_vec()),
                Ok(vec![0xEF]),
                Err("transport error"),
                Ok(vec![0xBB, 0xBF, b'x'])
            ]
        );
    }
}

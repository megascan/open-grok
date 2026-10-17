# Thread gzip storage verification — 2026-10-17

Baseline: `a6c052a2638e6cb139120fef6e968a99664d3ec6`.
Implementation: `a61396b2` (shared reader) and `00f8db2a` (storage and consumers).

## Behavior

Large `chat_history.jsonl` and `updates.jsonl` files can contain gzip bytes under
their existing names. Readers detect the encoding from the opened file. Whole
snapshots compress at 64 KiB when gzip is smaller; append histories compress
after 30 seconds without persistence messages and on persistence channel close.
Appends first atomically restore plain JSONL under the same lock used for
compression and snapshot replacement. Metadata, checkpoints, and other logs
remain in their existing formats. Existing plain histories need no migration.

Full reads validate gzip trailers. Limits and replay offsets count decoded
bytes. Corrupt compressed histories reject append and export without replacing
the original bytes. Preview consumers may read only a prefix; they do not claim
to validate an unread gzip trailer.

## Evidence

The focused tests use synthetic fixtures in temporary directories and an
isolated `OPENGROK_HOME` under `/tmp/open-grok-thread-gzip-20261017/test-home`.
No real user sessions were read or converted. The temporary session fixtures
are deleted by the tests; test logs remain under the verification directory.

| Path | Evidence |
| --- | --- |
| Shared reader | Plain, gzip, and concatenated members produce identical bytes; decoded size caps reject expansion; CRC and truncation errors propagate; rewind stays on the originally opened file after pathname replacement. |
| JSONL storage | Session `gzip-history`, cwd `/gzip-fixture`, has a valid plain `summary.json`; typed history reloads after compression, accepts a new message, recompresses, and rewrites to a small plain history. |
| Persistence actor | Sessions `gzip-close` and `gzip-idle` prove the actual channel-close and 30-second idle triggers; idle compression is followed by a successful new append. |
| Durability and concurrency | Durable append restores compressed bytes correctly; corrupt restore preserves the original; concurrent compression and append retain all 8,020 records; cwd-switch retries do not duplicate a committed generation. |
| Fork, rewind, replay | Full and point copies match plain fixtures; corrupted point-copy input fails; checkpoint/rewind replay and typed replay agree with plain histories; paging preserves decoded offsets. |
| Search and consumers | Search preserves text and decoded size bounds; initial token counting, orphan cleanup, parent-ask context, pager previews/statistics, and the downgrade utility read compressed histories. |
| Export | Parent and nested child histories export as readable JSONL; binary attachments remain byte-identical; malformed gzip fails the archive collector. |

The repetitive typed-history fixture stored 312,054 decoded bytes in 3,580 gzip
bytes (98.85% smaller). This is a synthetic correctness fixture, not a measured
average for user sessions.

## Validation

Validation uses `scripts/cargo-bounded.sh` with the existing 80 GiB target budget
at `/tmp/open-grok-sync-20261016/target`. Focused tests run individually with
`RUST_MIN_STACK=8388608` and isolated state to avoid shared-global interference.

| Check | Result |
| --- | --- |
| `xai-file-utils` shared reader tests | 3 passed |
| Shell storage, replay, search, persistence, archive, and new consumer cases | 480 distinct tests passed |
| Task-tool regression tests, including compressed parent context | 269 passed |
| Pager compressed preview/statistics test | 1 passed |
| `chat-history-downgrade` binary tests | 16 passed |
| `cargo check --locked -p xai-grok-shell -p xai-grok-pager-bin` | Passed; existing compiler warnings remain |
| Scoped rustfmt and `git diff --check` | Passed |

Total: 769 distinct passing tests. The initial orphan test fixture put a
completed task ID at the wrong JSON level; after adding `task_snapshot.task_id`,
the isolated retry passed for both plain and gzip inputs. The first test build
also exposed a missing `flate2` dev dependency in the tools crate, which was
added before the successful builds. No test failures remain.

The final independent source review accepted the writer locking, decoder error
handling, decoded bounds, pinned-file reads, consumer coverage, and archive error
propagation. The review report is retained with the local test logs.

## Compatibility and limits

- New readers support both existing plain files and gzip files. Older binaries
  without this reader cannot load gzip histories.
- Existing sessions are compressed lazily through writes, not by a startup
  sweep. Abrupt termination can leave valid plain files.
- Resuming an append-heavy compressed history requires one full decompression
  before its next append. Gzip seeks replay decoded bytes, so random access is
  not constant time.
- This verification covers filesystem storage and application consumers with
  hermetic tests. It does not claim a live-provider run or a published release.

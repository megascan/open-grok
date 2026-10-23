# Second improvement wave — 2026-10-17

Baseline: `b4d6225d062b5e47ea67e4f1f0682a78de8ca720`. This wave preserves
`OPEN_GROK_VERSION` and does not publish or install a release. Ten native audit
workers and five independent reviewers investigated bounded improvements. The
lead owns serialized Cargo validation, measurements, integration, and commits.

## Changes

| Area | Behavior |
| --- | --- |
| Streaming | A bounded prefix adapter handles split UTF-8 byte-order marks across all five SSE paths, including repeated BOMs and transport errors. |
| File tools | Hashline anchors and output use one file snapshot, with byte budgets applied to the formatted output. OpenCode truncation preserves UTF-8 boundaries and read traversal stops after confirming the remaining-line footer. |
| Scrollback | Cached range height uses prefix positions; sticky headers select the needed descriptors without allocating a full-history vector. Multiple dirty height updates keep prompt positions aligned with the prefix cache. |
| Session search | Prompt extraction reduces events incrementally. Bootstrap limits submitted, unjoined jobs to four instead of creating a task for every session. |
| Leader transport | An in-progress frame read survives outbound notifications, preserving partially consumed headers and bodies. |
| Mailboxes | An in-process delivery receipt retains messages until the chat actor appends the tool result. Abandoned delivery releases the batch for retry while preserving FIFO order and sender identity. |
| MCP | Children follow the parent's live owned client pool. Preparation and retry bind to client identity and catalog revision, preserving masks and child-owned overrides across changes. |
| Plugins | Ordinary Git updates stage a checkout and publish against the selected registry record, tree identity, and content fingerprint. Bulk selections retain their original expectations; partial ACP success still triggers reload while reporting failures. |

## Validation

Tests ran on macOS arm64 using Rust 1.94.0, an isolated `OPENGROK_HOME`,
the existing bounded target directory, and an 8 MiB Rust test thread stack.
Each focused direct invocation ran one exact test in a fresh process. Counts
are unique tests, including successful corrected reruns only once.

| Selection | Passed |
| --- | ---: |
| Sampler client and SSE prefix | 111 |
| Tools: read/hashline/grep and coordinator | 280 |
| Chat-state tool-result acceptance and adjacent cases | 18 |
| Pager geometry, sticky headers, and update routing | 155 |
| Shell leader, mailbox, MCP, plugin, and search cases | 200 |
| MCP servers and owned clients | 199 |
| Agent plugin install, update, registry, and refresh | 81 |
| Marketplace installer | 23 |
| **Total** | **1,067** |

Three additional opt-in synthetic measurements passed. During validation, a new
MCP test needed a missing import, a pager pin fixture needed a fresh tail prompt,
and marketplace update validation needed to run before registry lookup and use
normalized provenance. The affected tests passed after these corrections.

The Windows directory-identity implementation was extracted without behavioral
changes and compiled to metadata for `x86_64-pc-windows-gnu`. This checks the
function's Rust/API type usage, not full Windows package linking or runtime
behavior. No installed release was replaced.

Final `cargo check --locked -p xai-grok-shell -p xai-grok-pager-bin` passed,
including the changed dependency crates. Scoped rustfmt checks cover all 55
changed Rust files, and `git diff --check` passes. Compiler warnings remain. This is not
a full-workspace test run, live-provider verification, or platform release check.

Local evidence is under `/tmp/open-grok-improvement-wave2-20261017/`: final
package check `check-final-2.log`, test builds `test-final-build.log` and
`test-marketplace-fix-build.log`, `test-*-run.log`, per-test logs and JSON under
`runs/`, `test-pager-pin-rerun.log`, `validation-summary.json`,
`bench-pager.log`, `bench-session.log`, and `windows-identity-probe.log`.

## Synthetic measurements

These opt-in tests ran with the unoptimized test profile on macOS arm64. They
compare a retained reference calculation with the changed helper in the same
process; they do not measure whole-app latency or release-build performance.

| Fixture | Observed result |
| --- | --- |
| Range height, 10,000 entries, 1,000 iterations | Reference 48.78 ms; prefix helper 17.67 µs. Reference visits 10,000,000 entries; helper reads one layout and two prefix positions per iteration. |
| Range height, 100,000 entries, 1,000 iterations | Reference 494.99 ms; prefix helper 18.46 µs. |
| Sticky lookup, 10,000 prompts, 100 iterations | Reference 14.88–22.79 ms across three scroll positions; new helper 33.75–41.38 µs. Reference copies 1,000,000 descriptors; new helper copies at most two per iteration. |
| Sticky lookup, 100,000 prompts, 100 iterations | Reference 148.82–220.71 ms; new helper 44.46–48.83 µs. |
| Search extraction, 200,002 lines / 23,600,315 input bytes | Output parity passed; the prior event-vector shape occupied 10,485,760 capacity bytes. Repeated non-user events do not grow the new accumulator. Extraction took 570 ms; no baseline elapsed-time comparison is claimed. |

Both pager measurements and the session extraction measurement passed. The
hashline regression additionally verifies one actual text read per invocation.

## Boundaries and follow-up work

- Scrollback measurements are helper-level synthetic comparisons, not an
  end-to-end frame-rate claim. Sticky lookup can still scan a long sequence of
  non-sticky prompts. Session bootstrap retains its initial metadata list and
  cannot bound blocking work that has already detached after a timeout.
- Mailbox receipts protect in-process acceptance, not process-crash durability.
  A resumed native-v2 child cannot drain an older ordinary-tool mailbox backlog
  through its native `wait_agent` surface. The original messages remain retained.
- MCP calls admitted before invalidation may finish on the old client. Real
  stdio process cleanup deadlines and every inheritance/resource/file-backed
  tool combination are outside the exercised test matrix.
- Plugin updates with applied Git filter attributes are refused. Cooperating
  writers use the registry lock; an external writer can still race the last
  fingerprint check. Explicit deny ACLs can prevent backup removal after commit
  and leave journal recovery blocked. Deferred cleanup needs a separate design.
- Windows transaction identity requires usable nonzero 128-bit file IDs.
  Unsupported filesystems fail closed; older empty Windows journal identities
  cannot authorize recovery. Read-only cleanup is probed on the staging
  filesystem. Windows runtime behavior has not been exercised here.
- Atomic live-session forks and ordering classifier admission against queued
  credential changes remain separate protocol work. A disk flush or a local
  gate alone does not establish the needed cross-writer ordering.

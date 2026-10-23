# Native-agent reliability work — 2026-10-17

This wave starts at `544f146bf1768a5f085203dd721735bc926696d2` and keeps
`OPEN_GROK_VERSION` at `1.0.13-open-grok.90`. Nineteen native agents handled
bounded implementation and independent source-review assignments. The lead
serialized Cargo, integrated shared files, ran the tests, and committed the
tested units. This is local source validation, not a release or upstream import.

## Changes

| Area | Behavior added or corrected |
| --- | --- |
| Turn lifecycle | Cancellation and completion retain the exact turn's finalization lease through cleanup. A newly promoted prompt gets its rewind boundary before its task can run. Stale completion cannot clear a successor's state. |
| Agent authority | Initial child tasks, wakes, queued prompts, interjections, and replay retain model-authored provenance independently of prompt-ID spelling. Stopped agent mail waits for human reengagement. |
| Mailboxes | A closed receiver no longer consumes queued messages. Waking a completed child preserves the original sender and message ID. |
| Code Mode | Cells retain their owning turn's cancellation token, old cell IDs cannot alias a reset runtime, and nested hook context reaches the next sample once. |
| Permissions and edits | Effective post-hook patch targets receive per-path checks. Plan-mode edits carry a per-call capability, and dispatch rechecks plan mode after waiting. Unix plan writes use pinned directory descriptors and atomic replacement. Self moves and invalid UTF-8 content edits are refused; hashline writes emit attribution notifications. |
| Persistence | Fork and new-session creation reserve destinations exclusively. Historical forks and cross-compaction rewinds require exact retained model history. Partial file-rewind failures retain retry state. Historical copies omit future sidecars and preserve persisted plan restrictions. |
| Provider boundaries | Auxiliary requests validate their explicit route and credentials. Denying-provider work waits for durable export-boundary markers, including parent/child boundaries. Title and image helpers close boundaries when making requests, and classifiers refresh catalog credentials. |
| MCP | Connection generations own handshakes and recovery watchers. Catalog changes refresh definitions, disabled-tool state, and same-client child bridges, with a coalesced follow-up when changes arrive during refresh. |
| Terminal | Descendant cleanup keeps the process-group leader until escalation. Shell-state capture is cancellable and bounded. Fair pipe draining, cooperative repolling, and coalesced notifications keep sustained output responsive. |
| Plugins | Staged publication and removal use a journal and registry lock. Recovery verifies tree ownership; registry merging rejects stale entries. Bulk removal retains immutable expectations, and local refresh compares content. Persistence errors reach callers. |
| Settings and updater | Settings writes preserve raw environment expressions and version overrides, reject invalid typed sections, and update live caches only after publication. Linux arm64 selects its published update asset. |
| Pager | Deferred paste retains draft and session ownership, including initial session creation. Successful background reloads drain their own queues. Title mutations serialize per session and roll back to confirmed state. |

## Verification

Run on macOS arm64 with the pinned Rust toolchain, an isolated
`OPENGROK_HOME=/tmp/open-grok-native-improvement-20261017/test-home`, and the
existing bounded Cargo cache. Direct test binaries used the repository's
`RUST_MIN_STACK=8388608` setting. No installed release was replaced.

The selections include every newly added host-visible regression plus adjacent
tests for the changed behavior. Each direct invocation ran one exact test in a
fresh process. Counts below are unique tests, without counting initial Cargo
filters or successful reruns twice.

| Crate / selection | Passed |
| --- | ---: |
| Tools: terminal/shell state, coordinator, plan filesystem, edit tools | 217 |
| Shell: lifecycle, authority, providers, persistence, Code Mode, permissions, hooks, MCP, settings, plugin purge | 229 |
| Pager: new regressions plus paste, title rollback, and reconnect selections | 351 |
| Workspace: permission manager and gate preflight | 223 |
| Agent: plugin registry, install, and refresh | 71 |
| MCP: servers and liveness | 195 |
| Plugin marketplace: installer | 21 |
| Updater: platform selection | 2 |
| **Total** | **1,309** |

Five pre-existing ignored terminal fixtures remain skipped. One new relative-root
fixture initially assumed `tempdir_in(".")` returns a relative path; the fixture
was corrected and passed against a fresh binary. One direct Code Mode test
initially omitted Cargo's stack-size environment setting; it passed when rerun
with the configured 8 MiB stack. Neither result is counted as a passing initial
attempt.

Locked package checks passed for tools, workspace, MCP, agent, shell, pager-bin,
plugin-marketplace, and update, including their pager dependency. Scoped rustfmt
and `git diff --check` passed. Compiler warnings remain; this wave does not claim
a warning-free build, full-workspace tests, or live-provider verification.

Local logs and per-test JSON results are under
`/tmp/open-grok-native-improvement-20261017/`: `check-final.log`,
`test-*-build.log`, `xai-grok-*-results.json`, `test-shell-stack-rerun.log`,
`test-tools-relative-rerun.log`, and `test-shell-plugin-purge.log`.

## Explicit limits and follow-up work

This section records the limits at this wave's completion. The subsequent
[second improvement wave](improvement-wave-2-2026-10-17.md) adds mailbox receipts,
live MCP pool rebinding, and staged ordinary Git updates, with its own validation
and remaining limits.

- The protected plan-file backend is implemented for local Unix filesystems.
  Windows and unsupported/remote filesystems refuse this plan-edit capability.
  Linux and Windows runtime behavior were not exercised in this wave.
- Historical forks require an available exact model-history prefix. They can
  reject previously accepted lossy reconstructions. A live source's immediately
  preceding mode mutation can still race its on-disk snapshot; fully atomic live
  forks need an actor-owned snapshot protocol.
- Mailbox delivery checks channel acceptance, not recipient consumption. A turn
  dropped after a successful send but before polling can still lose that message;
  preventing this needs consumption acknowledgment and redelivery.
- Classifier requests admitted before a queued credential mutation completes can
  still use the earlier route. Requests after completed mutations reload it.
- Children observe catalog revisions and recovery on their shared MCP client.
  Replacing the parent's client Arc does not rebind already-running children.
- Legacy symlink plugin installs are refused during transactional uninstall.
  Ordinary in-place git pulls remain outside the staged replacement transaction.

# Harness reliability acceptance ledger through 1.0.62

This ledger covers the six harness-reliability slices in the 2026-10-17
ownership manifest. The implementations and audits below use the official
[1.0.46–1.0.52 changelog](https://x.ai/changelog/build) and the
[1.0.53](https://x.ai/cli/changelogs/1.0.53.external.md),
[1.0.54](https://x.ai/cli/changelogs/1.0.54.external.md),
[1.0.59](https://x.ai/cli/changelogs/1.0.59.external.md), and
[1.0.62](https://x.ai/cli/changelogs/1.0.62.external.md) notes. Upstream
1.0.46–1.0.62 source was not available for replay and is not claimed as
imported. Review began from clean commit `3c398095`.

The table distinguishes passing tests from broader behavior those tests do
not establish. The lead runs Cargo with an isolated `OPENGROK_HOME`; log names
below are relative to `/tmp/open-grok-harness-20261017`.

## Acceptance cases

| Slice | Behavior and evidence | Remaining limit |
| --- | --- | --- |
| Worktrees · 1.0.54 | Headless worktree resume validates and uses the returned cwd. Tests in [`headless_tests.rs`](../../crates/codegen/xai-grok-pager/src/headless_tests.rs) pass for valid and out-of-tree cwd responses; pager startup response tests pass as well. | A real fork and shell write in both headless and TUI remain unverified. |
| Worktrees · 1.0.50, 1.0.52 | The passing worktree library tests cover active-session and creator-process removal guards, and GC retains dirty or active trees even with force. See [`api.rs`](../../crates/codegen/xai-fast-worktree/src/api.rs). | Session/process observations are snapshots, not a cross-process exclusion lock; permissions can limit cwd visibility. |
| Worktrees · 1.0.50, 1.0.52, 1.0.59 | The passing managed-removal tests reject replaced worktrees and repository paths, bind source identity, and exercise jj/Grove compatibility. Pager removal-error status passes. | The removal-validation tests do not establish every platform-specific CLI path; Linux orphan-cleaner and mount paths were not executed on macOS. |
| Worktrees · 1.0.51 | Passing tests exercise corrupt-index failure in managed removal and a bounded status command that fails on timeout or error. | The timeout fixture exercises the status helper, not an end-to-end CLI removal. |
| Worktrees · snapshot completion | The passing `retained_dirty_snapshot_resumes_only_with_proven_identity` creates a dirty tracked file, snapshots it, verifies removal refusal, and resumes only with matching identity. Restore population failure and resume validation also pass. | Production completion and durable snapshot restoration with untracked files have not been proved end to end; eager cleanup cannot assume another session has not adopted a tree. |
| Hooks · 1.0.59 | Hook library, shell client-hook and prompt-gate tests pass for `continue: false`, including nested calls; `latched_hook_stop_cancels_real_turn_before_next_sample` passes. | Full external-client end-to-end coverage of every hook event remains unverified. |
| Hooks · 1.0.53 | Shell tests pass for direct and nested tool restrictions; the pager agent-construction test passes for interactive restrictions in [`acp/mod.rs`](../../crates/codegen/xai-grok-pager/src/acp/mod.rs). | A full interactive run remains unverified. |
| Hooks · 1.0.53 | Hooks library tests pass for unreadable disabled-hooks state and managed policy. | — |
| Background · 1.0.62 | Per-turn delivery and admission tracking, exact wake resolution, reparent ordering and cancellation are implemented. Passing bridge tests cover delayed and detached starts, reparent delivery and removed wakes. Bash/monitor and ACP adapter tests cover late starts, one Send Now notification, owner-scoped cleanup and child-runtime watcher transfer. A real headless process test passes for a gated background command and its blocked follow-up turn using a mock provider. | A failed or timed-out ACP kill is logged and its record retained; it is not returned as a cancellation error. |
| Background · 1.0.62 | [`headless.rs`](../../crates/codegen/xai-grok-pager/src/headless.rs) tracks timeout outcome and pending background work. Both the outcome unit test and a real process test asserting timeout error and nonzero exit pass. | Live-provider behavior remains unverified. |
| Background · 1.0.53 | `real_goal_turn_pauses_after_three_no_tool_continuations` passes through the real turn loop. Goal actor/tracker tests pass for streak reset and resume. | A real-loop test showing an actual tool dispatch resets the streak is not established. |
| Context · 1.0.52 | Mock-provider turn-loop test passes for explicit context overflow without metadata and retries after compaction. | Live provider and route-boundary verification remain untested. |
| Context · 1.0.52 | `repeated_provider_overflow_stops_before_a_fourth_compaction` passes in the real sampling/compaction loop; the guard helper covers reset after progress. | Real-loop reset and other preflight paths remain unverified. |
| Context · 1.0.52 | A mock-history compaction regression and delivered nested PostToolUse note preflight test pass. | Full hook-dispatch-to-compaction behavior is not covered by one integrated regression. |
| Context · 1.0.50 | Chat Completions decoder and mock-wire tests pass for missing `finish_reason`, retry, and the explicit retry-only-before-output policy. | Streamed partial text may already be visible before retry; canonical-response assertions do not prove UI rollback. |
| Tools · 1.0.53 | Passing search-replace tests reject overlapping exact and normalized matches, including replace-all. | — |
| Tools · 1.0.52 | Passing grep/list-dir tests distinguish empty results from timeout and failures, including partial match plus error. | Filesystem permission tests can skip assertions when the process can still read the directory. |
| Tools · 1.0.53 | Standard, concise and hashline read-file tests pass for real line counts and EOF. | — |
| Durability · 1.0.54 | Active-session library and shell tests pass for corrupt-registry backup and salvage; TUI and headless warning tests pass. | A complete client registration and warning flow remains unverified. |
| Durability · 1.0.54 | Pager queue regressions pass for exactly-once delivery after load and model-switch resolution in [`session/load.rs`](../../crates/codegen/xai-grok-pager/src/app/dispatch/session/load.rs). | A complete interactive startup run remains unverified. |
| Durability · 1.0.54 | Textarea library tests pass for byte-bounded undo and oversized deleted pastes. | — |
| Durability · 1.0.54 | The pager eviction and reconstruction test `evict_offscreen_render_caches_sweeps_far_entries_only` passes in [`scrollback/state/mod.rs`](../../crates/codegen/xai-grok-pager/src/scrollback/state/mod.rs). | Viewport geometry verification remains pending. |
| Durability · 1.0.53 | `concurrent_usage_turns_preserve_every_writer` passes and production persistence calls the transaction. | Cross-process and production end-to-end coverage are not established by the test. |

## Verification

The lead's focused runs below used `./scripts/cargo-bounded.sh`, the existing
bounded target cache and an isolated `OPENGROK_HOME`. Counts describe tests
selected, not all possible platforms or paths.

| Package and selection | Result | Log |
| --- | ---: | --- |
| `xai-grok-hooks --lib` | 325 passed | `hooks-tests-2.log` |
| `xai-grok-active-sessions --lib` | 8 passed | `registry-tests.log` |
| `xai-ratatui-textarea --lib` | 368 passed | `textarea-tests.log` |
| `xai-grok-sampler --lib stream::chat_completions::tests` | 21 passed | `sampler-chat.log` |
| `xai-grok-sampler --test test_actor chat_eof_` | 2 passed | `sampler-eof.log` |
| `xai-fast-worktree --features metadata --lib` | 432 passed | `fast-worktree-final-2.log` |
| `xai-grok-workspace` worktree filter | 66 passed | `workspace-worktrees-final-2.log` |
| `xai-grok-tools` search-replace, standard/concise/hashline read-file, list-dir, grep filters | 342 passed | `tools-*.log` |
| `xai-grok-tools --lib notification::handle::tests` | 15 passed | `final-tools-notification-handle_tests.log` |
| `xai-grok-tools --lib task::coordinator::tests` | 71 passed | `final-tools-task-coordinator_tests.log` |
| `xai-grok-tools --lib agent_swarm` | 32 passed | `final-tools-agent_swarm.log` |
| `xai-grok-tools --lib computer::local::terminal::tests -- --test-threads=1` | 76 passed, 5 ignored | `final-tools-computer-local-terminal-tests.log` |
| `xai-grok-tools --lib implementations::grok_build::monitor::` | 31 passed | `final-tools-implementations-grok_build-monitor-.log` |
| `xai-grok-tools --lib implementations::grok_build::bash::` | 190 passed | `final-tools-implementations-grok_build-bash-.log` |
| `xai-grok-shell --lib terminal::adapter::tests` | 23 passed | `final-shell-terminal-adapter.log` |
| `xai-grok-shell --lib tools::notification_bridge::tests` | 48 passed | `final-shell-notification_bridge_tests.log` |
| `xai-grok-shell --lib agent::subagent::tests::rest` | 116 passed | `final-shell-agent-subagent-tests-rest.log` |
| `xai-grok-shell --lib goal_planner_e2e_tests` | 30 passed | `final-shell-goal_planner_e2e_tests.log` |
| `xai-grok-shell --lib goal_tracker` | 129 passed | `final-shell-goal_tracker.log` |
| `xai-grok-shell --lib inline_auto_compact_flow_tests` | 63 passed | `final-shell-inline_auto_compact_flow_tests.log` |
| `xai-grok-shell --lib client_hooks_tests` | 35 passed | `final-shell-client_hooks_tests.log` |
| `xai-grok-shell --lib prompt_gate_tests` | 19 passed | `final-shell-prompt_gate_tests.log` |
| `xai-grok-shell --lib latched_hook_stop_cancels_real_turn_before_next_sample` | 1 passed | `final-shell-latched_hook_stop_cancels_real_turn_before_next_sample.log` |
| `xai-grok-shell --lib cancel_running_task_tests` | 25 passed | `final-shell-cancel_running_task_tests.log` |
| `xai-grok-shell --lib cleanup_worktree_on_failure` | 2 passed | `final-shell-cleanup_worktree_on_failure.log` |
| `xai-grok-pager --lib headless` | 185 passed | `final-pager-headless.log` |
| `xai-grok-pager --lib interactive_restrictions` | 1 passed | `final-pager-interactive_restrictions.log` |
| `xai-grok-pager --lib resume_queue_tests` | 2 passed | `final-pager-resume_queue_tests.log` |
| `xai-grok-pager --lib active_registry_warning_tests` | 2 passed | `final-pager-active_registry_warning_tests.log` |
| `xai-grok-pager --lib evict_offscreen_render_caches_sweeps_far_entries_only` | 1 passed | `final-pager-evict_offscreen_render_caches_sweeps_far_entries_only.log` |
| `xai-grok-pager --lib worktree_resume_response` | 2 passed | `final-pager-worktree_resume_response.log` |
| `xai-grok-pager --lib remove_errors_produce_failure_status` | 1 passed | `final-pager-remove_errors_produce_failure_status.log` |
| `xai-grok-pager --test pty_e2e_smoke headless_background_wait -- --ignored` | 2 passed | `headless-process.log` |

A combined shell/tools/pager/bin `cargo check` passed on the final production
source (`check-final.log`, 102.08 seconds); scoped rustfmt and `git diff --check`
also passed. The lead ran the compiled shell test binary directly
for the concurrent-usage and corrupt-usage regressions (one pass each;
`verification-direct-shell.json`) and `session::workflow::` (98 passes;
`shell-workflow-direct.log`). Runtime, hooks and shell-registry units were
committed as `f4064ec5`, `6f5d0940` and `3fd8c30c` after focused tests; pager
changes were committed as `4f61a528`. The fresh CLI build passed
(`build-cli.log`, 158.25 seconds); the binary reported version 1.0.0 and commit
`4f61a528`. Its 228,430-byte Bash completions passed `bash -n` with an isolated
home. The two process tests used this command (154.6 seconds including
compilation):

```sh
CARGO_BOUNDED_TARGET_DIR=/tmp/open-grok-sync-20261016/target \
OPENGROK_HOME=/tmp/open-grok-harness-20261017/test-home \
PAGER_BINARY=/tmp/open-grok-sync-20261016/target/debug/open-grok \
./scripts/cargo-bounded.sh test --locked -p xai-grok-pager \
  --test pty_e2e_smoke headless_background_wait -- --ignored
```

Earlier runs included a goal fixture missing `update_goal`, hook fixtures
missing a delivery scope, a parallel terminal startup timeout, and two filters
that selected zero tests. The corrected goal and hook suites, isolated and
serial terminal runs, and corrected nonempty filters passed as recorded above. A pager cache-test
compile error was corrected without changing its assertions. Zero-test runs are
not counted as coverage.

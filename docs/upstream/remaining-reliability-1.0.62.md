# Remaining reliability fixes through 1.0.62

This follow-up began at `fcc2190a` on 2026-10-17 after the
[harness reliability wave](harness-reliability-1.0.62.md). It implements the
ten remaining issues identified from the official
[1.0.53](https://x.ai/cli/changelogs/1.0.53.external.md),
[1.0.54](https://x.ai/cli/changelogs/1.0.54.external.md),
[1.0.59](https://x.ai/cli/changelogs/1.0.59.external.md),
[1.0.60](https://x.ai/cli/changelogs/1.0.60.external.md),
[1.0.61](https://x.ai/cli/changelogs/1.0.61.external.md), and
[1.0.62](https://x.ai/cli/changelogs/1.0.62.external.md) notes. These are
independent fixes; no unavailable upstream source is claimed as imported.

## Behaviors

| Area | Result |
| --- | --- |
| Closed CLI output | Checked writes tolerate a closed downstream pipe, preserve ordinary output failures, and avoid completion-generator panics. Required confirmation and authorization prompts fail if they cannot be displayed. |
| Early help/version | Top-level and subcommand help/version dispatch before state-directory setup. |
| Session discovery | Listings retain healthy sessions when another directory or individual relocation fails. Incomplete scans remain explicit; direct ID lookup, mutation, export, and title selection reject unresolved ambiguity. |
| Parallel MCP input | Elicitation requests retain individual response channels in FIFO order. Completion and cancellation target the matching request; overlapping cards preserve drafts and input ownership. |
| Fork approval | Initial, answered, and startup fork paths refuse a parent with outstanding interactions. A refused fork does not persist the dialog's preference. |
| Workflow stop/pause | Destructive selectors require an exact full ID or display name and retain ambiguity checks. |
| Shell recovery | Failed snapshot replay warns and runs the user command once with the original environment and cwd. Recovery preserves the process-group leader and avoids dispatch after cancellation. |
| Resume | Arguments filter the picker; a proven unique exact match opens directly. Search supports words in any order; CLI titles normalize separators, show dated suggestions, and report local title misses without remote sign-in. |
| Sentry initialization | Invalid DSNs warn without revealing their value. Disabled, empty, and invalid configurations cannot re-enable reporting from an ambient DSN. |
| MCP home paths | Stdio programs and individual arguments expand bare `~` and leading `~/` without shell interpretation. |

## Verification

The lead serializes Cargo with `scripts/cargo-bounded.sh`, reusing
`/tmp/open-grok-sync-20261016/target` and an isolated `OPENGROK_HOME`.
Log names below are relative to `/tmp/open-grok-followups-20261017`.

| Selection | Result | Log |
| --- | --- | --- |
| Telemetry `sentry::tests` | 10 passed | `test-sentry.log` |
| MCP `plan_stdio_spawn` | 9 passed | `test-mcp-2.log` |
| TTY utilities `cli_output` | 2 passed | `test-cli-output-helper.log` |
| Tools `computer::local::shell_state::tests`, serial | 35 passed | `test-shell-state.log` |
| Shell workflow `run_match_tests` | 7 passed | `test-shell-workflow.log` |
| Shell newest-session, duplicate-ID, corrupt-summary and strict title discovery filters | 10 passed | `shell-results-rest.json` |
| Shell `session::unified_list::` | 49 passed | `shell-session-unified-list-.log` |
| Shell `session::merge::` | 48 passed | `shell-session-merge-.log` |
| Shell device-code/OIDC/Codex authentication tests | 29 passed | `shell-results-rest.json` |
| Shell `antigravity_acp` | 29 passed | `shell-antigravity-acp.log` |
| Shell relocation and fault recovery, serial | 25 passed | `test-relocation-final-3.log` |
| Pager ACP interaction handlers | 39 passed | `test-pager-interactions.log` |
| Pager fork dispatch | 62 passed | `pager-app-dispatch-tests-session-fork.log` |
| Pager interaction input and plan handling | 40 + 37 passed | `pager-results-additional.json` |
| Pager resume arguments, list pagination, title resolution, search and startup | 7 + 2 + 23 + 1 + 59 passed | `pager-results.json` |
| Pager session load and dashboard dispatch | 102 + 214 passed | `pager-results-additional.json` |
| Pager completion output failures | 1 passed | `pager-completions-output-handles.log` |
| Fresh CLI original failure reproductions and no-state checks | 9 passed | `cli-smoke-results.json` |
| Binary required Antigravity URL output | 1 passed | `test-bin-auth-output.log` |
| Mock-provider headless background and closed output processes | 4 passed | `test-headless-pipes.log` |
| Real-process CLI help/version, closed pipes and ordinary write failures | 4 passed | `test-cli-process-final.log` |
| Locked package check: tools, shell, pager, pager binary | Passed | `check-final.log` |
| Scoped rustfmt on all 77 changed Rust files | Passed | `format-final.log` |
| Fresh Bash completion generation and `bash -n` | Passed | `completions.bash` |

All selections above passed on the final source. The package check retains
compiler warnings; this is not a full-workspace test or live-provider run.
The first integrated check found
ambiguous CLI macro imports; these were corrected before the final rerun.
The first MCP test run found a trailing separator on bare `~`; the corrected
rerun is recorded above.
The relocation finalization fault fixture initially injected its error during
recovery instead of after journal removal; it was corrected to exercise the
intended boundary.
The CLI ordinary-error fixture initially used a read-only descriptor, but Rust
standard streams suppress `EBADF`. A full nonblocking pipe now verifies
`WouldBlock` errors using a valid stream instead.

The shared build cache finished at 41,404,571,648 allocated bytes, below the
80 GiB guard budget. No installed release was replaced. The seven delegated
threads completed implementation and follow-up review and were archived after
their relevant verification passed.

## Limits

- Direct title selection requires a complete single response. Old servers,
  partial scans, more than 1,000 rows, and active remote registries without
  an exhaustion contract retain the filtered picker. Independent live pages
  cannot establish uniqueness because rows can move between requests.
- Title matching lowercases Unicode alphanumeric words; it does not perform
  full Unicode case folding.
- Shell wrapper probes passed on macOS, Ubuntu 22.04 (GNU coreutils 8.32,
  Linux amd64 emulation), and Alpine (BusyBox 1.37, Linux arm64). The Rust
  suite ran on macOS. Recovery needs `/usr/bin/env -0` and `/bin/sh`; other
  Unix implementations remain unverified. Zsh recovery reads the system
  zshenv, and interrupted Bash replay can report exit 143 while preventing
  user-command dispatch.
- Authentication verification uses mock transports and injected output
  failures. An external authentication child can act or persist its own
  credentials before emitting a URL; parent-side failure cannot roll back
  those external effects. No live provider login was performed.
- Windows MCP path planning is unit-tested on macOS, not on a Windows host.
- CLI checks preserve errors surfaced by the writer; Rust standard streams'
  special handling of `EBADF` remains in effect.

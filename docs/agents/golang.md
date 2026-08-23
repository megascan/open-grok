# Go native Entropy

The shipping product binary is a **native Go** application (`cmd/entropy`).
It cross-compiles to Linux, macOS, Windows, and FreeBSD on amd64 and arm64
with `CGO_ENABLED=0`. The public command is `entropy` (`entropy.exe` on
Windows). User state lives under `$ENTROPY_HOME` or `~/.entropy` — never
`~/.grok`. Project config is `.entropy/` (`.opengrok/` is still scanned as a
compatibility fallback). The Go module path remains
`github.com/megascan/open-grok`.

Rust crates under `crates/` remain as the historical reference implementation.
New product work lands in Go.

## Layout

```text
cmd/entropy            composition root
bin/entropy-dev        go run ./cmd/entropy (bin/open-grok-dev is a wrapper)
internal/cli           Cobra CLI
internal/pager         Bubble Tea TUI (streaming transcript, tool cards, goal rail)
internal/agent         turn loop, sessions, tool-mode, compaction, subagents, goals
internal/goals         GoalTracker + session_dir/goal/orchestration.json
internal/acp           ACP stdio, WebSocket serve, leader socket
internal/codemode      persistent goja JS session (exec/wait + tools.*)
internal/sampler       Chat Completions / Responses / Messages adapters
internal/catalog       curated models + live /models
internal/auth          provider-isolated auth.json / codex-auth.json
internal/tools         bash, files, apply_patch, grep, glob, web, plan, skills, memory, task, update_goal, session bus, flat-team mailbox
internal/workspace     plan-mode edit gate + permission manager
internal/hooks         PreToolUse fail-open
internal/config        ENTROPY_HOME / ~/.entropy (never ~/.grok; OPENGROK_HOME is an alias)
internal/mcp           MCP JSON-RPC client (stdio + HTTP) + tool wrapping
internal/sessionbus    machine-local presence + IPC (list/read/message)
internal/oauth         ChatGPT Codex browser PKCE + device-code
internal/update        GitHub release check and install into ENTROPY_HOME/bin
internal/verify        module-by-module inventory tests for public functions
internal/version       product version (Entropy / entropy)
```

## Invariants preserved

1. `$ENTROPY_HOME` / `~/.entropy` only (never `~/.grok`). `$OPENGROK_HOME` is accepted as an alias.
2. Provider identity comes from model metadata, never a slug or URL.
3. API backend ≠ credentials.
4. Plan-mode edits are gated in the tool path before permissions. YOLO does not bypass that gate.
5. Agent file writes call `recordAgentWrite` (hunks are not External).
6. Codex credentials live in `codex-auth.json`; other API keys use scoped `auth.json`.
7. Subagents inherit permissions/YOLO and get a fresh Inactive plan tracker. Max depth 1.
8. Codex `code_mode_only` on Responses wins over Settings; non-Responses fails closed to Direct.
9. Code Mode `exec`/`wait` are transport. Nested `tools.*` calls are what users should see.
10. Hooks fail open.
11. Session bus is root-session only; peer bodies are untrusted model input.
12. Codex OAuth tokens live in `codex-auth.json` in the Codex-rs shape (`tokens` / `OPENAI_API_KEY`).
13. Goals: `/goal` starts a persisted tracker; `update_goal` is always registered and rejects with `goal_harness_disabled` outside goal mode. `completed:true` is a claim — the user confirms with `/goal complete`. Unknown wire statuses restore as `user_paused`. Active goals pause across restart.

## TUI (Go pager)

The native TUI is a Charmbracelet Bubble Tea app (`internal/pager`), not a port of the Rust pager's Elm dispatch loop. It is the interactive product surface:

- Full-screen chrome with a live streaming transcript (token deltas via `tea.Program.Send`)
- Tool cards; `update_goal` uses a distinct goal card
- Right-hand **Goals** rail (wide terminals) showing status, objective, progress, and history
- Slash palette (`/goal`, `/plan`, `/model`, …) with prefix hints in the footer
- Status badges for YOLO, plan mode, and goal lifecycle

`/goal <objective> [--budget <tokens>]` creates the goal and starts a turn. `/goal pause|resume|complete|clear|status` match the user-guide verbs. There is no host-owned classifier/strategist stack in Go; completion is user-verified.

## Packages used (not reimplemented)

| Concern | Package |
| --- | --- |
| CLI | `github.com/spf13/cobra` |
| TUI | `github.com/charmbracelet/bubbletea`, `bubbles`, `lipgloss` |
| Code Mode JS | `github.com/dop251/goja` (pure Go; no CGO/V8) |
| WebSocket | `github.com/gorilla/websocket` |
| TOML | `github.com/pelletier/go-toml/v2` |
| UUID | `github.com/google/uuid` |
| Blake3 (long CWD names) | `github.com/zeebo/blake3` |
| Terminal | `golang.org/x/term` |
| HTTP / JSON / SSE | Go standard library |

## Build

```sh
go test ./...
go test ./internal/verify -count=1   # every package's public functions exist and run
go build -o entropy ./cmd/entropy
./bin/entropy-dev --version
bash scripts/build-go-release.sh   # all GOOS/GOARCH into dist/entropy-*
```

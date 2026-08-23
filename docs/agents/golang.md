# Go native Open Grok

The product binary is a **native Go** application (`cmd/open-grok`). It cross-compiles
to Linux, macOS, Windows, and FreeBSD on amd64 and arm64 with `CGO_ENABLED=0`.

Rust crates under `crates/` remain as the historical reference implementation.
New product work lands in Go.

## Layout

```text
cmd/open-grok          composition root
internal/cli           Cobra CLI
internal/pager         Bubble Tea TUI
internal/agent         turn loop, sessions, tool-mode, compaction, subagents
internal/acp           ACP stdio, WebSocket serve, leader socket
internal/codemode      persistent goja JS session (exec/wait + tools.*)
internal/sampler       Chat Completions / Responses / Messages adapters
internal/catalog       curated models + live /models
internal/auth          provider-isolated auth.json / codex-auth.json
internal/tools         bash, files, apply_patch, grep, glob, web, plan, skills, memory, task, session bus, flat-team mailbox
internal/workspace     plan-mode edit gate + permission manager
internal/hooks         PreToolUse fail-open
internal/config        OPENGROK_HOME / ~/.opengrok (never ~/.grok)
internal/mcp           MCP JSON-RPC client (stdio + HTTP) + tool wrapping
internal/sessionbus    machine-local presence + IPC (list/read/message)
internal/oauth         ChatGPT Codex browser PKCE + device-code
internal/update        GitHub release check and install into OPENGROK_HOME/bin
```

## Invariants preserved

1. `$OPENGROK_HOME` / `~/.opengrok` only.
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
go build -o open-grok ./cmd/open-grok
./bin/open-grok-dev --version
bash scripts/build-go-release.sh   # all GOOS/GOARCH into dist/
```

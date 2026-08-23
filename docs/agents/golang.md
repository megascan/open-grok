# Go native Open Grok

The product binary is a **native Go** application (`cmd/open-grok`). It cross-compiles
to Linux, macOS, Windows, and FreeBSD on amd64 and arm64 with `CGO_ENABLED=0`.

Rust crates under `crates/` remain as the historical reference implementation.
New product work lands in Go.

## Layout

```text
cmd/open-grok          composition root
internal/cli           Cobra CLI (login, models, agent stdio, …)
internal/pager         Bubble Tea TUI (Charmbracelet)
internal/agent         turn loop, sessions, AGENTS.md load
internal/acp           JSON-RPC ACP over stdio
internal/sampler       Chat Completions / Responses / Messages adapters
internal/catalog       curated models + live /models
internal/auth          provider-isolated auth.json / codex-auth.json
internal/tools         bash, files, grep, glob, web, plan
internal/workspace     plan-mode edit gate + permission manager
internal/config        OPENGROK_HOME / ~/.opengrok (never ~/.grok)
internal/mcp           MCP stdio server list from config.toml
internal/update        GitHub release check
```

## Invariants preserved

1. `$OPENGROK_HOME` / `~/.opengrok` only.
2. Provider identity comes from model metadata, never a slug or URL.
3. API backend ≠ credentials.
4. Plan-mode edits are gated in the tool path before permissions. YOLO does not bypass that gate.
5. Agent file writes call `recordAgentWrite` (hunks are not External).
6. Codex credentials live in `codex-auth.json`; other API keys use scoped `auth.json`.
7. Subagents get a fresh Inactive plan tracker.

## Packages used (not reimplemented)

| Concern | Package |
| --- | --- |
| CLI | `github.com/spf13/cobra` |
| TUI | `github.com/charmbracelet/bubbletea`, `bubbles`, `lipgloss` |
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

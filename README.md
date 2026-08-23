<div align="center">

# Entropy (`entropy`)

**Grok Build with ChatGPT Codex optimizations — native Go**

[Install](#install) · [Updates](#updates) · [Sign in](#sign-in-and-use-both-providers) ·
[Code Mode](#codex-code-mode) · [Build from source](#build-from-source) ·
[Provenance](#upstream-provenance-and-license)

</div>

Open Grok is a community fork of [Grok Build](https://github.com/xai-org/grok-build),
the terminal coding agent published by SpaceXAI. It retains the Grok Build TUI,
headless agent, tools, and Agent Client Protocol support while adding an
OpenAI Codex provider path and Codex-compatible execution behavior.

The public command is `entropy`. It can be installed beside the upstream
`grok` command without replacing it. The GitHub module path remains
`github.com/megascan/open-grok`.

The shipping application is a **native Go** binary (`cmd/entropy`) that
cross-compiles to Linux, macOS, Windows, and FreeBSD on amd64 and arm64.
User state stays under `$ENTROPY_HOME` or `~/.entropy`. Rust crates under
`crates/` remain as the historical reference implementation.

## What this fork adds

- **Codex Code Mode and Code Mode Only.** Open Grok includes the persistent
  JavaScript `exec`/`wait` runtime used by Codex. Ordinary tools remain available
  through the generated `tools.*` namespace; direct-only interaction and
  multi-agent controls stay top-level.
- **Live Codex model catalog.** When ChatGPT Codex is connected, Open Grok calls
  the same authenticated `/models?client_version=…` endpoint as codex-rs. Live
  model names, context windows, reasoning levels, search support, and tool modes
  override the embedded Sol/Terra/Luna fallback catalog when OpenAI changes it.
- **Codex Max and Ultra parity.** Max and Ultra remain distinct in the TUI while
  both use Codex's `max` wire effort. For models whose live catalog metadata
  declares multi-agent v2, Ultra also enables codex-rs's proactive request
  policy.
- **Current Codex automatic compaction.** By default, Codex conversations use
  Remote Compaction V2 over the normal streaming `/responses` endpoint, retain
  the newest bounded real-user tail, and replay the opaque encrypted compaction
  item exactly. The legacy unary `/responses/compact` protocol remains an
  explicit compatibility option. Both paths follow the live model catalog's
  compaction limit and compatibility hash; xAI keeps Grok Build's compaction.
- **Codex response continuity.** A stable per-session prompt-cache key,
  turn-scoped `x-codex-turn-state`, durable `response.output_item.done` items,
  encrypted reasoning, and catalog-selected reasoning summaries are preserved
  across normal turns, tool loops, retries, compaction, and client rebuilds.
  Full-input HTTP turns deliberately do not send `previous_response_id`,
  matching codex-rs; codex-rs reserves ID-based prefix reuse for its validated
  WebSocket path.
- **ChatGPT Codex OAuth.** `entropy login --codex` uses the Codex OAuth flow and
  stores those credentials separately in `~/.entropy/codex-auth.json`.
- **xAI and Codex side by side.** xAI sign-in remains available, `/model` can
  switch an existing conversation between provider-backed models by rebuilding
  the correct harness in place, and `/usage` reports xAI billing and Codex quota
  windows independently.
- **Kimi Platform and Kimi Code.** Settings and `/login kimi` can select either
  Kimi service while keeping their endpoints, credentials, and model catalogs
  isolated. Platform supports live discovery; Code provides the membership
  coding models through its dedicated API.
- **Provider-aware hosted search.** xAI models receive xAI web/X search tools;
  Codex models receive OpenAI `web_search`. Open Grok does not pass one
  provider's credentials or provider-only tools to the other.
- **Optional Kimi web-search fallback.** Settings can enable Perplexity's raw
  Search API for Kimi Platform and Kimi Code while keeping the public
  `web_search(query, allowed_domains?)` schema. The isolated key lives only in
  owner-protected `auth.json`; xAI and Codex continue using native search.
- **One harness across providers.** Codex keeps the same subagent, scheduler,
  monitor, goal, plan, and user-question features as Grok while using Codex's
  file tools, prompt, transport, and model metadata.
- **Verified release updates.** Release builds check the public Open Grok
  GitHub release feed, verify version and SHA-256 before activation, and keep a
  canonical versioned binary under `ENTROPY_HOME`.

The implementation and compatibility contract are documented in
[`docs/code-mode-port.md`](docs/code-mode-port.md) and
[`docs/codex-provider-port.md`](docs/codex-provider-port.md). The extension
contract for adding providers, wire formats, and API-key or OAuth credentials
is in [`docs/provider-architecture.md`](docs/provider-architecture.md).

For AI coding agents and contributors working in this tree, start with
[`AGENTS.md`](AGENTS.md) and the deep dives under
[`docs/agents/`](docs/agents/) (architecture, agent runtime, file edits, TUI
and config, providers, and development workflow).

## Install

Prebuilt releases support **Apple Silicon macOS** (`arm64` / `aarch64`),
**Linux x86_64**, and **64-bit Windows** (`x86_64`). Intel macOS, Linux on
Arm, and Windows on Arm users should build from source for now.

Apple Silicon macOS or Linux x86_64:

```sh
curl -fsSL https://github.com/megascan/open-grok/releases/latest/download/install.sh | bash
entropy --version
```

64-bit Windows PowerShell:

```powershell
irm https://github.com/megascan/open-grok/releases/latest/download/install.ps1 | iex
entropy --version
```

Each installer downloads the platform artifact and its `.sha256` file, verifies
SHA-256, and activates `entropy` (`entropy.exe` on Windows). Go release assets
are named `entropy-<os>-<arch>`. The managed command lives under
`${ENTROPY_HOME:-$HOME/.entropy}/bin/` on macOS and Linux or `%ENTROPY_HOME%\bin\`
on Windows, which keeps manual installs and in-app updates on the same path.
It does not create `grok` or `agent` aliases. Historical Rust installers still
activate `open-grok` under `OPENGROK_HOME`.

By default, that managed `bin` directory is also added to `PATH`. Set
`OPEN_GROK_BIN_DIR` or `ENTROPY_BIN_DIR` to expose a symlink from another
absolute directory while keeping the canonical managed binary under
`ENTROPY_HOME`. When piping the installer, export the override first so it is
available to `bash`:

```sh
export OPEN_GROK_BIN_DIR="$HOME/.local/bin"
curl -fsSL https://github.com/megascan/open-grok/releases/latest/download/install.sh | bash
```

Install a specific version by passing it to the script (with or without a
leading `v`):

```sh
curl -fsSL https://github.com/megascan/open-grok/releases/latest/download/install.sh \
  | bash -s -- v0.1.220-open-grok.24
```

For local installer testing, `OPEN_GROK_RELEASE_BASE_URL` may point directly to
an asset base URL such as `http://127.0.0.1:8000` containing the platform
binary and its `.sha256` file. Windows automation may set
`OPEN_GROK_NO_PATH_UPDATE=1` to leave the persistent User `PATH` unchanged.

Release binaries are stripped and ad-hoc signed, but they are not Apple
Developer ID signed or notarized. macOS may show an unidentified-developer or
Gatekeeper warning. If your security policy requires notarization, build from
source and sign the result with your own identity.

## Updates

Release builds check for a newer full GitHub Release at launch. When automatic
updates are enabled, the TUI downloads and verifies the release in the
background, atomically advances `${ENTROPY_HOME:-$HOME/.entropy}/bin/entropy`,
and offers to restart onto it. The running process is never overwritten in
place.

Check or update explicitly:

```sh
entropy update --check
entropy update --check --json
entropy update
```

Automatic updates default on and are configurable in Settings or in
`$ENTROPY_HOME/config.toml`:

```toml
[cli]
auto_update = false
```

For one launch, pass `--no-auto-update`. For managed environments, set
`OPENGROK_DISABLE_AUTOUPDATER=1`; the legacy `GROK_DISABLE_AUTOUPDATER` name is
accepted only for compatibility. Explicit `entropy update` remains available
when background checks are disabled.

## Sign in and use both providers

Sign in to either provider, or both:

```sh
entropy login                         # xAI browser sign-in
entropy login --codex                 # ChatGPT Codex OAuth
entropy login --codex --device-auth   # headless/remote Codex flow
```

Then launch the TUI and select a model:

```sh
entropy
```

On first launch, the TUI offers ChatGPT Codex and xAI Grok as separate sign-in
choices. Inside the TUI, use `/model gpt-5.6-sol` for GPT-5.6 Sol, `/model` (or
the model picker) to switch providers, and `/usage` to view both providers'
available usage. The picker labels each model with its provider and live context
window. Bare `login`/`logout` continue to operate on xAI credentials; use
`logout --codex` for Codex or `logout --all` for both.

Codex credentials are isolated from the primary xAI credential store. A
Codex-selected session can run without xAI authentication, and a provider
failure in `/usage` does not hide the other provider's result.

Codex catalog data is cached for five minutes in the separate
`$ENTROPY_HOME/codex_models_cache.json`. The cache is scoped to the Entropy
version, endpoint, and Codex account identity; it never shares xAI's
`models_cache.json`. Authenticated Codex sessions can use the embedded catalog as
a fallback when the cache or network is unavailable.

Recap and memory work can use independent models from either provider. In
Settings, choose **Recap model** and **Memory model**, or leave them on
**Automatic**: Codex chats use GPT-5.6 Terra at medium reasoning while xAI chats
reuse their active Grok model at low reasoning. Explicit choices can mix
providers—for example, GPT chat with Grok memory—using each provider's isolated
credentials and endpoint. Codex reasoning summaries default to detailed when
the selected model supports them.

## Code Mode

Settings exposes three explicit tool presentations for Responses-backed
models:

- **Direct** exposes ordinary function tools only.
- **Code Mode** adds `exec` and `wait` alongside the ordinary tools.
- **Code Mode Only** keeps ordinary tools behind JavaScript `tools.*`, leaving
  `exec`, `wait`, and direct-only interaction/collaboration controls top-level.

OpenAI Codex model metadata may require Code Mode Only and overrides the user
preference; GPT-5.6 Sol does so. xAI Responses models use the explicit Settings
choice.

Codex carries `exec` as its native freeform custom tool with raw JavaScript;
xAI carries the same runtime control as an ordinary function whose `source`
field contains the JavaScript, because xAI Responses does not accept OpenAI's
native custom-tool type. `wait` resumes or terminates a yielded cell in both
routes. The V8 runtime persists for the active session, resets on timeline or
incompatible provider changes, and is disposed when the session ends. The TUI
and restored transcripts hide the outer `exec`/`wait` transport and show only
decoded nested calls and their normal structured results. Hosted search remains
provider-local. Changing this restart-scoped setting requires restarting Open
Grok; existing persisted sessions retain their resolved mode when resumed.

## Build from source

The product is Go 1.22+. `./bin/entropy-dev` runs `go run ./cmd/entropy`
(`./bin/open-grok-dev` is a compatibility wrapper).

```sh
go test ./...
go build -o entropy ./cmd/entropy
./entropy version
./bin/entropy-dev --version
```

Cross-compile every published platform (Linux, macOS, Windows, FreeBSD ×
amd64/arm64) into `dist/`:

```sh
bash scripts/build-go-release.sh
```

See [`docs/agents/golang.md`](docs/agents/golang.md) for the Go package map.

The previous Rust implementation remains under `crates/` as a reference. A
Rust-only build still needs:

- Rust, pinned by [`rust-toolchain.toml`](rust-toolchain.toml)
- `protoc`, resolved through [`bin/protoc`](bin/protoc) and its
  [Dotslash](https://dotslash-cli.com) launcher, or provided through `PATH` /
  `PROTOC`
- Xcode Command Line Tools when producing the signed macOS release artifacts

```sh
cargo build --locked -p xai-grok-pager-bin --bin open-grok
./target/debug/open-grok --version
```

On Apple Silicon macOS, build the complete release asset set with:

```sh
./scripts/build-macos-release.sh
```

The script reads [`OPEN_GROK_VERSION`](OPEN_GROK_VERSION), injects it through
`GROK_VERSION`, builds the hardened `release-dist` profile, strips and ad-hoc
signs the binary, verifies its version and source commit, and writes these
ignored local artifacts. Release builds require a clean worktree and embed a
trusted local arm64 `rg` selected through `GROK_TOOLS_BUNDLE_RG_PATH` or `PATH`.
The required version is ripgrep 15.0.0, matching the embedded-tool metadata.

```text
dist/open-grok-macos-aarch64
dist/open-grok-macos-aarch64.sha256
dist/install.sh
dist/LICENSE
dist/THIRD-PARTY-NOTICES
```

On Linux x86_64, build the verified release asset set with:

```sh
./scripts/build-linux-release.sh
```

The Linux builder embeds a trusted local x86_64 ripgrep 15.0.0 the same way,
strips the ELF binary, and writes:

```text
dist/open-grok-linux-x86_64
dist/open-grok-linux-x86_64.sha256
dist/install.sh
dist/LICENSE
dist/THIRD-PARTY-NOTICES
```

On 64-bit Windows, build the verified release asset set with:

```powershell
.\scripts\build-windows-release.ps1
```

The Windows builder uses `PROTOC` or `protoc.exe` from `PATH` when supplied.
Otherwise it downloads the pinned official protoc 29.3 archive, verifies its
SHA-256, and caches it under ignored `target/release-tools/`.

```text
dist/open-grok-windows-x86_64.exe
dist/open-grok-windows-x86_64.exe.sha256
dist/install.ps1
dist/LICENSE
dist/THIRD-PARTY-NOTICES
```

## Compatibility with Grok Build

Entropy uses `~/.entropy` for user-level runtime state and project-local
`.entropy` directories for repository configuration. Set `ENTROPY_HOME` to
move all user-level Entropy state (`OPENGROK_HOME` is accepted as an alias).
Project `.opengrok/` trees are still scanned for skills, hooks, and workflows.
Codex OAuth is stored at `~/.entropy/codex-auth.json` by default.

The product does not fall back to upstream Grok Build state. Credentials, settings,
sessions, caches, skills, plugins, and project configuration are isolated from
an upstream installation.

## Repository layout

| Path | Contents |
| --- | --- |
| `cmd/entropy` | Native Go composition root for the `entropy` product binary |
| `internal/` | Go CLI, pager, agent, auth, sampler, tools, and workspace |
| `crates/codegen/xai-grok-pager-bin` | Historical Rust composition-root package for `open-grok` |
| `crates/codegen/xai-grok-pager` | TUI, commands, settings, and rendering |
| `crates/codegen/xai-grok-shell` | Agent runtime, sessions, provider routing, and headless modes |
| `crates/codegen/xai-grok-code-mode*` | Codex-compatible Code Mode protocol and runtime |
| `crates/codegen/xai-grok-tools` | Terminal, file, search, and other tool implementations |
| `AGENTS.md` | Instructions for coding agents working in this repo |
| `docs/` | Fork compatibility notes, release notes, and agent developer docs |
| `docs/agents/` | Architecture, runtime, editing, TUI/config, providers, development |
| `scripts/` | Release packaging helpers |

> [!IMPORTANT]
> The root `Cargo.toml` is generated. Treat it as read-only and make dependency
> changes in the per-crate manifests.

## Upstream provenance and license

This checkout publishes releases from
[`megascan/open-grok`](https://github.com/megascan/open-grok). The public
upstream project remains
[`mweinbach/open-grok`](https://github.com/mweinbach/open-grok). It is derived
from the Grok Build local source snapshot
`c1b5909ec707c069f1d21a93917af044e71da0d7` dated 2026-07-15. That exact
snapshot is the fork baseline because the upstream repository currently has
replacement, unrelated history. Compatible upstream snapshots have since been
ported through Grok Build `1.0.0` at
`8a14c91d88875a831a38b3a066b1683116bcb31c` (2026-08-09), without merging the
unrelated histories. Additional compatible improvements from Grok Build
`1.0.6` snapshot `19d42e35c07a9c9244f03f6df0c4c353f970d4f9` (2026-08-19)
were selectively replayed, including unhealthy-repository session recovery,
keyboard-driven prompt selection, authenticated hook environment expansion,
and sanitized MCP server/tool icon metadata. Codex behavior is ported from a
pinned revision of
[`openai/codex`](https://github.com/openai/codex). The pinned commits and
deliberate compatibility differences are recorded in the documents linked above.

This community fork is not affiliated with, sponsored by, or endorsed by xAI,
SpaceXAI, or OpenAI. Grok, ChatGPT, Codex, and related marks belong to their
respective owners.

First-party source is licensed under the **Apache License, Version 2.0**; see
[`LICENSE`](LICENSE). Ported and vendored code retains its original license and
attribution. See [`THIRD-PARTY-NOTICES`](THIRD-PARTY-NOTICES),
[`crates/codegen/xai-grok-tools/THIRD_PARTY_NOTICES.md`](crates/codegen/xai-grok-tools/THIRD_PARTY_NOTICES.md),
and [`third_party/NOTICE`](third_party/NOTICE).

Every binary GitHub Release must upload `LICENSE` and `THIRD-PARTY-NOTICES`
beside the core binary, checksum, and installer assets. Publish the release as a
full GitHub Release, not a GitHub prerelease, so the documented
`/releases/latest/download` installer URL resolves.

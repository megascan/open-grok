# Antigravity ACP integration

Antigravity is an external Google agent, not an Open Grok sampler provider. The
ACP client uses a separate runtime and profile under `OPENGROK_HOME` (or
`~/.opengrok`). It sends ACP protocol version 1 and `clientInfo.name = zed`
for compatibility. That identity does not establish access to any particular
model: the available models are those advertised to the authenticated account.
Do not substitute another model when the requested one is unavailable.

## Runtime installation

On macOS and Linux, run `scripts/install-antigravity-acp.sh`. It installs the
Google ACP Registry's pinned 1.4.0 archive in
`$OPENGROK_HOME/antigravity-acp/runtime/1.4.0/` without modifying another
application's files. Both the server and its companion executable are needed.
The installer requires `curl`, Python 3 and either `sha256sum` or `shasum`. It
validates the archive hash, permits only the two expected regular files, and
refuses to replace an existing incomplete installation.

The hashes were computed from archives downloaded over HTTPS from the URLs in
the [official ACP registry](https://github.com/agentclientprotocol/registry/blob/main/antigravity-acp/agent.json).
The registry did not publish independent signatures or checksums at the time
of pinning, so the hashes detect later changes to those downloaded bytes; they
are not an independent verification of Google's publication.

A manually installed official executable can be selected with an absolute path
in `OPENGROK_ANTIGRAVITY_ACP`. This also provides a manual installation route on
Windows, which the shell installer does not support. Use the appropriate
platform archive from the registry and keep its companion executable beside the
server. The client supplies `--uid=` on Linux as specified by the registry.

## Authentication and isolation

The client supports the server-advertised `oauth-personal` method only after a
user explicitly initiates sign-in. The server's browser authorization link is
sensitive and is passed to the user interface without logging or persisting it.
The client sets `HOME`, `GEMINI_HOME`, and `AGY_ACP_FORCE_FILE_STORAGE` for an
Open Grok-specific profile; it does not copy Controller or other provider
credentials. The exact 1.4.0 browser flow, credential-store isolation, account
entitlements and model catalog still need an explicitly authorized live OAuth
smoke. An unauthenticated handshake and mocked OAuth are not proof of these.

Use `open-grok login --antigravity` or `/login antigravity` to start sign-in;
`open-grok logout --antigravity` and `/logout antigravity` remove the cached
catalog and request logout. After signing in, start a new session so the task
tool can advertise the account's catalog. Child model names have the form
`antigravity-acp:<advertised-id>` and belong only to the task tool; they are
not parent chat models. A child checks its selected ID against the live ACP
session catalog before sending its prompt.

## Safety and integration status

Only unrestricted general-purpose children can launch: the parent needs an
actor-issued unrestricted lease, inactive plan mode, no hooks, and a verified
local sandbox mode of off. Restricted or unverified parents fail closed.
Normal in-process policy, hook, plan, and sandbox changes revoke the lease and
stop the enrolled process group. ACP permission requests for known tool kinds
may select `allow_once`; interaction questions and unsupported reverse requests
are cancelled or rejected. Questions are not currently routed to the user.
The parent tree's xAI export boundary is closed and persisted before a prompt
is sent. Resume uses a transport-tagged local session record and rechecks the
model against the live catalog.

Sign-in and sign-out cancel active Antigravity children in the same Open Grok
process before changing credentials. A sign-out in a different process cannot
yet cancel already-running children. Completed ACP edit diffs are attributed
to the agent when their full contents match the file on disk. Writes without
such a diff, including changes made by shell commands, can be observed as
external edits by the hunk tracker. Worktree snapshot and child usage reporting
are also not equivalent to native children.

Focused ACP transport and route tests and pager sign-in tests passed using
mocks. The workspace permission-lease and sandbox fence tests also passed.
The authenticated browser flow, account catalog and a real task still need a
user-authorized end-to-end smoke with the installed 1.4.0 server. See the ACP [initialization](https://agentclientprotocol.com/protocol/v1/initialization),
[authentication](https://agentclientprotocol.com/protocol/v1/authentication) and
[session configuration](https://agentclientprotocol.com/protocol/v1/session-config-options)
specifications for wire behavior.

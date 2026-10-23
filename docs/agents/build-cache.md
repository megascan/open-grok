# Bounded Cargo verification

Use one reusable target directory for a coordinated task, and serialize its
Cargo commands. The wrapper monitors that directory and the free space on its
filesystem throughout each command; it does not delete any cache or touch
other Cargo processes. For example:

```sh
export CARGO_BOUNDED_TARGET_DIR=/tmp/open-grok-sync-20261016/target
./scripts/cargo-bounded.sh check --locked -p xai-grok-shell
./scripts/cargo-bounded.sh test --locked -p xai-grok-shell -- plan_mode
```

Choose a task-specific path on a local filesystem with enough free space. The
default, `/tmp/open-grok-cargo-target`, is reusable across invocations but may
be cleaned by the operating system. All participants must use the same path
and settings to share artifacts. The wrapper locks the target against another
instance of itself; do not run unguarded Cargo commands or other writers in
that directory. The wrapper creates the target and a lock file if necessary.
It does not modify global Cargo configuration or preexisting caches.

The default budget is **80 GiB** of allocated target-directory space and the
free-space floor is **20 GiB** on the target filesystem. Each command reports
its starting usage, then checks at two-second intervals and on completion. If
the target reaches its budget, available space drops below the floor, or a
measurement fails, the wrapper terminates its own Cargo process group and
returns status 125. It refuses to start when the starting state already
violates either threshold. Normal Cargo exit status is propagated. This is a
sampled guard, not a hard quota: builds can overshoot between samples, and
other processes can consume space independently. Leave adequate headroom;
use an OS-enforced quota when a strict limit is required.

The wrapper sets `CARGO_TARGET_DIR`, disables incremental compilation and
inherited Rust compiler wrappers, and disables debug information in dev/test
profiles and their build overrides. This avoids populating a separate sccache
while using this workflow. The normal machine-aware Cargo job default remains
in effect. Explicit custom or release profiles, compiler flags, build scripts,
tests, dependency downloads, and output paths outside the target may still
consume space elsewhere. Do not use this wrapper as a release builder. Cargo
`--config` and `--target-dir` options are rejected because they could bypass
the wrapper's settings; arguments after Cargo's `--` are passed through.

Optional environment settings (integer bytes and seconds):

| Variable | Default | Purpose |
| --- | ---: | --- |
| `CARGO_BOUNDED_TARGET_DIR` | `/tmp/open-grok-cargo-target` | Reusable task target |
| `CARGO_BOUNDED_MAX_BYTES` | `85899345920` | Target budget, 80 GiB |
| `CARGO_BOUNDED_MIN_FREE_BYTES` | `21474836480` | Minimum available space, 20 GiB |
| `CARGO_BOUNDED_INTERVAL` | `2` | Sampling interval in seconds |

Inspect before deciding whether to reclaim any storage: `du -sh "$CARGO_BOUNDED_TARGET_DIR"`
and `df -h "$CARGO_BOUNDED_TARGET_DIR"`. Cleanup is a separate explicit
decision after all users of a task target have finished; never sweep unrelated
targets, registries, or compiler caches.

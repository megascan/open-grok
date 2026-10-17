#!/usr/bin/env bash
# Reuse one monitored target for local verification without touching other caches.
set -euo pipefail

if [[ $# -eq 0 || ${1:-} == --help ]]; then
    cat <<'USAGE'
Usage: scripts/cargo-bounded.sh <cargo arguments...>

Runs Cargo with a reusable target directory, incremental compilation and
compiler caches disabled, and dev/test debug information disabled. Stops its
Cargo process group when target size or free disk thresholds are crossed.

CARGO_BOUNDED_TARGET_DIR  Target directory (default: /tmp/open-grok-cargo-target)
CARGO_BOUNDED_MAX_BYTES   Target size budget (default: 85899345920, 80 GiB)
CARGO_BOUNDED_MIN_FREE_BYTES  Minimum available disk space (default: 21474836480, 20 GiB)
CARGO_BOUNDED_INTERVAL    Sampling interval in seconds (default: 2)

The guard samples usage; it is not a hard filesystem quota. Do not run other
writers in the same target directory or put it on a shared network filesystem.
USAGE
    [[ $# -gt 0 ]] && exit 0
    exit 2
fi

exec python3 - "$@" <<'PY'
import fcntl
import os
import shutil
import signal
import subprocess
import sys
import time

args = sys.argv[1:]
# Cargo options can override the environment set below. Arguments after `--`
# belong to the test/program being run, rather than Cargo itself.
for arg in args[:args.index('--') if '--' in args else len(args)]:
    if arg == '--target-dir' or arg.startswith('--target-dir=') or arg == '--config' or arg.startswith('--config='):
        sys.exit('cargo-bounded: --target-dir and --config are not permitted; use CARGO_BOUNDED_TARGET_DIR')


def integer_setting(name, default, minimum):
    raw = os.environ.get(name, str(default))
    if not raw.isascii() or not raw.isdecimal():
        sys.exit(f'cargo-bounded: {name} must be an integer >= {minimum}')
    value = int(raw)
    if value < minimum:
        sys.exit(f'cargo-bounded: {name} must be an integer >= {minimum}')
    return value


limit = integer_setting('CARGO_BOUNDED_MAX_BYTES', 80 * 1024**3, 1)
min_free = integer_setting('CARGO_BOUNDED_MIN_FREE_BYTES', 20 * 1024**3, 0)
interval = integer_setting('CARGO_BOUNDED_INTERVAL', 2, 1)
target = os.path.realpath(os.path.expanduser(os.environ.get('CARGO_BOUNDED_TARGET_DIR', '/tmp/open-grok-cargo-target')))
os.makedirs(target, exist_ok=True)
lock = open(os.path.join(target, '.cargo-bounded.lock'), 'a')
try:
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    sys.exit(f'cargo-bounded: another guarded command is using {target}')


def sample():
    # du reports allocated filesystem blocks, rather than sparse-file lengths.
    result = subprocess.run(['du', '-sk', target], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"cannot measure target: {result.stderr.strip()}")
    try:
        used = int(result.stdout.split()[0]) * 1024
        free = shutil.disk_usage(target).free
    except (ValueError, IndexError, OSError) as exc:
        raise RuntimeError(f'cannot measure disk usage: {exc}') from exc
    return used, free


def violation(used, free):
    if used >= limit:
        return f'target uses {used} bytes (budget {limit})'
    if free < min_free:
        return f'filesystem has {free} bytes free (minimum {min_free})'
    return None


def checked_sample():
    used, free = sample()
    return used, free, violation(used, free)


try:
    used, free, reason = checked_sample()
except (OSError, RuntimeError) as exc:
    sys.exit(f'cargo-bounded: {exc}')
print(f'cargo-bounded: target={target} used={used} budget={limit} free={free} minimum={min_free}', file=sys.stderr, flush=True)
if reason:
    sys.exit(f'cargo-bounded: refusing to start: {reason}')

env = os.environ.copy()
env.update({
    'CARGO_TARGET_DIR': target,
    'CARGO_BUILD_TARGET_DIR': target,
    'CARGO_INCREMENTAL': '0',
    'CARGO_PROFILE_DEV_INCREMENTAL': 'false',
    'CARGO_PROFILE_TEST_INCREMENTAL': 'false',
    'CARGO_PROFILE_DEV_DEBUG': '0',
    'CARGO_PROFILE_TEST_DEBUG': '0',
    'CARGO_PROFILE_DEV_BUILD_OVERRIDE_DEBUG': '0',
    'CARGO_PROFILE_TEST_BUILD_OVERRIDE_DEBUG': '0',
    'RUSTC_WRAPPER': '',
    'RUSTC_WORKSPACE_WRAPPER': '',
    'CARGO_BUILD_RUSTC_WRAPPER': '',
    'CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER': '',
})
try:
    process = subprocess.Popen(['cargo', *args], env=env, start_new_session=True)
except OSError as exc:
    sys.exit(f'cargo-bounded: cannot start cargo: {exc}')


def stop_group():
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        pass
    # Also stop surviving descendants if Cargo exited before them.
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def interrupted(signum, _frame):
    print(f'cargo-bounded: received signal {signum}; stopping Cargo process group', file=sys.stderr, flush=True)
    stop_group()
    sys.exit(128 + signum)


signal.signal(signal.SIGINT, interrupted)
signal.signal(signal.SIGTERM, interrupted)
while True:
    try:
        process.wait(timeout=interval)
        finished = True
    except subprocess.TimeoutExpired:
        finished = False
    try:
        used, free, reason = checked_sample()
    except (OSError, RuntimeError) as exc:
        reason = str(exc)
    if reason:
        print(f'cargo-bounded: stopping: {reason}', file=sys.stderr, flush=True)
        stop_group()
        sys.exit(125)
    if finished:
        print(f'cargo-bounded: finished: target used={used} bytes; free={free} bytes', file=sys.stderr, flush=True)
        sys.exit(process.returncode if process.returncode >= 0 else 128 - process.returncode)
PY

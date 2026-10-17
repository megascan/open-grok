#!/usr/bin/env bash
# Install Google's pinned Antigravity ACP distribution into Open Grok's home.
set -euo pipefail
umask 077

version=1.4.0
case "$(uname -s):$(uname -m)" in
  Darwin:arm64) platform=macos; arch=arm64; digest=21a32eeab85b394a515510c84539bda4b2504bf2917e0a201f5c4130b13581a5 ;;
  Darwin:x86_64) platform=macos; arch=x86_64; digest=407b6b8ef167bb207d1effdbd488bacf63ba7dab2b47d4a47d8ea3710a8c0f55 ;;
  Linux:aarch64|Linux:arm64) platform=linux; arch=arm64; digest=3dc43a21f5402b23a4dba7b320a6745e9dbf4b6ead912ce9fc76011ea3a08a7e ;;
  Linux:x86_64) platform=linux; arch=x86_64; digest=fbb2d54dd94fb897845308d3b6b46187c627f1c60078ded81adeeaf8b90c5d7a ;;
  *) printf 'Unsupported platform. Install the official ACP server manually and configure its absolute path.\n' >&2; exit 1 ;;
esac
case "$platform" in macos) release_os=darwin ;; linux) release_os=linux ;; esac
opengrok_home=${OPENGROK_HOME:-"${HOME:?HOME is required}/.opengrok"}
case "$opengrok_home" in /*) ;; *) printf 'OPENGROK_HOME must be an absolute path.\n' >&2; exit 1 ;; esac
runtime="$opengrok_home/antigravity-acp/runtime"
destination="$runtime/$version"
mkdir -p "$runtime"
if [ -e "$destination" ] || [ -L "$destination" ]; then
  if [ -f "$destination/agy_acp_server.par" ] && [ -x "$destination/agy_acp_server.par" ] && [ -f "$destination/localharness_external" ]; then
    printf 'Antigravity ACP %s already installed: %s\n' "$version" "$destination/agy_acp_server.par"
    exit 0
  fi
  printf 'Refusing to replace existing runtime: %s\n' "$destination" >&2
  exit 1
fi
lock="$runtime/.install-$version.lock"
if ! mkdir "$lock" 2>/dev/null; then
  printf 'Another install is in progress or a stale lock exists: %s\n' "$lock" >&2
  exit 1
fi
staging=''
cleanup() { [ -z "$staging" ] || rm -rf -- "$staging"; rmdir "$lock" 2>/dev/null || true; }
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
staging=$(mktemp -d "$runtime/.install-$version.XXXXXXXX")
archive="$staging/archive.zip"
url="https://dl.google.com/agy-extensions/releases/$platform/agy-acp-server-$version-$release_os-$arch.zip"
curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --output "$archive" "$url"
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$archive" | awk '{print $1}'); else actual=$(shasum -a 256 "$archive" | awk '{print $1}'); fi
if [ "$actual" != "$digest" ]; then printf 'Antigravity ACP archive checksum mismatch.\n' >&2; exit 1; fi
# Only extract the two expected regular files; reject traversal, symlinks,
# duplicates and oversized members. The archive is verified before extraction.
python3 - "$archive" "$staging" <<'PY'
import os, stat, sys, zipfile
archive, staging = sys.argv[1:]
expected = {'agy_acp_server.par', 'localharness_external'}
with zipfile.ZipFile(archive) as z:
    entries = z.infolist()
    if len(entries) != len(expected) or {e.filename for e in entries} != expected:
        raise SystemExit('Unexpected archive contents')
    for e in entries:
        mode = e.external_attr >> 16
        kind = stat.S_IFMT(mode)
        if kind not in (0, stat.S_IFREG) or e.file_size > 2 * 1024**3:
            raise SystemExit('Unsafe archive member')
        path = os.path.join(staging, e.filename)
        with z.open(e) as source, open(path, 'xb') as dest:
            while chunk := source.read(1024 * 1024):
                dest.write(chunk)
        os.chmod(path, 0o700)
PY
rm -- "$archive"
if [ -e "$destination" ] || [ -L "$destination" ]; then printf 'Runtime appeared during installation; refusing to replace it.\n' >&2; exit 1; fi
mv -- "$staging" "$destination"
staging=''
printf 'Installed Antigravity ACP %s: %s\n' "$version" "$destination/agy_acp_server.par"

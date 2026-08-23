#!/usr/bin/env bash
# Cross-compile the native Go open-grok binary for every published platform.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="$(tr -d '[:space:]' < OPEN_GROK_VERSION)"
ldflags="-s -w -X github.com/megascan/open-grok/internal/version.verOverride=${version}"
mkdir -p dist

# Keep internal/version/VERSION in lockstep with the repo stamp.
printf '%s\n' "$version" > internal/version/VERSION

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
  "freebsd/amd64"
)

for spec in "${targets[@]}"; do
  goos="${spec%/*}"
  goarch="${spec#*/}"
  arch="$goarch"
  case "$goarch" in
    amd64) arch=x86_64 ;;
    arm64) arch=aarch64 ;;
  esac
  name="open-grok-${goos}-${arch}"
  if [[ "$goos" == windows ]]; then
    name="${name}.exe"
  fi
  echo "→ $name"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$ldflags" -o "dist/${name}" ./cmd/open-grok
  (
    cd dist
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$name" > "${name}.sha256"
    else
      shasum -a 256 "$name" > "${name}.sha256"
    fi
  )
done

echo "artifacts in dist/"
ls -l dist

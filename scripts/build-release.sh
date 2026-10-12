#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
checkin_version="${1:-0.2.0}"
if [[ ! "$checkin_version" =~ ^[a-zA-Z0-9._-]+$ ]]; then
  printf '%s\n' '版本只能包含字母、数字、点、下划线和连字符' >&2
  exit 2
fi
mkdir -p dist
for checkin_target in darwin/arm64 darwin/amd64 windows/amd64 linux/amd64; do
  checkin_os="${checkin_target%/*}"
  checkin_arch="${checkin_target#*/}"
  checkin_name="checkin-${checkin_os}-${checkin_arch}"
  checkin_dir="dist/${checkin_name}"
  mkdir -p "$checkin_dir"
  checkin_executable="checkin"
  if [ "$checkin_os" = windows ]; then checkin_executable="checkin.exe"; fi
  CGO_ENABLED=0 GOOS="$checkin_os" GOARCH="$checkin_arch" go build -trimpath \
    -ldflags "-s -w -X checkin/internal/version.Version=$checkin_version" \
    -o "$checkin_dir/$checkin_executable" ./cmd/checkin
  checkin_size=$(wc -c < "$checkin_dir/$checkin_executable")
  if [ "$checkin_size" -gt 12582912 ]; then
    printf '%s\n' "$checkin_name 超过 12 MiB：$checkin_size 字节" >&2
    exit 1
  fi
  cp README.md "$checkin_dir/README.md"
  (cd "$checkin_dir" && zip -q -9 -j "../$checkin_name.zip" "$checkin_executable" README.md)
  printf '%s\n' "${checkin_name}：${checkin_size} 字节"
done
if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum checkin-*.zip > SHA256SUMS)
else
  (cd dist && shasum -a 256 checkin-*.zip > SHA256SUMS)
fi

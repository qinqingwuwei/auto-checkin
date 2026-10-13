#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
checkin_version="${1:-dev}"
checkin_version="${checkin_version#v}"
if [[ ! "$checkin_version" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]; then
  printf '%s\n' '版本只能包含字母、数字、点、下划线和连字符' >&2
  exit 2
fi
checkin_targets=(darwin/arm64 darwin/amd64 windows/amd64 linux/amd64)
if [ -n "${2:-}" ]; then
  case "$2" in
    darwin/arm64|darwin/amd64|windows/amd64|linux/amd64) checkin_targets=("$2") ;;
    *) printf '%s\n' "不支持的平台：$2" >&2; exit 2 ;;
  esac
fi
checkin_output="dist/$checkin_version"
mkdir -p "$checkin_output"
checkin_staging=$(mktemp -d "${TMPDIR:-/tmp}/checkin-release.XXXXXX")
trap 'rm -rf "$checkin_staging"' EXIT
checkin_epoch="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}"
checkin_archives=()
for checkin_target in "${checkin_targets[@]}"; do
  checkin_os="${checkin_target%/*}"
  checkin_arch="${checkin_target#*/}"
  checkin_name="checkin-${checkin_version}-${checkin_os}-${checkin_arch}"
  checkin_dir="$checkin_staging/$checkin_name"
  mkdir -p "$checkin_dir"
  checkin_executable=checkin
  checkin_setup=setup.sh
  if [ "$checkin_os" = windows ]; then
    checkin_executable=checkin.exe
    checkin_setup=setup.ps1
  fi
  CGO_ENABLED=0 GOOS="$checkin_os" GOARCH="$checkin_arch" go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X checkin/internal/version.Version=$checkin_version" \
    -o "$checkin_dir/$checkin_executable" ./cmd/checkin
  checkin_size=$(wc -c < "$checkin_dir/$checkin_executable")
  if [ "$checkin_size" -gt 12582912 ]; then
    printf '%s\n' "$checkin_name 超过 12 MiB：$checkin_size 字节" >&2
    exit 1
  fi
  cp README.md "$checkin_dir/README.md"
  cp "$checkin_setup" "$checkin_dir/$checkin_setup"
  python3 scripts/package-release.py "$checkin_dir" "$checkin_output/$checkin_name.zip" "$checkin_epoch"
  checkin_archives+=("$checkin_name.zip")
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$checkin_output" && sha256sum "$checkin_name.zip" > "$checkin_name.zip.sha256")
  else
    (cd "$checkin_output" && shasum -a 256 "$checkin_name.zip" > "$checkin_name.zip.sha256")
  fi
  printf '%s\n' "${checkin_name}：二进制 $checkin_size 字节"
done
if [ "${#checkin_targets[@]}" -eq 4 ]; then
  (cd "$checkin_output" && cat "${checkin_archives[@]/%/.sha256}" > SHA256SUMS)
  python3 scripts/verify-release.py "$checkin_output" "$checkin_version"
fi

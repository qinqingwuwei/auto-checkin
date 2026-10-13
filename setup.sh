#!/usr/bin/env bash
# 使用已下载的二进制，或从源码构建；不安装运行时或浏览器。
set -euo pipefail
cd "$(dirname "$0")"
checkin_root="${CHECKIN_HOME:-$HOME/.checkin}"
if [[ "$checkin_root" = '~/'* ]]; then checkin_root="$HOME/${checkin_root:2}"; fi
mkdir -p "$checkin_root/bin"
checkin_root=$(cd "$checkin_root" && pwd)
checkin_binary="$checkin_root/bin/checkin"
checkin_source="${CHECKIN_BINARY:-./checkin}"
if [ -f "$checkin_source" ]; then
  if ! [ "$checkin_source" -ef "$checkin_binary" ]; then install -m 755 "$checkin_source" "$checkin_binary"; fi
elif command -v go >/dev/null 2>&1; then
  CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$checkin_binary" ./cmd/checkin
else
  printf '%s\n' '请先从 GitHub Releases 下载对应平台的 checkin 放到此目录，或安装 Go 1.23+ 后从源码构建。' \
    'https://github.com/qinqingwuwei/auto-checkin/releases' >&2
  exit 1
fi
chmod 755 "$checkin_binary"
export CHECKIN_HOME="$checkin_root"
"$checkin_binary" --version
printf '\n%s\n' "安装完成：$checkin_binary" "看板：\"$checkin_binary\" ui" '在看板中启用定时，退出程序即停止；不会安装系统后台任务。'

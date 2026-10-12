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
if [ -t 0 ]; then
  if [ ! -s "$checkin_root/credentials/trae.json" ] && [ ! -s "$checkin_root/trae-web-state.json" ]; then
    read -r -p 'Trae 未登录，按 Enter 打开系统浏览器登录（输入 n 跳过）：' checkin_answer || checkin_answer=n
    if [ "$checkin_answer" != n ]; then "$checkin_binary" login trae || true; fi
  fi
  if ! compgen -G "$checkin_root/credentials/workbuddy/*.json" >/dev/null && [ ! -s "$checkin_root/workbuddy-sms.json" ] && [ ! -s "$checkin_root/workbuddy-oauth.json" ]; then
    read -r -p 'WorkBuddy 登录：1 浏览器扫码 / 2 短信 / 3 跳过 [3]：' checkin_method || checkin_method=3
    case "$checkin_method" in
      1) "$checkin_binary" login workbuddy --method browser || true ;;
      2) "$checkin_binary" login workbuddy --method sms || true ;;
    esac
  fi
fi
if [ "$(uname -s)" = Darwin ]; then "$checkin_binary" schedule install; fi
"$checkin_binary" doctor
"$checkin_binary" run --dry-run
printf '\n%s\n' "安装完成：$checkin_binary" "看板：\"$checkin_binary\" ui"

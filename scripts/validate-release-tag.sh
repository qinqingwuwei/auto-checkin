#!/usr/bin/env bash
set -euo pipefail
checkin_tag="${1:-}"
if [[ ! "$checkin_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)*)?$ ]]; then
  printf '%s\n' '发布 Tag 应为 vMAJOR.MINOR.PATCH，可追加 -rc.1 等预发布标识。' >&2
  exit 2
fi
# Semantic Versioning prohibits leading zeros in numeric prerelease parts.
if [[ "$checkin_tag" == *-* ]]; then
  checkin_prerelease="${checkin_tag#*-}"
  IFS=. read -r -a checkin_parts <<< "$checkin_prerelease"
  for checkin_part in "${checkin_parts[@]}"; do
    if [[ "$checkin_part" =~ ^0[0-9]+$ ]]; then
      printf '%s\n' '预发布版本的纯数字标识不能包含前导零。' >&2
      exit 2
    fi
  done
fi

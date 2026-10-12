"""本地系统通知：macOS 用 osascript（Windows toast 属 Phase 3）。"""
from __future__ import annotations

import logging
import subprocess

from checkin import paths

log = logging.getLogger(__name__)


def _escape(text: str) -> str:
    return text.replace("\\", "\\\\").replace('"', '\\"')


def send(title: str, text: str) -> tuple[bool, str]:
    """弹一条系统通知；返回 ``(是否成功, 说明)``。"""
    if paths.is_macos():
        script = (
            f'display notification "{_escape(text[:400])}" '
            f'with title "{_escape(title[:120])}"'
        )
        try:
            result = subprocess.run(
                ["osascript", "-e", script], capture_output=True, timeout=15
            )
        except Exception as exc:  # noqa: BLE001
            return False, f"osascript 调用失败：{type(exc).__name__}: {exc}"
        if result.returncode == 0:
            return True, "ok"
        return False, f"osascript 返回 {result.returncode}：{result.stderr.decode(errors='replace')[:150]}"
    if paths.is_windows():
        return False, "Windows 本地通知将在 Phase 3 支持（当前仅日志 + 钉钉）"
    return False, "当前平台暂不支持本地系统通知（仅日志 + 钉钉）"
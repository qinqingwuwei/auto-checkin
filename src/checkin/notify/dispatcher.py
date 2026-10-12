"""通知分发器：合并成单条消息，逐通道发送；失败降级为仅日志。

日志是兜底通道（``log`` 模块始终写 ``~/.checkin/logs/checkin.log``），
因此任何通知通道失败都不影响签到主流程的成败判定。
"""
from __future__ import annotations

import logging
from dataclasses import dataclass, field

from checkin import config
from checkin.notify import dingtalk, local

log = logging.getLogger(__name__)

@dataclass
class Notification:
    title: str
    lines: list[str] = field(default_factory=list)
    level: str = "info"  # info | warn | error

    def body(self) -> str:
        parts = [f"### {self.title}", ""]
        parts.extend(f"- {line}" for line in self.lines)
        return "\n".join(parts)


def dispatch(note: Notification) -> dict[str, tuple[bool, str]]:
    """发送到所有已启用通道；返回每通道结果（始终写日志）。"""
    cfg = config.load()
    results: dict[str, tuple[bool, str]] = {}
    text = note.body()
    log.info("通知[%s] %s\n%s", note.level, note.title, "\n".join(note.lines))

    ding = cfg["notify"]["dingtalk"]
    if ding.get("enabled"):
        if ding.get("webhook"):
            results["dingtalk"] = dingtalk.send(note.title, text)
        else:
            results["dingtalk"] = (False, "未配置 webhook（跳过）")
            log.warning("钉钉通知未配置 webhook，已跳过（本地通知/日志不受影响）")
    else:
        results["dingtalk"] = (False, "已关闭（跳过）")

    if cfg["notify"]["local"].get("enabled"):
        summary = note.lines[0] if note.lines else note.title
        results["local"] = local.send(note.title, summary)
    else:
        results["local"] = (False, "已关闭（跳过）")

    for channel, (ok, message) in results.items():
        if ok:
            log.info("通知通道 %s 发送成功", channel)
        else:
            log.warning("通知通道 %s 未送达：%s", channel, message)
    return results
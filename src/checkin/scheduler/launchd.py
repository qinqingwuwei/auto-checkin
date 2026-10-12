"""launchd 定时任务（macOS）：``com.checkin.daily`` 的生成 / 安装 / 卸载 / 查询。

- ``StartCalendarInterval`` 取 ``config.schedule.time``（默认 08:00），``RunAtLoad=false``；
- 用 venv 的 python 绝对路径 ``-m checkin run`` 拉起（不依赖 PATH）；
- 标准输出/错误重定向到 ``~/.checkin/logs/``；
- 睡眠/关机错过定时点：launchd 唤醒后补跑，幂等逻辑保证不重复签到（计划 §5.3）。
"""
from __future__ import annotations

import logging
import os
import plistlib
import subprocess
import sys
from xml.sax.saxutils import escape

from checkin import config, paths

log = logging.getLogger(__name__)

LABEL = "com.checkin.daily"


def plist_path():
    return paths.launch_agents_dir() / f"{LABEL}.plist"


def _domain() -> str:
    return f"gui/{os.getuid()}"


def program_args() -> list[str]:
    return [sys.executable, "-m", "checkin", "run"]


def render_plist(time_str: str | None = None) -> str:
    """生成 plist 文本（HH:MM → Hour/Minute）。"""
    time_str = time_str or config.schedule_time()
    hour, minute = (int(x) for x in time_str.split(":"))
    out, err = paths.launchd_log_paths()
    out.parent.mkdir(parents=True, exist_ok=True)
    args = "\n".join(f"        <string>{escape(a)}</string>" for a in program_args())
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{LABEL}</string>
    <key>ProgramArguments</key>
    <array>
{args}
    </array>
    <key>WorkingDirectory</key>
    <string>{escape(str(paths.project_dir()))}</string>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>{hour}</integer>
        <key>Minute</key>
        <integer>{minute}</integer>
    </dict>
    <key>RunAtLoad</key>
    <false/>
    <key>StandardOutPath</key>
    <string>{escape(str(out))}</string>
    <key>StandardErrorPath</key>
    <string>{escape(str(err))}</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
</dict>
</plist>
"""


def _run_launchctl(args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(["launchctl", *args], capture_output=True, text=True, timeout=30)


def install() -> tuple[bool, str]:
    """生成并加载定时任务（幂等：先卸载再加载）。"""
    if not paths.is_macos():
        return False, "当前平台不是 macOS（Windows 定时将在 Phase 3 支持）"
    target = plist_path()
    target.parent.mkdir(parents=True, exist_ok=True)
    text = render_plist()
    target.write_text(text, encoding="utf-8")
    os.chmod(target, 0o644)

    _run_launchctl(["bootout", _domain(), str(target)])  # 已加载时先卸载（失败忽略）
    result = _run_launchctl(["bootstrap", _domain(), str(target)])
    if result.returncode != 0:
        legacy = _run_launchctl(["load", "-w", str(target)])
        if legacy.returncode != 0:
            return False, (
                f"launchctl bootstrap 失败：{result.stderr.strip()[:200]}；"
                f"load -w 亦失败：{legacy.stderr.strip()[:200]}"
            )
    return True, f"已安装定时任务 {LABEL}（每天 {config.schedule_time()} 执行），plist：{target}"


def uninstall() -> tuple[bool, str]:
    target = plist_path()
    if not target.exists():
        return True, "未安装定时任务（plist 不存在）"
    _run_launchctl(["bootout", _domain(), str(target)])
    _run_launchctl(["unload", "-w", str(target)])
    try:
        target.unlink()
    except OSError as exc:
        return False, f"删除 plist 失败：{exc}"
    return True, f"已卸载定时任务 {LABEL}"


def status() -> dict:
    """查看安装状态与配置时间是否一致。"""
    target = plist_path()
    info: dict = {
        "installed": target.exists(),
        "plist": str(target),
        "config_time": config.schedule_time(),
        "plist_time": None,
        "loaded": False,
        "program": program_args(),
        "log_out": str(paths.launchd_log_paths()[0]),
        "log_err": str(paths.launchd_log_paths()[1]),
    }
    if target.exists():
        try:
            data = plistlib.loads(target.read_bytes())
            interval = data.get("StartCalendarInterval") or {}
            info["plist_time"] = f"{int(interval.get('Hour', 0)):02d}:{int(interval.get('Minute', 0)):02d}"
            info["program"] = list(data.get("ProgramArguments") or program_args())
            info["run_at_load"] = bool(data.get("RunAtLoad"))
        except Exception as exc:  # noqa: BLE001
            info["error"] = f"plist 解析失败：{exc}"
        result = _run_launchctl(["list", LABEL])
        info["loaded"] = result.returncode == 0
        info["launchctl_output"] = (result.stdout or result.stderr).strip()[:200]
    return info
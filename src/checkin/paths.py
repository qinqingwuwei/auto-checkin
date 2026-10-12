"""路径解析（唯一的 OS 分支点之一）。

本工具的所有数据统一放在 ``~/.checkin/``（可用环境变量 ``CHECKIN_HOME`` 覆盖，
供测试隔离使用），**绝不写回任何客户端目录**。
"""
from __future__ import annotations

import os
import pathlib
import sys

APP_NAME = "checkin"


def is_macos() -> bool:
    return sys.platform == "darwin"


def is_windows() -> bool:
    return os.name == "nt"


# ---------------------------------------------------------------- 数据根目录

def state_dir() -> pathlib.Path:
    """工具数据根目录（默认 ~/.checkin，可被 CHECKIN_HOME 覆盖）。"""
    env = os.environ.get("CHECKIN_HOME")
    if env:
        return pathlib.Path(env).expanduser()
    return pathlib.Path.home() / ".checkin"


def project_dir() -> pathlib.Path:
    """本项目仓库根目录（src/checkin/paths.py → 上两级）。"""
    return pathlib.Path(__file__).resolve().parents[2]


# ---------------------------------------------------------------- 文件布局

def config_path() -> pathlib.Path:
    return state_dir() / "config.json"


def state_path() -> pathlib.Path:
    return state_dir() / "state.json"


def points_path() -> pathlib.Path:
    return state_dir() / "points.jsonl"


def logs_dir() -> pathlib.Path:
    return state_dir() / "logs"


def log_path() -> pathlib.Path:
    return logs_dir() / "checkin.log"


def launchd_log_paths() -> tuple[pathlib.Path, pathlib.Path]:
    """launchd 的标准输出 / 标准错误落盘位置。"""
    return logs_dir() / "launchd.out.log", logs_dir() / "launchd.err.log"


def credentials_dir() -> pathlib.Path:
    return state_dir() / "credentials"


def trae_cred_path() -> pathlib.Path:
    return credentials_dir() / "trae.json"


def workbuddy_cred_dir() -> pathlib.Path:
    """WorkBuddy 多账号凭据目录（每账号一个 <uid>.json）。"""
    return credentials_dir() / "workbuddy"


def trae_browser_profile_dir() -> pathlib.Path:
    """Trae 登录用的持久化浏览器 profile（复用系统浏览器的独立窗口）。"""
    return state_dir() / "browser" / "trae"


# ------------------------------------------- Phase 0 探针遗留产物（仅首启迁移）

def legacy_trae_state_path() -> pathlib.Path:
    return state_dir() / "trae-web-state.json"


def legacy_workbuddy_paths() -> list[pathlib.Path]:
    """遗留凭据文件，按新旧排序（SMS 版更晚产生，优先）。"""
    return [state_dir() / "workbuddy-sms.json", state_dir() / "workbuddy-oauth.json"]


# ---------------------------------------------------------------- 系统位置

def launch_agents_dir() -> pathlib.Path:
    return pathlib.Path.home() / "Library" / "LaunchAgents"


def ensure_dirs() -> None:
    """创建数据目录（含权限收紧，尽力而为）。"""
    for d in (state_dir(), credentials_dir(), workbuddy_cred_dir(), logs_dir()):
        d.mkdir(parents=True, exist_ok=True)
    if is_macos():
        try:
            os.chmod(state_dir(), 0o700)
        except OSError:
            pass
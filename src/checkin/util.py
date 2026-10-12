"""通用小工具：JWT 解析、递归取键、脱敏、JSON/JSONL 读写、时间格式化。"""
from __future__ import annotations

import base64
import datetime as dt
import json
import os
import pathlib
import re
import subprocess
import sys
import time
from typing import Any

# ---------------------------------------------------------------- JWT

def jwt_payload(token: str) -> dict:
    """解出 JWT 的 payload（不校验签名）；失败返回空 dict。"""
    try:
        seg = token.split(".")[1]
        seg += "=" * (-len(seg) % 4)
        payload = json.loads(base64.urlsafe_b64decode(seg))
        return payload if isinstance(payload, dict) else {}
    except Exception:  # noqa: BLE001
        return {}


def jwt_exp(token: str) -> int | None:
    exp = jwt_payload(token).get("exp")
    return int(exp) if isinstance(exp, (int, float)) else None


# ---------------------------------------------------------------- 递归取值

def dig(obj: Any, keys: list[str]) -> Any:
    """递归（键名大小写不敏感）取第一个命中的非空值。"""
    wanted = {k.lower() for k in keys}
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k.lower() in wanted and v not in (None, ""):
                return v
        for v in obj.values():
            hit = dig(v, keys)
            if hit is not None:
                return hit
    elif isinstance(obj, list):
        for v in obj[:5]:
            hit = dig(v, keys)
            if hit is not None:
                return hit
    return None


def find_list(obj: Any, key_name: str) -> list | None:
    """递归（键名大小写不敏感）找名为 key_name 的 list 值。"""
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k.lower() == key_name.lower() and isinstance(v, list):
                return v
        for v in obj.values():
            hit = find_list(v, key_name)
            if hit is not None:
                return hit
    elif isinstance(obj, list):
        for v in obj:
            hit = find_list(v, key_name)
            if hit is not None:
                return hit
    return None


# ---------------------------------------------------------------- 脱敏

def mask_secret(value: str | None, keep_head: int = 4, keep_tail: int = 4) -> str:
    """密钥脱敏展示（保留头尾少量字符 + 长度）。"""
    if not value:
        return "（未配置）"
    if len(value) <= keep_head + keep_tail:
        return f"{value[0]}{'*' * (len(value) - 1)}（共 {len(value)} 字符）"
    return f"{value[:keep_head]}…{value[-keep_tail:]}（共 {len(value)} 字符）"


def mask_webhook(url: str | None) -> str:
    if not url:
        return "（未配置）"
    return re.sub(
        r"(access_token=)([^&]+)",
        lambda m: m.group(1) + mask_secret(m.group(2), 4, 4),
        url,
    )


def mask_phone(phone: str | None) -> str:
    if not phone or len(phone) < 7:
        return phone or "-"
    return f"{phone[:3]}****{phone[-4:]}"


# ---------------------------------------------------------------- 时间

def fmt_ts(epoch: float | int | None, fmt: str = "%Y-%m-%d %H:%M") -> str:
    if not epoch:
        return "-"
    return time.strftime(fmt, time.localtime(epoch))


def fmt_remain(epoch: float | int | None) -> str:
    """距某时刻的剩余时长描述，用于凭据状态展示。"""
    if not epoch:
        return "未知"
    delta = epoch - time.time()
    if delta <= 0:
        return "已过期"
    hours = delta / 3600
    if hours < 1:
        return f"{int(delta / 60)} 分钟后过期"
    if hours < 48:
        return f"{hours:.1f} 小时后过期"
    return f"{hours / 24:.1f} 天后过期"


def today_str() -> str:
    return dt.datetime.now().strftime("%Y-%m-%d")


# ---------------------------------------------------------------- 文件读写

def atomic_write_text(path: pathlib.Path, text: str, mode: int = 0o600) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.parent / (path.name + ".tmp")
    tmp.write_text(text, encoding="utf-8")
    os.chmod(tmp, mode)
    os.replace(tmp, path)


def read_json(path: pathlib.Path, default: Any = None) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return default


def write_json(path: pathlib.Path, obj: Any, mode: int = 0o600) -> None:
    atomic_write_text(path, json.dumps(obj, ensure_ascii=False, indent=2), mode)


def read_jsonl(path: pathlib.Path) -> list[dict]:
    out: list[dict] = []
    try:
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                item = json.loads(line)
            except ValueError:
                continue
            if isinstance(item, dict):
                out.append(item)
    except OSError:
        pass
    return out


def append_jsonl(path: pathlib.Path, obj: dict, mode: int = 0o600) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    is_new = not path.exists()
    with path.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps(obj, ensure_ascii=False) + "\n")
    if is_new:
        os.chmod(path, mode)


# ---------------------------------------------------------------- 系统交互

def open_in_browser(url: str) -> bool:
    """用系统默认浏览器打开 URL（本工具只在登录时唤起浏览器）。"""
    try:
        if sys.platform == "darwin":
            return subprocess.run(["open", url], capture_output=True, timeout=20).returncode == 0
        if os.name == "nt":
            os.startfile(url)  # type: ignore[attr-defined]
            return True
        return subprocess.run(["xdg-open", url], capture_output=True, timeout=20).returncode == 0
    except Exception:  # noqa: BLE001
        return False


def copy_to_clipboard(text: str) -> bool:
    """把文本复制到剪贴板（登录链接兜底用）。"""
    try:
        if sys.platform == "darwin":
            cmd: list[str] = ["pbcopy"]
        elif os.name == "nt":
            cmd = ["clip"]
        else:
            return False
        return subprocess.run(cmd, input=text.encode(), capture_output=True, timeout=10).returncode == 0
    except Exception:  # noqa: BLE001
        return False
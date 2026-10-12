"""配置读写与校验：``~/.checkin/config.json``（0600）。

- 叶子级点路径读写（如 ``notify.dingtalk.webhook``），CLI / Web UI 共用；
- 每次 load 都用 DEFAULTS 补全缺失键并校验：**非法值回退默认并记录警告**，
  脏配置永远不会让主流程崩溃（``checkin doctor`` 会列出警告）；
- 首次运行自动生成默认配置文件。
"""
from __future__ import annotations

import copy
import logging
import re

from checkin import paths
from checkin.util import mask_secret, mask_webhook, read_json, write_json

log = logging.getLogger(__name__)

DEFAULTS: dict = {
    "schedule": {"time": "08:00"},
    "expiry_warning_days": 7,
    "notify": {
        "dingtalk": {"enabled": True, "webhook": "", "secret": ""},
        "local": {"enabled": True},
        "daily_summary": False,
    },
    "products": {"trae": {"enabled": True}, "workbuddy": {"enabled": True}},
}

# ------------------------------------------------------------------ 校验器
# 输入是"用户可读"的值（CLI 传字符串，程序内可传原生类型），输出校验后的原生值。

_TIME_RE = re.compile(r"^(\d{1,2}):(\d{1,2})$")
_TRUE_WORDS = {"1", "true", "yes", "on", "y", "t", "enabled", "开", "是"}
_FALSE_WORDS = {"0", "false", "no", "off", "n", "f", "disabled", "关", "否"}


def _v_time(value) -> str:
    text = str(value).strip()
    match = _TIME_RE.match(text)
    if match:
        hour, minute = int(match.group(1)), int(match.group(2))
        if 0 <= hour <= 23 and 0 <= minute <= 59:
            return f"{hour:02d}:{minute:02d}"
    raise ValueError("时间格式应为 HH:MM（24 小时制，如 08:00）")


def _v_days(value) -> int:
    try:
        days = int(str(value).strip())
    except ValueError:
        raise ValueError("应为整数天数（0-365，0 表示关闭预警）") from None
    if not 0 <= days <= 365:
        raise ValueError("天数应在 0-365 之间（0 表示关闭预警）")
    return days


def _v_bool(value) -> bool:
    if isinstance(value, bool):
        return value
    text = str(value).strip().lower()
    if text in _TRUE_WORDS:
        return True
    if text in _FALSE_WORDS:
        return False
    raise ValueError("应为布尔值（true/false、yes/no、1/0、开/关）")


def _v_webhook(value) -> str:
    text = str(value).strip()
    if not text:
        return ""
    if not text.startswith(("http://", "https://")):
        raise ValueError("webhook 应以 http:// 或 https:// 开头")
    return text


def _v_secret(value) -> str:
    text = str(value).strip()
    if not text:
        return ""
    if len(text) < 8:
        raise ValueError("加签 secret 长度不足 8 位（请检查是否复制完整）")
    return text


VALIDATORS: dict[str, object] = {
    "schedule.time": _v_time,
    "expiry_warning_days": _v_days,
    "notify.dingtalk.enabled": _v_bool,
    "notify.dingtalk.webhook": _v_webhook,
    "notify.dingtalk.secret": _v_secret,
    "notify.local.enabled": _v_bool,
    "notify.daily_summary": _v_bool,
    "products.trae.enabled": _v_bool,
    "products.workbuddy.enabled": _v_bool,
}

_warnings: list[str] = []


# ------------------------------------------------------------------ 读写

def _merge(dst: dict, src: dict, warnings: list[str], prefix: str = "") -> None:
    """把 src（磁盘上的原始值）逐叶子合并进 dst（默认值副本），非法值回退默认。"""
    for key, default_value in dst.items():
        path = f"{prefix}{key}"
        if isinstance(default_value, dict):
            sub = src.get(key) if isinstance(src, dict) else None
            if isinstance(sub, dict):
                _merge(default_value, sub, warnings, path + ".")
            continue
        if not isinstance(src, dict) or key not in src:
            continue
        validator = VALIDATORS.get(path)
        try:
            dst[key] = validator(src[key]) if validator else src[key]
        except ValueError as exc:
            warnings.append(f"{path}: 值非法（{exc}），已回退默认值 {default_value!r}")


def load() -> dict:
    """读取配置（缺失自动生成；非法值回退默认并记录警告）。"""
    global _warnings
    path = paths.config_path()
    existed = path.exists()
    raw = read_json(path)

    warnings: list[str] = []
    cfg = copy.deepcopy(DEFAULTS)
    if isinstance(raw, dict):
        _merge(cfg, raw, warnings)
    elif raw is not None:
        warnings.append("config.json 顶层不是对象，已整体回退默认值")
    elif existed:
        warnings.append("config.json 解析失败（不是合法 JSON），已整体回退默认值")

    _warnings = warnings
    for item in warnings:
        log.warning("配置：%s", item)
    if not existed:
        save(cfg)
        log.info("已生成默认配置文件：%s", path)
    return cfg


def last_warnings() -> list[str]:
    """最近一次 ``load()`` 产生的回退警告（doctor 用）。"""
    return list(_warnings)


def save(cfg: dict) -> None:
    write_json(paths.config_path(), cfg, mode=0o600)


def get(path: str, default=None):
    """按点路径取配置值，如 ``get("notify.dingtalk.webhook")``。"""
    node = load()
    for part in path.split("."):
        if not isinstance(node, dict) or part not in node:
            return default
        node = node[part]
    return node


def set(path: str, raw) -> tuple[bool, str]:  # noqa: A001 - 与 get 对称的命名
    """写入单个配置项（先校验后落盘）；返回 ``(是否成功, 提示)``。"""
    validator = VALIDATORS.get(path)
    if validator is None:
        return False, f"未知配置项：{path}（可用：{', '.join(sorted(VALIDATORS))}）"
    try:
        value = validator(raw)
    except ValueError as exc:
        return False, f"{path} 值非法：{exc}"
    cfg = load()
    node = cfg
    parts = path.split(".")
    for part in parts[:-1]:
        node = node.setdefault(part, {})
    node[parts[-1]] = value
    save(cfg)
    return True, f"{path} = {display(path, value)}"


def display(path: str, value) -> str:
    """配置值的人类可读展示（webhook / secret 打码）。"""
    if path.endswith("webhook"):
        return mask_webhook(str(value))
    if path.endswith("secret"):
        return mask_secret(str(value))
    return str(value)


def masked_view() -> dict:
    """打码后的全量配置（UI / config show 用，不泄露明文）。"""
    cfg = load()
    view = copy.deepcopy(cfg)
    ding = view["notify"]["dingtalk"]
    ding["webhook"] = mask_webhook(ding.get("webhook"))
    ding["secret"] = mask_secret(ding.get("secret"))
    return view


# ------------------------------------------------------------------ 便捷读取

def schedule_time() -> str:
    return str(get("schedule.time", DEFAULTS["schedule"]["time"]) or "08:00")


def warning_days() -> int:
    try:
        return int(get("expiry_warning_days", DEFAULTS["expiry_warning_days"]))
    except (TypeError, ValueError):
        return int(DEFAULTS["expiry_warning_days"])


def dingtalk_ready() -> tuple[bool, str]:
    """钉钉通知是否可用；不可用时给出下一步指引。"""
    ding = get("notify.dingtalk", {}) or {}
    if not ding.get("enabled"):
        return False, "钉钉通知已关闭（notify.dingtalk.enabled=false）"
    if not ding.get("webhook"):
        return False, "未配置 webhook：checkin config set notify.dingtalk.webhook <URL>"
    if not ding.get("secret"):
        return False, "未配置加签 secret：checkin config set notify.dingtalk.secret <SECRET>"
    return True, "ok"
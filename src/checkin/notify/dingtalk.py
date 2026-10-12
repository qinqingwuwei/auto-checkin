"""钉钉机器人通知：webhook + 加签（算法见计划 §5.7）。

- 重试：网络错误与 5xx 指数退避（20s 起、最多 3 次、带抖动）；4xx 不重试（多为签名/配置错误）；
- 发送失败**不影响**签到主流程成败（通知是旁路，日志始终兜底）。
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import logging
import time
import urllib.parse

import httpx

from checkin import config
from checkin.net import request
from checkin.util import mask_webhook

log = logging.getLogger(__name__)

SIGN_ERROR_CODE = 310000


def sign(secret: str, timestamp: str) -> str:
    """钉钉加签：``quote_plus(base64(hmac_sha256(secret, f"{ts}\\n{secret}")))``。"""
    string_to_sign = f"{timestamp}\n{secret}"
    digest = hmac.new(
        secret.encode("utf-8"), string_to_sign.encode("utf-8"), hashlib.sha256
    ).digest()
    return urllib.parse.quote_plus(base64.b64encode(digest).decode("utf-8"))


def signed_url(webhook: str, secret: str = "", timestamp: str | None = None) -> str:
    """拼接带 timestamp 与 sign 的最终 URL（webhook 自带 query 时用 & 追加）。"""
    ts = str(timestamp if timestamp is not None else round(time.time() * 1000))
    if not secret:
        return webhook
    separator = "&" if "?" in webhook else "?"
    return f"{webhook}{separator}timestamp={ts}&sign={sign(secret, ts)}"


def payload(title: str, text: str) -> dict:
    return {"msgtype": "markdown", "markdown": {"title": title, "text": text}}


def send(
    title: str,
    text: str,
    *,
    webhook: str | None = None,
    secret: str | None = None,
    retries: int = 3,
) -> tuple[bool, str]:
    """发送 markdown 消息；返回 ``(是否成功, 说明)``。"""
    cfg_ding = config.get("notify.dingtalk", {}) or {}
    webhook = webhook if webhook is not None else (cfg_ding.get("webhook") or "")
    secret = secret if secret is not None else (cfg_ding.get("secret") or "")
    if not webhook:
        return False, "未配置钉钉 webhook"
    url = signed_url(webhook, secret)
    try:
        with httpx.Client(timeout=20) as client:
            resp = request(
                client, "POST", url, json=payload(title, text),
                retries=retries, backoff=20.0, label="钉钉通知",
            )
    except httpx.HTTPError as exc:
        return False, f"钉钉发送失败（网络）：{type(exc).__name__}: {exc}"

    if resp.status_code != 200:
        return False, f"钉钉返回 HTTP {resp.status_code}：{resp.text[:200]}（{mask_webhook(webhook)}）"
    try:
        data = resp.json()
    except ValueError:
        return False, f"钉钉响应非 JSON：{resp.text[:200]}"
    errcode = data.get("errcode")
    if errcode == 0:
        return True, "ok"
    errmsg = str(data.get("errmsg") or "")
    if errcode == SIGN_ERROR_CODE:
        return False, f"钉钉加签校验失败（310000）：请核对 secret 与时间同步（errmsg={errmsg}）"
    return False, f"钉钉发送失败：errcode={errcode} errmsg={errmsg}"
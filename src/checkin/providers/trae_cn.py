"""Trae CN Provider：签到状态 / 领取 / 批次台账（接口见计划 §5.4，✅ 已实测）。

鉴权：``Authorization: Cloud-IDE-JWT <jwt>`` + ``X-User-Region: cn``（JWT 由
``credentials.trae`` 用网页会话 cookie 换取）。错误码：``9095`` 今日已签到（记成功）、
``9074`` 限流、``12153`` 票失效。
"""
from __future__ import annotations

import logging

import httpx

from checkin import config
from checkin.credentials import trae as trae_cred
from checkin.net import request
from checkin.providers.base import (
    AuthExpired,
    CheckinStatusResult,
    ClaimOutcome,
    PointBatch,
    PointsSnapshot,
    ProviderError,
    RateLimited,
)
from checkin.util import dig

log = logging.getLogger(__name__)

BASE = "https://api.trae.cn"
STATUS_URL = BASE + "/trae/api/v2/ug/checkin_credits/status"
CLAIM_URL = BASE + "/trae/api/v2/ug/checkin_credits/claim"
ENTS_URL = BASE + "/trae/api/v2/pay/user_current_entitlement_list"
EXPIRED_URL = BASE + "/trae/api/v2/pay/expired_ents"
BILLING_URL = BASE + "/trae/api/v2/pay/cn_credits_billing_status"

CLIENT_VERSION = "3.4.2"
CHECKIN_BODY = {"trae_client": "IDE", "req_source": 1, "client_version": CLIENT_VERSION}
CODE_ALREADY_CHECKED_IN = 9095
CODE_RATE_LIMITED = 9074
CODE_TOKEN_INVALID = 12153

_UA = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
)


class TraeCNProvider:
    name = "trae"
    label = "Trae CN"

    def __init__(self, jwt: str | None = None, timeout: float = 30.0) -> None:
        self._jwt = jwt
        self._client = httpx.Client(timeout=timeout, base_url=BASE)
        self._timeout = timeout

    # -------------------------------------------------------------- 内部
    def close(self) -> None:
        self._client.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def _token(self, force: bool = False) -> str:
        if self._jwt and not force:
            return self._jwt
        return trae_cred.jwt_token(force=force)

    def _post(self, url: str, body: dict, label: str, auth: bool = True) -> dict:
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/plain, */*",
            "Origin": "https://www.trae.cn",
            "Referer": "https://www.trae.cn/",
            "X-User-Region": "cn",
            "trae_client": "IDE",
            "User-Agent": _UA,
        }
        if auth:
            jwt = self._token()
            headers["Authorization"] = f"Cloud-IDE-JWT {jwt}"
        try:
            resp = request(self._client, "POST", url, headers=headers, json=body, label=label)
        except httpx.HTTPError as exc:
            raise ProviderError(f"{label} 网络错误：{type(exc).__name__}: {exc}") from exc

        if resp.status_code in (401, 403):
            raise AuthExpired(
                f"{label} 鉴权失败（HTTP {resp.status_code}）：凭据可能已失效，"
                "请运行 `checkin login trae`"
            )
        if resp.status_code != 200:
            raise ProviderError(f"{label} HTTP {resp.status_code}：{resp.text[:200]}")
        try:
            data = resp.json()
        except ValueError as exc:
            raise ProviderError(f"{label} 响应非 JSON：{resp.text[:200]}") from exc
        if not isinstance(data, dict):
            raise ProviderError(f"{label} 响应结构异常")
        return data

    def _check_code(self, data: dict, label: str) -> None:
        code = data.get("code")
        msg = str(data.get("msg") or data.get("message") or "")
        if code in (None, 0, CODE_ALREADY_CHECKED_IN):
            return
        if code == CODE_TOKEN_INVALID:
            raise AuthExpired(f"{label} 票失效（12153），请运行 `checkin login trae`")
        if code == CODE_RATE_LIMITED:
            raise RateLimited(f"{label} 被限流（9074）：请稍后重试")
        raise ProviderError(f"{label} 失败：code={code} msg={msg or data}")

    # -------------------------------------------------------------- 业务
    def status(self) -> CheckinStatusResult:
        data = self._post(STATUS_URL, CHECKIN_BODY, "Trae 签到状态")
        self._check_code(data, "Trae 签到状态")
        checked = bool(data.get("checked_in")) or bool(data.get("did_checked_in")) or data.get("code") == CODE_ALREADY_CHECKED_IN
        credits = data.get("credits")
        extra = data.get("extra_credits")
        credits_today = None
        if isinstance(credits, (int, float)):
            credits_today = float(credits) + float(extra or 0)
        return CheckinStatusResult(
            checked_in=checked,
            did_checked_in=bool(data.get("did_checked_in")),
            credits_today=credits_today,
            message=f"checked_in={checked} enable={data.get('enable')}",
        )

    def claim(self) -> ClaimOutcome:
        data = self._post(CLAIM_URL, CHECKIN_BODY, "Trae 签到")
        code = data.get("code")
        if code == CODE_ALREADY_CHECKED_IN:
            return ClaimOutcome(success=True, already=True, message="今日已签到（9095）")
        msg = str(data.get("msg") or data.get("message") or "")
        if code in (None, 0):
            credits = data.get("credits")
            return ClaimOutcome(
                success=True,
                credits=float(credits) if isinstance(credits, (int, float)) else None,
                message="签到成功",
            )
        if "已签" in msg:
            return ClaimOutcome(success=True, already=True, message=f"今日已签到（{msg}）")
        self._check_code(data, "Trae 签到")
        raise ProviderError(f"Trae 签到失败：code={code} msg={msg}")  # pragma: no cover

    def fetch_points(self) -> PointsSnapshot:
        """批次台账：``user_current_entitlement_list``（逐批 limit/used/expire_time）。"""
        data = self._post(
            ENTS_URL, {"require_usage": True, "full_data": True}, "Trae 积分台账"
        )
        packs = data.get("user_entitlement_pack_list")
        if packs is None:
            packs = dig(data, ["user_entitlement_pack_list", "entitlement_pack_list"]) or []
        summary = data.get("usage_summary") or {}
        batches: list[PointBatch] = []
        for pack in packs if isinstance(packs, list) else []:
            if not isinstance(pack, dict):
                continue
            base = pack.get("entitlement_base_info") or {}
            expire = pack.get("expire_time")
            if not isinstance(expire, (int, float)):
                expire = base.get("end_time") if isinstance(base.get("end_time"), (int, float)) else None
            limit = (pack.get("quota") or base.get("quota") or {}).get("credits_limit")
            used = (pack.get("usage") or {}).get("credits_amount") or 0
            limit = float(limit or 0)
            used = float(used or 0)
            batches.append(
                PointBatch(
                    remain=round(limit - used, 4),
                    expire=float(expire) if expire else None,
                    limit=limit,
                    used=used,
                    name=str(pack.get("entitlement_id") or base.get("entitlement_id") or ""),
                )
            )

        total = summary.get("total_amount")
        consumed = summary.get("consumed_amount")
        if isinstance(total, (int, float)):
            balance = float(total) - float(consumed or 0)
        else:
            balance = sum(b.remain for b in batches if b.remain > 0)
        return PointsSnapshot(
            balance=round(float(balance), 2),
            total_limit=float(total) if isinstance(total, (int, float)) else None,
            total_used=float(consumed) if isinstance(consumed, (int, float)) else None,
            batches=batches,
            source=ENTS_URL,
        )

    def fetch_expired(self) -> list[dict]:
        """最近过期记录（status 展示用，只读）。"""
        data = self._post(EXPIRED_URL, {}, "Trae 过期记录")
        items = data.get("expired_ent_list") or []
        out = []
        for item in items if isinstance(items, list) else []:
            if not isinstance(item, dict):
                continue
            expire_ms = item.get("expire_time_ms") or item.get("expire_time")
            expire = float(expire_ms) / 1000 if isinstance(expire_ms, (int, float)) and expire_ms > 10**10 else expire_ms
            out.append(
                {
                    "name": item.get("name") or "-",
                    "credits": item.get("credits_limit"),
                    "expire": expire,
                }
            )
        return out

    def billing_enabled(self) -> bool | None:
        data = self._post(BILLING_URL, {}, "Trae billing 状态")
        value = data.get("enable")
        return bool(value) if isinstance(value, bool) else None


def warning_days() -> int:
    return config.warning_days()
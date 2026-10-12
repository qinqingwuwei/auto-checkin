"""WorkBuddy Provider：签到状态 / 领取 / 批次台账（接口见计划 §7.4，✅ 已实测）。

鉴权：``Authorization: Bearer <AT>`` + ``X-User-Id``；billing host 依次尝试
``www.codebuddy.cn`` → ``www.workbuddy.cn``。"今日已签到"判定：业务 ``code==10001``
或 msg 含"已签到 / already / repeat"（记为成功而非失败）。
"""
from __future__ import annotations

import logging

import httpx

from checkin.credentials import workbuddy as wb_cred
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

BILLING_HOSTS = ("https://www.codebuddy.cn", "https://www.workbuddy.cn")
STATUS_PATH = "/v2/billing/meter/checkin-activity-status"
CLAIM_PATH = "/v2/billing/meter/daily-checkin"
RESOURCE_PATH = "/v2/billing/meter/get-user-resource"
PRODUCT_CODE = "p_tcaca"
UA = "WorkBuddy/5.5.6"

_RATE_WORDS = ("频繁", "限流", "too many", "rate limit")
_ALREADY_WORDS = ("已签到", "already", "repeat")
_LOGIN_WORDS = ("未登录", "登录失效", "重新登录", "unauthorized")


class WorkBuddyProvider:
    name = "workbuddy"

    def __init__(self, cred: dict | None = None, timeout: float = 30.0) -> None:
        raw = cred or wb_cred.load()
        if not raw:
            raise AuthExpired("未登录 WorkBuddy：运行 `checkin login workbuddy --method browser|sms`")
        self.cred = wb_cred.ensure_fresh(raw)
        self._client = httpx.Client(timeout=timeout)
        self._host = BILLING_HOSTS[0]

    @property
    def uid(self) -> str:
        return str(self.cred.get("uid") or "-")

    @property
    def label(self) -> str:
        return f"WorkBuddy（{wb_cred.short_uid(self.cred.get('uid'))}）"

    def close(self) -> None:
        self._client.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    # -------------------------------------------------------------- 内部
    def _headers(self) -> dict:
        return {
            "Authorization": f"Bearer {self.cred['accessToken']}",
            "X-User-Id": self.uid,
            "User-Agent": UA,
            "Accept": "application/json, text/plain, */*",
            "Origin": "https://www.codebuddy.cn",
            "Referer": "https://www.codebuddy.cn/",
            "Content-Type": "application/json",
        }

    def _post(self, path: str, body: dict, label: str) -> dict:
        hosts = [self._host] + [h for h in BILLING_HOSTS if h != self._host]
        last_error: Exception | None = None
        for host in hosts:
            try:
                resp = request(
                    self._client, "POST", host + path, headers=self._headers(), json=body,
                    label=f"{label}（{host}）",
                )
            except httpx.HTTPError as exc:
                last_error = ProviderError(f"{label} 网络错误：{type(exc).__name__}: {exc}")
                continue
            if resp.status_code in (401, 403):
                raise AuthExpired(
                    f"{label} 鉴权失败（HTTP {resp.status_code}）："
                    "请运行 `checkin login workbuddy` 重新登录"
                )
            if resp.status_code != 200:
                last_error = ProviderError(f"{label} HTTP {resp.status_code}：{resp.text[:200]}")
                continue
            try:
                data = resp.json()
            except ValueError:
                last_error = ProviderError(f"{label} 响应非 JSON：{resp.text[:200]}")
                continue
            if not isinstance(data, dict):
                last_error = ProviderError(f"{label} 响应结构异常")
                continue
            self._host = host
            return data
        if isinstance(last_error, ProviderError):
            raise last_error
        raise ProviderError(f"{label} 全部 host 失败：{last_error}")

    def _check_failure(self, data: dict, label: str) -> str:
        """非成功码时分类抛异常；返回 msg 文本。"""
        code = data.get("code")
        msg = str(data.get("msg") or data.get("message") or "")
        if code in (None, 0):
            return msg
        if any(w in msg.lower() for w in _LOGIN_WORDS):
            raise AuthExpired(f"{label} 失败：{msg}（请重新登录：checkin login workbuddy）")
        if any(w in msg.lower() for w in _RATE_WORDS):
            raise RateLimited(f"{label} 被限流：{msg}")
        raise ProviderError(f"{label} 失败：code={code} msg={msg or data}")

    # -------------------------------------------------------------- 业务
    def status(self) -> CheckinStatusResult:
        data = self._post(STATUS_PATH, {}, "WorkBuddy 签到状态")
        msg = self._check_failure(data, "WorkBuddy 签到状态")
        payload = data.get("data") if isinstance(data.get("data"), dict) else data
        checked_raw = dig(payload, ["today_checked_in", "checked_in", "todayCheckedIn"])
        checked = bool(checked_raw) if checked_raw is not None else False
        streak = dig(payload, ["streak_days", "streakDays", "continuous_days"])
        credits = dig(payload, ["today_credit", "todayCredit", "credit"])
        return CheckinStatusResult(
            checked_in=checked,
            credits_today=float(credits) if isinstance(credits, (int, float)) else None,
            streak_days=int(streak) if isinstance(streak, (int, float)) else None,
            message=msg or ("今日已签到" if checked else "今日未签到"),
        )

    def claim(self) -> ClaimOutcome:
        data = self._post(CLAIM_PATH, {}, "WorkBuddy 签到")
        code = data.get("code")
        msg = str(data.get("msg") or data.get("message") or "")
        if code == 10001 or any(w in msg.lower() for w in _ALREADY_WORDS):
            return ClaimOutcome(success=True, already=True, message=f"今日已签到（{msg or code}）")
        self._check_failure(data, "WorkBuddy 签到")
        payload = data.get("data") if isinstance(data.get("data"), dict) else {}
        credit = dig(payload, ["credit", "credits", "credit_amount"])
        return ClaimOutcome(
            success=True,
            credits=float(credit) if isinstance(credit, (int, float)) else None,
            message=msg or "签到成功",
        )

    def fetch_points(self) -> PointsSnapshot:
        """批次台账：``get-user-resource``（逐包 CapacityRemain / DeductionEndTime）。"""
        data = self._post(
            RESOURCE_PATH,
            {"PageNumber": 1, "PageSize": 100, "ProductCode": PRODUCT_CODE},
            "WorkBuddy 积分台账",
        )
        self._check_failure(data, "WorkBuddy 积分台账")
        payload = data.get("data") if isinstance(data.get("data"), dict) else {}
        response = payload.get("Response") if isinstance(payload.get("Response"), dict) else payload
        data_node = response.get("Data") if isinstance(response.get("Data"), dict) else response
        accounts = dig(data_node, ["Accounts"]) or []
        batches: list[PointBatch] = []
        for item in accounts if isinstance(accounts, list) else []:
            if not isinstance(item, dict):
                continue
            remain = item.get("CapacityRemain") or item.get("Remain") or 0
            if not isinstance(remain, (int, float)):
                remain = 0
            expire = None
            for key in ("DeductionEndTime", "ExpiredTime", "PackageEndTime", "CycleEndTime"):
                value = item.get(key)
                if isinstance(value, (int, float)) and value > 10**10:
                    expire = float(value) / 1000
                    break
            batches.append(
                PointBatch(
                    remain=float(remain),
                    expire=expire,
                    limit=float(item["CapacitySize"]) if isinstance(item.get("CapacitySize"), (int, float)) else None,
                    used=float(item["CapacityUsed"]) if isinstance(item.get("CapacityUsed"), (int, float)) else None,
                    name=str(item.get("PackageName") or item.get("DealName") or ""),
                )
            )
        total_raw = dig(data_node, ["TotalDosage"])
        balance = float(total_raw) if isinstance(total_raw, (int, float)) else round(
            sum(b.remain for b in batches if b.remain > 0), 2
        )
        return PointsSnapshot(
            balance=round(balance, 2),
            total_limit=None,
            total_used=None,
            batches=batches,
            source=RESOURCE_PATH,
        )
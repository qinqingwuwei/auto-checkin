"""Provider 公共协议：异常分类 + 结果数据类。

异常分类决定上层行为（计划 §5.2）：
- ``AuthExpired``：凭据失效 → 不重试业务请求，直接告警并指引重新登录；
- ``RateLimited``：被限流/风控（Trae ``9074``）→ 上层退避后最多再试一次；
- ``ProviderError``：其他业务失败 → 记为异常并通知。
"""
from __future__ import annotations

from dataclasses import dataclass, field


class ProviderError(Exception):
    """业务失败（message 面向用户，可直接进通知）。"""


class AuthExpired(ProviderError):
    """凭据失效，需要重新登录。"""


class RateLimited(ProviderError):
    """被限流/风控，可稍后重试。"""


@dataclass
class CheckinStatusResult:
    checked_in: bool  # 今日是否已签到
    did_checked_in: bool = False  # 本次 status 调用是否顺带完成了签到
    credits_today: float | None = None  # 今日（可/已）获得积分
    streak_days: int | None = None  # 连签天数（若接口提供）
    message: str = ""


@dataclass
class ClaimOutcome:
    success: bool
    already: bool = False  # "今日已签到"同样记为成功，绝不重复领取
    credits: float | None = None
    message: str = ""


@dataclass
class PointBatch:
    """一个积分批次（逐批独立到期）。"""

    remain: float
    expire: float | None = None  # 到期时刻（epoch 秒）；None 表示无到期日
    limit: float | None = None
    used: float | None = None
    name: str = ""


@dataclass
class PointsSnapshot:
    balance: float
    total_limit: float | None = None
    total_used: float | None = None
    batches: list[PointBatch] = field(default_factory=list)
    source: str = ""  # 数据来源接口，便于排障
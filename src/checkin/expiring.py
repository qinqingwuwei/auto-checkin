"""即将过期积分：批次窗口汇总（纯函数，便于单测）。

数据来源是两侧产品接口直读的**逐批台账**（计划 §5.6 主路径），不做任何估算：
Trae = ``user_current_entitlement_list``、WorkBuddy = ``get-user-resource``。
"""
from __future__ import annotations

from checkin.providers.base import PointBatch

SECONDS_PER_DAY = 86400
DEFAULT_WINDOW_DAYS = 7
LONG_WINDOW_DAYS = 30


def in_window(expire: float | None, now: float, days: int) -> bool:
    """到期时刻是否落在 ``(now, now + days 天]`` 内；``days <= 0`` 表示关闭预警。"""
    if not expire or days <= 0:
        return False
    return now < expire <= now + days * SECONDS_PER_DAY


def days_left(expire: float | None, now: float) -> float | None:
    """距到期还有多少天（已过期返回负数；无到期日返回 None）。"""
    if not expire:
        return None
    return (expire - now) / SECONDS_PER_DAY


def valid_batches(batches: list[PointBatch], now: float) -> list[PointBatch]:
    """仍在期限内（或无到期日）且剩余为正的批次。"""
    return [
        batch
        for batch in batches
        if batch.remain and batch.remain > 0 and (batch.expire is None or batch.expire > now)
    ]


def summarize(
    batches: list[PointBatch], now: float, days: int = DEFAULT_WINDOW_DAYS
) -> dict:
    """汇总批次台账，输出统一的到期口径。

    返回键：``days`` / ``expiring_amount``（窗口内将过期） / ``expiring_30d`` /
    ``expiring_count`` / ``earliest``（窗口内最早到期时刻，epoch 秒） /
    ``batch_count``（有效批次数） / ``total_remain``（有效批次剩余合计）。
    """
    valid = valid_batches(batches, now)
    expiring = [b for b in valid if in_window(b.expire, now, days)]
    return {
        "days": days,
        "expiring_amount": round(sum(b.remain for b in expiring), 2),
        "expiring_30d": round(
            sum(b.remain for b in valid if in_window(b.expire, now, LONG_WINDOW_DAYS)), 2
        ),
        "expiring_count": len(expiring),
        "earliest": min((b.expire for b in expiring if b.expire), default=None),
        "batch_count": len(valid),
        "total_remain": round(sum(b.remain for b in valid), 2),
    }
"""到期窗口计算单测（纯函数，接口直读批次口径）。"""
from __future__ import annotations

from checkin.expiring import days_left, in_window, summarize, valid_batches
from checkin.providers.base import PointBatch

NOW = 1_800_000_000.0  # 固定"现在"，避免测试随时钟漂移
DAY = 86400


def batch(days: float, remain: float) -> PointBatch:
    """到期时刻 = NOW + days 天的批次。"""
    return PointBatch(remain=remain, expire=NOW + days * DAY)


def test_in_window_boundaries():
    assert in_window(NOW + 3 * DAY, NOW, 7)
    assert in_window(NOW + 7 * DAY, NOW, 7)  # 上界闭区间
    assert not in_window(NOW, NOW, 7)  # 恰好到点不算（已过期）
    assert not in_window(NOW + 7 * DAY + 1, NOW, 7)
    assert not in_window(NOW + 1 * DAY, NOW, 0)  # 0 天 = 关闭预警
    assert not in_window(None, NOW, 7)
    assert not in_window(NOW + DAY, NOW, -1)


def test_summarize_mixed_batches():
    batches = [batch(3, 100), batch(6, 200.5), batch(20, 300), batch(40, 400), batch(-1, 999)]
    summary = summarize(batches, NOW, 7)
    assert summary["expiring_amount"] == 300.5
    assert summary["expiring_30d"] == 600.5
    assert summary["expiring_count"] == 2
    assert summary["earliest"] == NOW + 3 * DAY
    assert summary["batch_count"] == 4  # 已过期批次不计
    assert summary["total_remain"] == 1000.5


def test_summarize_ignores_zero_and_negative_remain():
    batches = [
        PointBatch(remain=0, expire=NOW + DAY),
        PointBatch(remain=-5, expire=NOW + DAY),
        batch(-2, 50),
    ]
    summary = summarize(batches, NOW, 7)
    assert summary["expiring_amount"] == 0
    assert summary["total_remain"] == 0
    assert summary["batch_count"] == 0
    assert summary["earliest"] is None


def test_no_expiry_batch_is_valid_but_never_expiring():
    summary = summarize([PointBatch(remain=10, expire=None)], NOW, 7)
    assert summary["total_remain"] == 10
    assert summary["batch_count"] == 1
    assert summary["expiring_amount"] == 0


def test_window_disabled_returns_zero():
    summary = summarize([batch(1, 500)], NOW, 0)
    assert summary["expiring_amount"] == 0
    assert summary["expiring_30d"] == 500  # 30 天口径不受预警开关影响
    assert summary["total_remain"] == 500


def test_valid_batches_filters():
    batches = [batch(1, 5), batch(-1, 5), PointBatch(remain=0, expire=None)]
    assert len(valid_batches(batches, NOW)) == 1


def test_days_left():
    assert days_left(NOW + 2 * DAY, NOW) == 2
    assert days_left(NOW - DAY, NOW) == -1
    assert days_left(None, NOW) is None
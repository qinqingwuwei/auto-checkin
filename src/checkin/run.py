"""一次完整执行的主流程：签到 → 批次台账 → 到期汇总 → 通知 → 落盘。

- **幂等**（计划 §5.3）：``state.json`` 记录每产品最后一次成功日期，今天已成功则跳过
  （``--force`` 强制重跑、``--dry-run`` 仅预览）；签到前必查 status，已签到记为成功，
  **绝不重复触发 claim**；
- **dry-run**：完整走一遍但**不 claim、不落 state / points、不发通知**，只打印快照预览；
- 通知仅在异常 / 到期预警 / 台账失败 / （可选的）每日成功简报时发送，通知失败不影响成败判定。
"""
from __future__ import annotations

import json
import logging
import time
from dataclasses import dataclass, field

from checkin import config, paths
from checkin.credentials import trae as trae_cred
from checkin.credentials import workbuddy as wb_cred
from checkin.credentials.base import CredentialError
from checkin.expiring import summarize
from checkin.notify import dispatcher
from checkin.notify.dispatcher import Notification
from checkin.providers import trae_cn
from checkin.providers import workbuddy as wb_provider
from checkin.providers.base import AuthExpired, ProviderError, RateLimited
from checkin.util import append_jsonl, jwt_payload, read_json, today_str, write_json

log = logging.getLogger(__name__)

STATE_VERSION = 1
RATE_LIMIT_RETRY_WAIT = 30  # 9074 等限流：等 30 秒重试一次


@dataclass
class ItemResult:
    key: str
    label: str
    account: str = "-"
    success: bool = False  # 签到目标达成（含"今日已签到"）
    already: bool = False  # 今日已签到（跳过 claim）
    skipped: bool = False  # 因幂等跳过
    error: bool = False  # 需要用户注意（会进通知）
    auth_expired: bool = False  # 凭据失效（给重新登录指引）
    message: str = ""
    credit_today: float | None = None
    balance: float | None = None
    batch_count: int | None = None
    expiring_days: int | None = None
    expiring: float | None = None
    expiring_30d: float | None = None
    points: dict | None = None


@dataclass
class RunReport:
    date: str
    dry_run: bool = False
    items: list[ItemResult] = field(default_factory=list)
    started_at: float = field(default_factory=time.time)
    finished_at: float | None = None

    @property
    def failed(self) -> list[ItemResult]:
        return [item for item in self.items if item.error]

    @property
    def ok(self) -> bool:
        return not self.failed


# ------------------------------------------------------------------ 对外入口

def execute(
    dry_run: bool = False, force: bool = False, only: list[str] | None = None
) -> RunReport:
    """执行一次签到流程并返回报告（CLI / Web UI 共用）。"""
    cfg = config.load()
    days = int(cfg["expiry_warning_days"])
    only_set = {x.lower() for x in (only or [])}
    report = RunReport(date=today_str(), dry_run=dry_run)
    state = _load_state()
    log.info(
        "===== 开始执行（%s%s）=====",
        report.date,
        "，dry-run" if dry_run else ("，--force" if force else ""),
    )

    if cfg["products"]["trae"]["enabled"] and (not only_set or "trae" in only_set):
        report.items.append(_run_trae(state, dry_run=dry_run, force=force, days=days))
    if cfg["products"]["workbuddy"]["enabled"] and (not only_set or "workbuddy" in only_set):
        report.items.extend(_run_workbuddy_all(state, dry_run=dry_run, force=force, days=days))

    report.finished_at = time.time()
    if not dry_run:
        _save_state(state)

    note = _compose_notification(report, cfg, days)
    if note is not None:
        if dry_run:
            log.info("dry-run：以下通知不会真实发送\n%s", "\n".join(note.lines))
        else:
            dispatcher.dispatch(note)
    else:
        log.info("无需通知（无异常、无预警，且每日简报未开启）")
    return report


# ------------------------------------------------------------------ Trae

def _run_trae(state: dict, dry_run: bool, force: bool, days: int) -> ItemResult:
    item = ItemResult(key="trae", label="Trae CN")
    skip = _skip_reason(state, "trae", force, dry_run)
    if skip:
        item.skipped = item.success = True
        item.message = skip
        log.info("Trae CN：%s", skip)
        return item

    provider = None
    try:
        provider = trae_cn.TraeCNProvider()
        item.account = _trae_account()
        status = provider.status()
        if status.checked_in:
            item.success = item.already = True
            item.credit_today = status.credits_today
            item.message = "今日已签到（跳过 claim）"
        elif dry_run:
            item.success = True
            item.message = "dry-run：今日未签到，正式执行时将调用 claim"
        else:
            outcome = _claim_with_retry(provider, "Trae CN")
            item.success = outcome.success
            item.already = outcome.already
            item.credit_today = outcome.credits
            item.message = outcome.message
        log.info("Trae CN 签到：%s", item.message)
        _fetch_points(item, provider, days, dry_run)
    except AuthExpired as exc:
        _mark_auth_error(item, exc)
    except RateLimited as exc:
        _mark_error(item, f"被限流：{exc}（可稍后手动重跑 `checkin run --only trae`）")
    except CredentialError as exc:
        _mark_auth_error(item, exc)
    except ProviderError as exc:
        _mark_error(item, str(exc))
    except Exception as exc:  # noqa: BLE001 - 单产品失败不拖垮整体
        log.exception("Trae CN 执行异常")
        _mark_error(item, f"未预期异常：{type(exc).__name__}: {exc}")
    finally:
        if provider is not None:
            provider.close()

    _finalize(item, state, dry_run=dry_run)
    return item


def _trae_account() -> str:
    """从换票 JWT 的 ``data.id``（网页会话 JWT 的用户标识）取账号名。"""
    cred = trae_cred.load() or {}
    payload = jwt_payload(str(cred.get("jwt") or ""))
    data = payload.get("data") if isinstance(payload.get("data"), dict) else {}
    return str(data.get("id") or data.get("user_id") or data.get("tid") or "-")


# ------------------------------------------------------------------ WorkBuddy（多账号）

def _run_workbuddy_all(state: dict, dry_run: bool, force: bool, days: int) -> list[ItemResult]:
    accounts = wb_cred.load_all()
    if not accounts:
        item = ItemResult(key="workbuddy", label="WorkBuddy", auth_expired=True)
        _mark_error(
            item,
            "未登录：运行 `checkin login workbuddy --method browser`（微信扫码）"
            "或 `--method sms --phone <手机号>`",
        )
        return [item]
    return [
        _run_workbuddy(cred, state, dry_run=dry_run, force=force, days=days)
        for cred in accounts
    ]


def _run_workbuddy(cred: dict, state: dict, dry_run: bool, force: bool, days: int) -> ItemResult:
    uid = str(cred.get("uid") or "unknown")
    key = f"workbuddy:{uid}"
    item = ItemResult(key=key, label=f"WorkBuddy（{wb_cred.short_uid(uid)}）", account=uid)
    skip = _skip_reason(state, key, force, dry_run)
    if skip:
        item.skipped = item.success = True
        item.message = skip
        log.info("%s：%s", item.label, skip)
        return item

    provider = None
    try:
        provider = wb_provider.WorkBuddyProvider(cred)
        status = provider.status()
        if status.checked_in:
            item.success = item.already = True
            item.credit_today = status.credits_today
            streak = f"（连签 {status.streak_days} 天）" if status.streak_days else ""
            item.message = f"今日已签到{streak}（跳过 claim）"
        elif dry_run:
            item.success = True
            item.message = "dry-run：今日未签到，正式执行时将调用 claim"
        else:
            outcome = _claim_with_retry(provider, item.label)
            item.success = outcome.success
            item.already = outcome.already
            item.credit_today = outcome.credits
            item.message = outcome.message
        log.info("%s 签到：%s", item.label, item.message)
        _fetch_points(item, provider, days, dry_run)
    except AuthExpired as exc:
        _mark_auth_error(item, exc)
    except RateLimited as exc:
        _mark_error(item, f"被限流：{exc}（可稍后手动重跑）")
    except CredentialError as exc:
        _mark_auth_error(item, exc)
    except ProviderError as exc:
        _mark_error(item, str(exc))
    except Exception as exc:  # noqa: BLE001
        log.exception("%s 执行异常", item.label)
        _mark_error(item, f"未预期异常：{type(exc).__name__}: {exc}")
    finally:
        if provider is not None:
            provider.close()

    _finalize(item, state, dry_run=dry_run)
    return item


# ------------------------------------------------------------------ 公共步骤

def _claim_with_retry(provider, label: str):
    try:
        return provider.claim()
    except RateLimited as exc:
        log.warning("%s 被限流（%s），%d 秒后重试一次", label, exc, RATE_LIMIT_RETRY_WAIT)
        time.sleep(RATE_LIMIT_RETRY_WAIT)
        return provider.claim()


def _fetch_points(item: ItemResult, provider, days: int, dry_run: bool) -> None:
    """批次台账 + 到期汇总（失败只标记异常，不影响已完成的签到）。"""
    try:
        snapshot = provider.fetch_points()
    except (AuthExpired, ProviderError) as exc:
        item.error = True
        item.message = f"{item.message}；积分台账获取失败：{exc}"
        log.error("%s 积分台账获取失败：%s", item.label, exc)
        return
    now = time.time()
    summary = summarize(snapshot.batches, now, days)
    item.balance = snapshot.balance
    item.batch_count = summary["batch_count"]
    item.expiring_days = days
    item.expiring = summary["expiring_amount"]
    item.expiring_30d = summary["expiring_30d"]
    item.points = {
        "balance": snapshot.balance,
        "total_limit": snapshot.total_limit,
        "total_used": snapshot.total_used,
        "batch_count": summary["batch_count"],
        "expiring_days": days,
        "expiring_amount": summary["expiring_amount"],
        "expiring_30d": summary["expiring_30d"],
        "earliest_expire": summary["earliest"],
    }
    log.info(
        "%s 积分：余额 %s，批次 %d，%d 天内到期 %s，30 天内到期 %s",
        item.label, snapshot.balance, summary["batch_count"], days,
        summary["expiring_amount"], summary["expiring_30d"],
    )
    if dry_run:
        log.info("dry-run 快照预览：%s", json.dumps(item.points, ensure_ascii=False))


def _skip_reason(state: dict, key: str, force: bool, dry_run: bool) -> str | None:
    if force or dry_run:
        return None
    record = (state.get("products") or {}).get(key) or {}
    if record.get("successDate") == today_str():
        return "今日已成功执行，跳过（--force 可强制重跑）"
    return None


def _mark_error(item: ItemResult, message: str) -> None:
    item.error = True
    item.message = message
    log.error("%s：%s", item.label, message)


def _mark_auth_error(item: ItemResult, exc: Exception) -> None:
    item.auth_expired = True
    _mark_error(item, str(exc) or "凭据失效，请重新登录")


def _finalize(item: ItemResult, state: dict, dry_run: bool) -> None:
    """落 state + points（dry-run 只打印）。"""
    if dry_run:
        return
    products = state.setdefault("products", {})
    record = products.get(item.key) or {}
    if item.success and not item.skipped:
        record["successDate"] = today_str()
    record.update(
        {
            "status": "error" if item.error else ("skip" if item.skipped else "ok"),
            "at": time.strftime("%Y-%m-%d %H:%M:%S"),
            "message": item.message,
            "creditToday": item.credit_today,
            "balance": item.balance,
        }
    )
    products[item.key] = record
    state["updatedAt"] = time.strftime("%Y-%m-%d %H:%M:%S")

    if item.points is not None and item.success:
        append_jsonl(
            paths.points_path(),
            {
                "ts": int(time.time()),
                "date": today_str(),
                "product": "trae" if item.key == "trae" else "workbuddy",
                "account": item.account,
                "credit_today": item.credit_today,
                **item.points,
            },
        )


def _load_state() -> dict:
    data = read_json(paths.state_path(), {}) or {}
    data.setdefault("version", STATE_VERSION)
    data.setdefault("products", {})
    return data


def _save_state(state: dict) -> None:
    write_json(paths.state_path(), state, mode=0o600)


# ------------------------------------------------------------------ 通知

def _compose_notification(report: RunReport, cfg: dict, days: int) -> Notification | None:
    problems = [item for item in report.items if item.error]
    warnings = [
        item
        for item in report.items
        if not item.error and days > 0 and (item.expiring or 0) > 0
    ]
    if problems:
        title, level = f"签到异常（{len(problems)} 项）", "error"
    elif warnings:
        title, level = "积分到期预警", "warn"
    elif cfg["notify"]["daily_summary"]:
        title, level = "签到成功简报", "info"
    else:
        return None
    return Notification(title=title, lines=[_describe_item(item) for item in report.items], level=level)


def _describe_item(item: ItemResult) -> str:
    if item.error:
        tag = "异常"
    elif item.skipped:
        tag = "跳过"
    else:
        tag = "完成"
    text = f"[{tag}] {item.label}：{item.message or '成功'}"
    bits = []
    if item.credit_today:
        bits.append(f"今日 +{item.credit_today:g}")
    if item.balance is not None:
        bits.append(f"余额 {item.balance:g}")
    if item.expiring:
        bits.append(f"{item.expiring_days} 天内将过期 {item.expiring:g}")
    if item.expiring_30d:
        bits.append(f"30 天内将到期 {item.expiring_30d:g}")
    if bits:
        text += "（" + "，".join(bits) + "）"
    return text
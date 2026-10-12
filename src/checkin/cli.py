"""CLI：``checkin run / status / doctor / login / config / schedule / ui``。"""
from __future__ import annotations

import argparse
import json
import logging
import sys

from checkin import __version__, config, paths
from checkin.credentials import trae as trae_cred
from checkin.credentials import workbuddy as wb_cred
from checkin.credentials.base import CredentialError
from checkin.log import setup_logging
from checkin.scheduler import launchd
from checkin.util import fmt_remain, fmt_ts, read_json, read_jsonl, today_str

log = logging.getLogger("checkin.cli")

OK, WARN, FAIL = "OK", "WARN", "FAIL"


# ------------------------------------------------------------------ run

def _cmd_run(args: argparse.Namespace) -> int:
    from checkin import run as runner

    report = runner.execute(
        dry_run=args.dry_run, force=args.force, only=args.only or None
    )
    print()
    print(f"== 执行结果（{report.date}{'，dry-run' if report.dry_run else ''}）==")
    for item in report.items:
        print(f"  {_item_line(item)}")
    if report.failed:
        print(f"\n有 {len(report.failed)} 项需要处理（详见日志：{paths.log_path()}）")
        return 1
    return 0


def _item_line(item) -> str:
    tag = FAIL if item.error else ("SKIP" if item.skipped else OK)
    extra = []
    if item.credit_today:
        extra.append(f"今日 +{item.credit_today:g}")
    if item.balance is not None:
        extra.append(f"余额 {item.balance:g}")
    if item.expiring:
        extra.append(f"{item.expiring_days} 天内到期 {item.expiring:g}")
    suffix = f"（{'，'.join(extra)}）" if extra else ""
    return f"[{tag}] {item.label}：{item.message or '成功'}{suffix}"


# ------------------------------------------------------------------ status

def _latest_points() -> list[dict]:
    latest: dict[tuple, dict] = {}
    for row in read_jsonl(paths.points_path()):
        key = (row.get("product"), row.get("account"))
        if key not in latest or (row.get("ts") or 0) > (latest[key].get("ts") or 0):
            latest[key] = row
    return sorted(latest.values(), key=lambda r: str(r.get("product")))


def _cmd_status(args: argparse.Namespace) -> int:
    state = read_json(paths.state_path(), {}) or {}
    trae_status = trae_cred.credential_status()
    wb_accounts = [wb_cred.credential_status(c) for c in wb_cred.load_all()]
    points = _latest_points()

    if args.json:
        payload = {
            "date": today_str(),
            "state": state,
            "credentials": {"trae": _plain(trae_status), "workbuddy": wb_accounts},
            "points": points,
        }
        print(json.dumps(payload, ensure_ascii=False, indent=2, default=str))
        return 0

    print(f"== 签到状态（{today_str()}）==")
    products = state.get("products") or {}
    if not products:
        print("  暂无执行记录（先跑一次：checkin run --dry-run）")
    for key, record in products.items():
        marker = OK if record.get("successDate") == today_str() else WARN
        print(
            f"  [{marker}] {key}：{record.get('message') or '-'}"
            f"（最近 {record.get('at') or '-'}，成功日 {record.get('successDate') or '-'}）"
        )

    print("\n== 凭据 ==")
    if trae_status.get("configured"):
        print(
            f"  [OK] Trae CN：会话 {fmt_remain(trae_status.get('sessionExpires'))}，"
            f"来源 {trae_status.get('source')}"
        )
    else:
        print(f"  [WARN] Trae CN：{trae_status.get('message')}")
    if wb_accounts:
        for acc in wb_accounts:
            flag = FAIL if acc.get("expired") else OK
            print(
                f"  [{flag}] WorkBuddy（{wb_cred.short_uid(acc.get('uid'))}）："
                f"AT {fmt_remain(acc.get('expiresAt'))}，登录方式 {acc.get('loginMethod')}"
                f"，手机号 {acc.get('phone')}"
            )
    else:
        print("  [WARN] WorkBuddy：未登录（checkin login workbuddy）")

    print("\n== 积分（最近快照）==")
    if not points:
        print("  暂无积分快照（先跑一次：checkin run）")
    for row in points:
        print(
            f"  {row.get('date')} {row.get('product')}/{str(row.get('account'))[:8]}："
            f"余额 {row.get('balance')}，"
            f"{row.get('expiring_days')} 天内到期 {row.get('expiring_amount')}，"
            f"30 天内到期 {row.get('expiring_30d')}"
        )
    print(f"\n日志：{paths.log_path()}")
    return 0


def _plain(obj):
    return {k: v for k, v in obj.items()}


# ------------------------------------------------------------------ doctor

def _cmd_doctor(args: argparse.Namespace) -> int:
    checks: list[tuple[str, str, str]] = []

    # 数据目录
    paths.ensure_dirs()
    checks.append(("数据目录", OK, str(paths.state_dir())))

    # 配置
    cfg = config.load()
    warnings = config.last_warnings()
    if warnings:
        checks.append(("配置文件", WARN, "；".join(warnings)))
    else:
        checks.append(("配置文件", OK, str(paths.config_path())))

    # 钉钉
    ready, reason = config.dingtalk_ready()
    checks.append(
        (
            "钉钉通知",
            OK if ready else WARN,
            reason if ready else f"{reason}（通知仍会写本地日志）",
        )
    )

    # 定时任务
    sched = launchd.status()
    if not paths.is_macos():
        checks.append(("定时任务", WARN, "当前非 macOS，launchd 不可用（Phase 3 支持 Windows）"))
    elif sched.get("installed"):
        time_note = f"执行时间 {sched.get('plist_time')}"
        if sched.get("plist_time") != sched.get("config_time"):
            checks.append(
                ("定时任务", WARN, f"已安装但时间不一致（plist {sched.get('plist_time')} ≠ 配置 {sched.get('config_time')}），请重新 install")
            )
        elif not sched.get("loaded"):
            checks.append(("定时任务", WARN, f"plist 存在但未加载：{sched.get('launchctl_output')}"))
        else:
            checks.append(("定时任务", OK, f"已加载，{time_note}"))
    else:
        checks.append(("定时任务", WARN, "未安装：checkin schedule install"))

    # 浏览器可用性（零下载约束下的一次性提示）
    channels = trae_cred.browser_channels_available()
    if channels:
        checks.append(("浏览器（Trae 登录用）", OK, "系统已装：" + "、".join(channels)))
    elif trae_cred.bundled_chromium_installed():
        checks.append(("浏览器（Trae 登录用）", WARN, "系统未装 Chrome/Edge，将回退自带 Chromium"))
    else:
        checks.append(
            ("浏览器（Trae 登录用）", WARN, "系统未装 Chrome/Edge 且无自带 Chromium；登录前需二选一")
        )

    # Trae 凭据
    trae_status = trae_cred.credential_status()
    if trae_status.get("configured"):
        checks.append(
            (
                "Trae 凭据",
                OK,
                f"会话 {fmt_remain(trae_status.get('sessionExpires'))}，来源 {trae_status.get('source')}",
            )
        )
    else:
        checks.append(("Trae 凭据", FAIL, str(trae_status.get("message"))))

    # WorkBuddy 凭据
    wb_accounts = wb_cred.load_all()
    if wb_accounts:
        for acc in wb_accounts:
            status = wb_cred.credential_status(acc)
            level = FAIL if status.get("expired") else OK
            checks.append(
                (
                    f"WorkBuddy 凭据（{wb_cred.short_uid(status.get('uid'))}）",
                    level,
                    f"AT {fmt_remain(status.get('expiresAt'))}，"
                    f"{status.get('loginMethod')} 登录，来源 {status.get('source')}",
                )
            )
    else:
        checks.append(("WorkBuddy 凭据", FAIL, "未登录：checkin login workbuddy --method browser|sms"))

    # 网络连通（--offline 跳过）
    if args.offline:
        checks.append(("网络连通", WARN, "已跳过（--offline）"))
    else:
        checks.extend(_network_checks())

    print("== 自检 ==")
    for name, level, detail in checks:
        print(f"  [{level:4s}] {name}：{detail}")
    failed = [c for c in checks if c[1] == FAIL]
    warned = [c for c in checks if c[1] == WARN]
    print(f"\n结果：{len(checks)} 项检查，{len(failed)} 项 FAIL，{len(warned)} 项 WARN")
    print(f"日志：{paths.log_path()}")
    return 1 if failed else 0


def _network_checks() -> list[tuple[str, str, str]]:
    from checkin.providers import trae_cn
    from checkin.providers import workbuddy as wb_provider

    checks: list[tuple[str, str, str]] = []

    try:
        with trae_cn.TraeCNProvider() as provider:
            status = provider.status()
            detail = "今日已签到" if status.checked_in else "今日未签到（run 时领取）"
            if status.credits_today is not None:
                detail += f"，今日积分 {status.credits_today:g}"
            checks.append(("网络 · Trae", OK, detail))
    except CredentialError as exc:
        checks.append(("网络 · Trae", FAIL, f"凭据问题：{exc}"))
    except Exception as exc:  # noqa: BLE001
        checks.append(("网络 · Trae", FAIL, f"{type(exc).__name__}: {exc}"))

    accounts = wb_cred.load_all()
    if not accounts:
        checks.append(("网络 · WorkBuddy", WARN, "无账号可测（未登录）"))
    for cred in accounts:
        label = f"网络 · WorkBuddy（{wb_cred.short_uid(cred.get('uid'))}）"
        try:
            with wb_provider.WorkBuddyProvider(cred) as provider:
                status = provider.status()
                detail = "今日已签到" if status.checked_in else "今日未签到（run 时领取）"
                if status.streak_days:
                    detail += f"，连签 {status.streak_days} 天"
                checks.append((label, OK, detail))
        except CredentialError as exc:
            checks.append((label, FAIL, f"凭据问题：{exc}"))
        except Exception as exc:  # noqa: BLE001
            checks.append((label, FAIL, f"{type(exc).__name__}: {exc}"))
    return checks


# ------------------------------------------------------------------ login

def _cmd_login_trae(args: argparse.Namespace) -> int:
    try:
        cred = trae_cred.browser_login(timeout=args.timeout, channel=args.channel)
        token = trae_cred.jwt_token(force=True)  # 立即验证换票链路
    except CredentialError as exc:
        print(f"登录失败：{exc}")
        return 1
    print(f"Trae 登录成功：会话有效期至 {fmt_ts(cred.get('sessionExpires'))}，来源 {cred.get('source')}")
    print(f"换票验证通过：JWT {len(token)} 字符，过期 {fmt_remain(trae_cred.jwt_exp(token))}")
    return 0


def _cmd_login_workbuddy(args: argparse.Namespace) -> int:
    method = args.method
    try:
        if method == "sms":
            cred = _login_workbuddy_sms(args)
            if cred is None:
                return 2
        else:
            print(f"将用系统默认浏览器打开登录页，请在 {int(args.timeout)} 秒内完成登录（微信扫码）…")
            cred = wb_cred.login_oauth(timeout=args.timeout)
    except CredentialError as exc:
        print(f"登录失败：{exc}")
        return 1

    print(
        f"WorkBuddy 登录成功：uid={wb_cred.short_uid(cred.get('uid'))}，"
        f"AT 过期 {fmt_remain(cred.get('expiresAt'))}，登录方式 {cred.get('loginMethod')}"
    )
    # 只读验证 token 可用
    try:
        from checkin.providers import workbuddy as wb_provider

        with wb_provider.WorkBuddyProvider(cred) as provider:
            status = provider.status()
            print(f"Token 验证通过：{status.message}")
    except Exception as exc:  # noqa: BLE001
        print(f"Token 验证未通过（请稍后重试 doctor）：{type(exc).__name__}: {exc}")
        return 1
    return 0


def _login_workbuddy_sms(args: argparse.Namespace):
    phone = args.phone
    if not phone:
        if not sys.stdin.isatty():
            print("非交互环境请提供 --phone <手机号>")
            return None
        phone = input("手机号：").strip()
    if not phone:
        print("未提供手机号")
        return None
    ok, message = wb_cred.send_sms(phone)
    print(message)
    if not ok:
        return None
    code = args.code
    if not code:
        if not sys.stdin.isatty():
            print("非交互环境请追加 --code <验证码>（验证码 5 分钟内有效，可先执行 send 再执行 login）")
            return None
        code = input("验证码（5 分钟内有效）：").strip()
    if not code:
        print("未输入验证码")
        return None
    return wb_cred.login_sms(phone, code)


# ------------------------------------------------------------------ config

def _cmd_config(args: argparse.Namespace) -> int:
    if args.action == "show":
        print(json.dumps(config.masked_view(), ensure_ascii=False, indent=2))
        warnings = config.last_warnings()
        if warnings:
            print("\n警告：" + "；".join(warnings))
        return 0
    if args.action == "path":
        print(paths.config_path())
        return 0
    if args.action == "get":
        if not args.key:
            print("用法：checkin config get <点路径>")
            return 2
        value = config.get(args.key)
        if value is None and args.key not in config.VALIDATORS:
            print(f"未知配置项：{args.key}")
            return 2
        if isinstance(value, dict):
            print(json.dumps(value, ensure_ascii=False, indent=2))
        else:
            print(config.display(args.key, value))
        return 0
    if not args.key or args.value is None:
        print("用法：checkin config set <点路径> <值>")
        return 2
    ok, message = config.set(args.key, args.value)
    print(message)
    return 0 if ok else 2


# ------------------------------------------------------------------ schedule

def _cmd_schedule(args: argparse.Namespace) -> int:
    if args.action == "install":
        ok, message = launchd.install()
        print(message)
        return 0 if ok else 1
    if args.action == "uninstall":
        ok, message = launchd.uninstall()
        print(message)
        return 0 if ok else 1
    info = launchd.status()
    print(json.dumps(info, ensure_ascii=False, indent=2))
    return 0


def _cmd_ui(args: argparse.Namespace) -> int:
    from checkin.web import server

    return server.serve(port=args.port, open_browser=not args.no_open)


# ------------------------------------------------------------------ 入口

def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="checkin", description="Trae CN + WorkBuddy 签到与积分助手（个人自用）"
    )
    parser.add_argument("--version", action="version", version=f"checkin {__version__}")
    parser.add_argument("-v", "--verbose", action="store_true", help="输出调试日志")
    sub = parser.add_subparsers(dest="command")

    p_run = sub.add_parser("run", help="执行一次签到 + 台账 + 预警（定时任务调用的就是这个）")
    p_run.add_argument("--dry-run", action="store_true", help="完整走一遍但不 claim、不落盘、不发通知")
    p_run.add_argument("--force", action="store_true", help="忽略幂等（今日已成功也重跑）")
    p_run.add_argument(
        "--only", action="append", choices=["trae", "workbuddy"], help="只跑指定产品（可重复）"
    )
    p_run.set_defaults(handler=_cmd_run)

    p_status = sub.add_parser("status", help="本地状态：签到记录 / 凭据 / 最近积分快照")
    p_status.add_argument("--json", action="store_true", help="输出 JSON")
    p_status.set_defaults(handler=_cmd_status)

    p_doctor = sub.add_parser("doctor", help="自检：配置 / 凭据 / 网络 / 定时 / 浏览器")
    p_doctor.add_argument("--offline", action="store_true", help="跳过网络连通检查")
    p_doctor.set_defaults(handler=_cmd_doctor)

    p_login = sub.add_parser("login", help="登录并保存凭据")
    login_sub = p_login.add_subparsers(dest="product", required=True)
    p_login_trae = login_sub.add_parser("trae", help="打开浏览器登录 trae.cn（复用系统浏览器，零下载）")
    p_login_trae.add_argument("--channel", choices=list(trae_cred.BROWSER_CHANNELS), help="强制指定浏览器通道")
    p_login_trae.add_argument("--timeout", type=float, default=trae_cred.LOGIN_WAIT_SECONDS, help="等待登录秒数")
    p_login_trae.set_defaults(handler=_cmd_login_trae)
    p_login_wb = login_sub.add_parser("workbuddy", help="登录 WorkBuddy（浏览器扫码 / 手机号验证码）")
    p_login_wb.add_argument("--method", choices=["browser", "sms"], default="browser", help="登录方式")
    p_login_wb.add_argument("--phone", help="短信登录的手机号")
    p_login_wb.add_argument("--code", help="短信验证码（非交互环境必须提供）")
    p_login_wb.add_argument("--timeout", type=float, default=wb_cred.OAUTH_WAIT_SECONDS, help="浏览器登录等待秒数")
    p_login_wb.set_defaults(handler=_cmd_login_workbuddy)

    p_config = sub.add_parser("config", help="配置读写（show / get / set / path）")
    config_sub = p_config.add_subparsers(dest="action", required=True)
    config_sub.add_parser("show", help="查看全部配置（密钥打码）").set_defaults(handler=_cmd_config)
    config_sub.add_parser("path", help="打印配置文件路径").set_defaults(handler=_cmd_config)
    p_cfg_get = config_sub.add_parser("get", help="读取单项")
    p_cfg_get.add_argument("key", nargs="?")
    p_cfg_get.set_defaults(handler=_cmd_config)
    p_cfg_set = config_sub.add_parser("set", help="写入单项（先校验后落盘）")
    p_cfg_set.add_argument("key", nargs="?")
    p_cfg_set.add_argument("value", nargs="?")
    p_cfg_set.set_defaults(handler=_cmd_config)

    p_sched = sub.add_parser("schedule", help="定时任务（launchd）")
    sched_sub = p_sched.add_subparsers(dest="action", required=True)
    sched_sub.add_parser("install", help="安装/更新每日定时").set_defaults(handler=_cmd_schedule)
    sched_sub.add_parser("uninstall", help="卸载每日定时").set_defaults(handler=_cmd_schedule)
    sched_sub.add_parser("status", help="查看定时任务状态").set_defaults(handler=_cmd_schedule)

    p_ui = sub.add_parser("ui", help="本地 Web 看板：浏览器中查看状态、改配置、手动执行")
    p_ui.add_argument("--port", type=int, default=None, help="监听端口（默认 8765，被占用时自动顺延）")
    p_ui.add_argument("--no-open", action="store_true", help="不自动打开浏览器")
    p_ui.set_defaults(handler=_cmd_ui)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    setup_logging(getattr(args, "verbose", False))
    handler = getattr(args, "handler", None)
    if handler is None:
        parser.print_help()
        return 0
    try:
        return int(handler(args) or 0)
    except KeyboardInterrupt:
        print("\n已中断")
        return 130
    except CredentialError as exc:
        print(f"凭据错误：{exc}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
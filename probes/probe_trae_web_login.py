#!/usr/bin/env python3
"""Phase 0.1d / 0.2 探针：Trae 网页登录 → 换票 → 只读验证 + 抓取用量接口。

流程：
1. Playwright 以持久化 profile 打开 www.trae.cn（登录一次，之后复用）
2. 等你在窗口里完成登录（检测 X-Cloudide-Session cookie）
3. 打开「用量管理」页，同时抓取 trae 域下所有 JSON 响应 → 落盘 ~/.checkin/probe-capture/
4. 用会话 Cookie 调 `GetUserToken` 换 JWT（两种传参方式各试一次）
5. 用 JWT 调 `checkin_credits/status`（**只读**，绝不调用 claim）

约束：不打印 cookie / token / 响应体的明文值，只打印字段名、类型与长度。
"""
import json
import pathlib
import sys
import time

import httpx
from playwright.sync_api import sync_playwright

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
PROFILE_DIR = STATE_DIR / "browser" / "trae"
CAPTURE_DIR = STATE_DIR / "probe-capture"

COOKIE_NAME = "X-Cloudide-Session"
LOGIN_URL = "https://www.trae.cn/"
USAGE_URL = "https://www.trae.cn/dashboard#usage"
TOKEN_URL = "https://api.trae.cn/cloudide/api/v3/common/GetUserToken"
STATUS_URL = "https://api.trae.cn/trae/api/v2/ug/checkin_credits/status"
LOGIN_WAIT_SECONDS = 480


def shape(obj, depth=0):
    """描述 JSON 结构（只给键名与类型，不给值）。"""
    pad = "  " * depth
    if isinstance(obj, dict):
        return "\n".join(
            f"{pad}- {k}: {shape(v, depth + 1) if isinstance(v, (dict, list)) else type(v).__name__}"
            for k, v in obj.items()
        )
    if isinstance(obj, list):
        inner = shape(obj[0], depth + 1) if obj else "(empty)"
        return f"list[{len(obj)}] of\n{inner}"
    return type(obj).__name__


def find_jwt(obj, path="root"):
    """递归找一个形如 JWT 的字符串，返回 (路径, 值)，不打印值。"""
    if isinstance(obj, dict):
        for k, v in obj.items():
            hit = find_jwt(v, f"{path}.{k}")
            if hit:
                return hit
    elif isinstance(obj, list):
        for i, v in enumerate(obj[:5]):
            hit = find_jwt(v, f"{path}[{i}]")
            if hit:
                return hit
    elif isinstance(obj, str) and obj.count(".") == 2 and obj.startswith("ey"):
        return path, obj
    return None


def call_status(jwt):
    headers = {
        "Authorization": f"Cloud-IDE-JWT {jwt}",
        "X-User-Region": "cn",
        "Content-Type": "application/json",
        "trae_client": "IDE",
        "User-Agent": "Trae/3.4.2",
    }
    body = {"trae_client": "IDE", "req_source": 1, "client_version": "3.4.2"}
    with httpx.Client(timeout=20) as client:
        resp = client.post(STATUS_URL, headers=headers, json=body)
        print(f"[status] HTTP {resp.status_code}")
        try:
            print("[status] body:", json.dumps(resp.json(), ensure_ascii=False)[:600])
        except Exception:  # noqa: BLE001
            print("[status] body(raw):", resp.text[:300])


def main():
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    PROFILE_DIR.mkdir(parents=True, exist_ok=True)
    CAPTURE_DIR.mkdir(parents=True, exist_ok=True)

    captured = []

    with sync_playwright() as pw:
        ctx = pw.chromium.launch_persistent_context(str(PROFILE_DIR), headless=False)
        page = ctx.new_page()

        def on_response(resp):
            if "trae" not in resp.url:
                return
            ctype = (resp.headers or {}).get("content-type", "")
            if "json" not in ctype:
                return
            captured.append({"url": resp.url, "status": resp.status})
            try:
                name = f"{len(captured):03d}.json"
                (CAPTURE_DIR / name).write_text(resp.text()[:300000])
            except Exception:  # noqa: BLE001
                pass

        page.on("response", on_response)
        page.goto(LOGIN_URL, wait_until="domcontentloaded")

        print("=" * 68)
        print("请在弹出的浏览器窗口里登录 www.trae.cn（扫码 / 手机号均可）。")
        print(f"登录成功后本探针会自动继续，最多等 {LOGIN_WAIT_SECONDS} 秒。")
        print("=" * 68)

        deadline = time.time() + LOGIN_WAIT_SECONDS
        logged_in = False
        while time.time() < deadline:
            names = {c["name"] for c in ctx.cookies()}
            if COOKIE_NAME in names:
                logged_in = True
                break
            time.sleep(2)

        names = sorted({c["name"] for c in ctx.cookies()})
        print(f"[login] {'检测到 ' + COOKIE_NAME if logged_in else '超时未检测到 ' + COOKIE_NAME}")
        print(f"[login] 当前 cookie 名：{names}")

        if logged_in:
            print("[step ] 打开「用量管理」页并抓取接口…")
            page.goto(USAGE_URL, wait_until="domcontentloaded")
            time.sleep(10)

            cookies = ctx.cookies()
            session = next((c["value"] for c in cookies if c["name"] == COOKIE_NAME), "")
            print(f"[cookie] {COOKIE_NAME} 长度 = {len(session)}")

            state_file = STATE_DIR / "trae-web-state.json"
            state_file.write_text(json.dumps(cookies, ensure_ascii=False))
            state_file.chmod(0o600)
            print(f"[save ] cookie 已存到 {state_file}（权限 600）")

            jwt = None
            with httpx.Client(timeout=20) as client:
                for label, kwargs in (
                    ("A_custom_header", {"headers": {"X-Cloudide-Session": session}}),
                    ("B_cookie_header", {"headers": {"Cookie": f"{COOKIE_NAME}={session}"}}),
                ):
                    try:
                        resp = client.post(TOKEN_URL, json={}, **kwargs)
                        print(f"[token/{label}] HTTP {resp.status_code}")
                        payload = resp.json()
                        print(f"[token/{label}] 结构:\n{shape(payload)}")
                        hit = find_jwt(payload)
                        if hit:
                            jwt = hit[1]
                            print(f"[token/{label}] 找到 JWT，位于 `{hit[0]}`")
                            break
                    except Exception as exc:  # noqa: BLE001
                        print(f"[token/{label}] FAIL {type(exc).__name__}: {exc}")

            if jwt:
                call_status(jwt)
            else:
                print("[token] 未能从响应中取出 JWT，需人工看响应结构再调整")

        print("\n[step ] 抓到的 trae 域 JSON 接口（前 40 条）：")
        for item in captured[:40]:
            print(f"  {item['status']} {item['url']}")
        (CAPTURE_DIR / "_summary.json").write_text(json.dumps(captured, ensure_ascii=False, indent=2))
        print(f"[save ] 响应体与清单已存到 {CAPTURE_DIR}")

        if sys.stdin.isatty():
            input("\n按回车关闭浏览器…")
        else:
            print("\n[auto ] 非交互运行：60 秒后自动关闭浏览器")
            time.sleep(60)
        ctx.close()


if __name__ == "__main__":
    main()
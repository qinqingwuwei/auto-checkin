#!/usr/bin/env python3
"""Phase 0.1d / 0.2 探针 v2：Trae 网页会话 → 换票 → 只读验证 + 抓取用量/积分接口。

v2 相对 v1 的修正：
- 顺序：先完成「换票 + 只读 status 验证」，再做页面抓取（不依赖页面加载成功）
- 页面导航改用 wait_until="commit" + try/except，不再因 domcontentloaded 超时中断
- 抓取记录 method / url / status / 请求体 / auth 方案，增量写 _summary.jsonl，崩溃不丢
- context 级监听（新标签页、弹窗的请求也能抓到）
- 输出实时 flush（配合 python -u）

约束：不打印 cookie / token / 响应体明文值，只打印字段名、类型与长度。
"""
import json
import pathlib
import re
import sys
import time

import httpx
from playwright.sync_api import sync_playwright

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
PROFILE_DIR = STATE_DIR / "browser" / "trae"
CAPTURE_DIR = STATE_DIR / "probe-capture2"
SUMMARY = CAPTURE_DIR / "_summary.jsonl"

COOKIE_NAME = "X-Cloudide-Session"
LOGIN_URL = "https://www.trae.cn/"
USAGE_URL = "https://www.trae.cn/dashboard#usage"
TOKEN_URL = "https://api.trae.cn/cloudide/api/v3/common/GetUserToken"
STATUS_URL = "https://api.trae.cn/trae/api/v2/ug/checkin_credits/status"
LOGIN_WAIT_SECONDS = 480


def log(msg):
    print(msg, flush=True)


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
    """递归找一个形如 JWT 的字符串，返回 (路径, 值)。"""
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


def jwt_info(jwt):
    """解析 JWT 的 claims 结构（不打印 token 本身）。"""
    import base64

    try:
        seg = jwt.split(".")[1]
        pad = "=" * (-len(seg) % 4)
        payload = json.loads(base64.urlsafe_b64decode(seg + pad))
        log(f"[jwt ] claims 字段: {sorted(payload.keys())}")
        if "data" in payload and isinstance(payload["data"], dict):
            log(f"[jwt ] data 字段: {sorted(payload['data'].keys())}")
        exp = payload.get("exp")
        if exp:
            log(f"[jwt ] exp = {exp} → {time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(exp))}")
    except Exception as exc:  # noqa: BLE001
        log(f"[jwt ] 解析失败: {type(exc).__name__}: {exc}")


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
        log(f"[status] HTTP {resp.status_code}")
        try:
            log("[status] body: " + json.dumps(resp.json(), ensure_ascii=False)[:600])
        except Exception:  # noqa: BLE001
            log("[status] body(raw): " + resp.text[:300])


def main():
    for d in (STATE_DIR, PROFILE_DIR, CAPTURE_DIR):
        d.mkdir(parents=True, exist_ok=True)

    captured = []

    def on_response(resp):
        url = resp.url
        if "trae" not in url:
            return
        ctype = (resp.headers or {}).get("content-type", "")
        if "json" not in ctype:
            return
        n = len(captured) + 1
        rec = {"n": n, "method": resp.request.method, "url": url, "status": resp.status}
        try:
            auth = resp.request.all_headers().get("authorization")
            if auth:
                rec["auth_scheme"] = auth.split(" ")[0]
        except Exception:  # noqa: BLE001
            pass
        try:
            pd = resp.request.post_data
            if pd:
                rec["post_data"] = pd[:1500]
        except Exception:  # noqa: BLE001
            pass
        try:
            body = resp.text()[:300000]
            slug = re.sub(r"[^A-Za-z0-9]+", "_", url.split("?")[0]).strip("_")[-70:]
            fname = f"{n:03d}_{slug}.json"
            (CAPTURE_DIR / fname).write_text(body)
            rec["file"] = fname
        except Exception:  # noqa: BLE001
            rec["file"] = None
        captured.append(rec)
        try:
            with SUMMARY.open("a") as fh:
                fh.write(json.dumps(rec, ensure_ascii=False) + "\n")
        except Exception:  # noqa: BLE001
            pass

    with sync_playwright() as pw:
        ctx = pw.chromium.launch_persistent_context(str(PROFILE_DIR), headless=False)
        ctx.on("response", on_response)
        page = ctx.new_page()

        try:
            page.goto(LOGIN_URL, wait_until="commit", timeout=60000)
        except Exception as exc:  # noqa: BLE001
            log(f"[warn ] 首页导航告警（继续）: {type(exc).__name__}: {exc}")

        names0 = sorted({c["name"] for c in ctx.cookies()})
        already = COOKIE_NAME in names0
        log(f"[login] 初始会话：{'已登录（复用持久化 profile）' if already else '未登录，等待手动登录'}")

        deadline = time.time() + LOGIN_WAIT_SECONDS
        logged_in = already
        while not logged_in and time.time() < deadline:
            if COOKIE_NAME in {c["name"] for c in ctx.cookies()}:
                logged_in = True
                break
            page.wait_for_timeout(2000)

        log(f"[login] {'检测到 ' + COOKIE_NAME if logged_in else '超时未检测到 ' + COOKIE_NAME}")

        if logged_in:
            cookies = ctx.cookies()
            session = next((c["value"] for c in cookies if c["name"] == COOKIE_NAME), "")
            log(f"[cookie] {COOKIE_NAME} 长度 = {len(session)}")

            state_file = STATE_DIR / "trae-web-state.json"
            state_file.write_text(json.dumps(cookies, ensure_ascii=False))
            state_file.chmod(0o600)
            log(f"[save ] cookie 已存到 {state_file}（权限 600）")

            jwt = None
            with httpx.Client(timeout=20) as client:
                for label, kwargs in (
                    ("A_custom_header", {"headers": {"X-Cloudide-Session": session}}),
                    ("B_cookie_header", {"headers": {"Cookie": f"{COOKIE_NAME}={session}"}}),
                ):
                    try:
                        resp = client.post(TOKEN_URL, json={}, **kwargs)
                        log(f"[token/{label}] HTTP {resp.status_code}")
                        payload = resp.json()
                        log(f"[token/{label}] 结构:\n{shape(payload)}")
                        expired = (payload.get("Result") or {}).get("ExpiredAt")
                        if expired:
                            log(f"[token/{label}] ExpiredAt = {expired}")
                        hit = find_jwt(payload)
                        if hit:
                            jwt = hit[1]
                            log(f"[token/{label}] 找到 JWT，位于 `{hit[0]}`，长度 {len(jwt)}")
                            break
                    except Exception as exc:  # noqa: BLE001
                        log(f"[token/{label}] FAIL {type(exc).__name__}: {exc}")

            if jwt:
                jwt_file = STATE_DIR / "trae-web-jwt.txt"
                jwt_file.write_text(jwt)
                jwt_file.chmod(0o600)
                log(f"[save ] JWT 已存到 {jwt_file}（权限 600）")
                jwt_info(jwt)
                call_status(jwt)
            else:
                log("[token] 未能从响应中取出 JWT，需看上面的结构再调整")

            log("[step ] 打开「用量管理」页抓取接口（commit 模式，失败不中断）…")
            try:
                page.goto(USAGE_URL, wait_until="commit", timeout=60000)
            except Exception as exc:  # noqa: BLE001
                log(f"[warn ] 用量页导航告警（继续）: {type(exc).__name__}: {exc}")
            # wait_for_timeout 会在等待期间持续泵 Playwright 事件（time.sleep 不会）
            page.wait_for_timeout(30000)
            page.mouse.wheel(0, 2000)
            page.wait_for_timeout(8000)

        ctx.cookies()  # 兜底：再泵一次事件队列，确保响应回调已全部执行
        log(f"\n[step ] 共抓到 {len(captured)} 条 trae 域 JSON 接口：")
        for item in captured:
            extra = f"  auth={item.get('auth_scheme')}" if item.get("auth_scheme") else ""
            log(f"  {item['method']:4s} {item['status']} {item['url'][:150]}{extra}")
        log(f"[save ] 响应体与增量清单已存到 {CAPTURE_DIR}")

        if sys.stdin.isatty():
            input("\n按回车关闭浏览器…")
        else:
            log("\n[auto ] 非交互运行：20 秒后自动关闭浏览器")
            time.sleep(20)
        ctx.close()


if __name__ == "__main__":
    main()
#!/usr/bin/env python3
"""抓取浏览器对 pay 接口发请求时的完整请求头（敏感值脱敏），用于直连复现对齐。"""
import json
import pathlib
import sys
import time

from playwright.sync_api import sync_playwright

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
PROFILE_DIR = STATE_DIR / "browser" / "trae"
OUT = STATE_DIR / "probe-capture2" / "reqheaders.json"

TARGETS = ("user_current_entitlement_list", "expired_ents", "query_user_usage_group_by_session",
           "checkin_credits", "daily_checkin")
SENSITIVE = {"authorization", "cookie", "x-cloudide-session"}

records = []


def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)

    def on_request(req):
        if not any(t in req.url for t in TARGETS):
            return
        try:
            headers = req.all_headers()
        except Exception:  # noqa: BLE001
            headers = {}
        safe = {k: ("<redacted len=%d>" % len(v)) if k.lower() in SENSITIVE else v
                for k, v in headers.items()}
        rec = {"method": req.method, "url": req.url, "headers": safe,
               "post_data": (req.post_data or "")[:1000]}
        records.append(rec)
        with OUT.open("a") as fh:
            fh.write(json.dumps(rec, ensure_ascii=False) + "\n")
        print(f"[req] {req.method} {req.url.split('?')[0]}", flush=True)
        print(f"      headers: {sorted(headers.keys())}", flush=True)
        print(f"      post_data: {rec['post_data']}", flush=True)

    with sync_playwright() as pw:
        ctx = pw.chromium.launch_persistent_context(str(PROFILE_DIR), headless=False)
        ctx.on("request", on_request)
        page = ctx.new_page()
        try:
            page.goto("https://www.trae.cn/dashboard#usage", wait_until="commit", timeout=60000)
        except Exception as exc:  # noqa: BLE001
            print(f"[warn] {type(exc).__name__}: {exc}", flush=True)
        page.wait_for_timeout(30000)
        page.mouse.wheel(0, 1500)
        page.wait_for_timeout(8000)

        print(f"\n[done] 共记录 {len(records)} 条请求，落盘 {OUT}")
        print("[hint] 浏览器自带 cookie 名（不打印值）:",
              sorted({c["name"] for c in ctx.cookies() if "trae" in c.get("domain", "")}))

        if sys.stdin.isatty():
            input("按回车关闭…")
        else:
            page.wait_for_timeout(15000)
        ctx.close()


if __name__ == "__main__":
    main()
#!/usr/bin/env python3
"""Phase 0.6 探针：WorkBuddy 手机号验证码直登（插件接口版，全程零浏览器）。

背景：OAuth 登录页上的"手机号验证码"入口会报"登录失效"（页面限制），
但插件底层有 SMS 直登 API，绕开页面即可用。字段逐字对齐
L0NE-6/WorkBuddy-Daily 的 workbuddy_login.py（已实测可用的版本）：

1. POST https://www.workbuddy.cn/v2/plugin/login/send-sms
   body {"phone": "<11位手机号>"} → {"code":0,"data":{"expires_in":300}}
2. POST https://www.workbuddy.cn/v2/plugin/login/token
   body {"login_method":"phone","phone":...,"sms_code":...}
   → {"code":0,"data":{"accessToken":...,"refreshToken":...}}（AT 为 JWT）
3. POST https://copilot.tencent.com/v2/plugin/auth/token/refresh
   头 X-Refresh-Token / X-Auth-Refresh-Source: plugin，body {}
   → 换发新 AT（验证 RT 可长期复用、支持自动续期）

用法（两段式，避免交互 TTY 限制）：
  uv run --with httpx python probes/probe_workbuddy_sms.py send  --phone 138xxxxxxxx
  uv run --with httpx python probes/probe_workbuddy_sms.py login --phone 138xxxxxxxx --code 123456

约束：只读；绝不调用 daily-checkin（签到动作）；不打印 token 明文；
成功后落盘 ~/.checkin/workbuddy-sms.json（0600），字段与 workbuddy-oauth.json 对齐。
"""
import argparse
import base64
import json
import pathlib
import time

import httpx

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
OUT = STATE_DIR / "workbuddy-sms.json"

SMS_HOST = "https://www.workbuddy.cn"
SEND_SMS_URL = SMS_HOST + "/v2/plugin/login/send-sms"
LOGIN_URL = SMS_HOST + "/v2/plugin/login/token"
REFRESH_URL = "https://copilot.tencent.com/v2/plugin/auth/token/refresh"
BILLING_HOSTS = ["https://www.codebuddy.cn", "https://www.workbuddy.cn"]

UA = ("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/138.0.7204.251 Safari/537.36")


def log(msg):
    print(msg, flush=True)


def plugin_headers():
    return {
        "Content-Type": "application/json",
        "Accept": "application/json, text/plain, */*",
        "Origin": SMS_HOST,
        "Referer": SMS_HOST + "/",
        "User-Agent": UA,
    }


def jwt_payload(tok):
    try:
        p = tok.split(".")[1]
        p += "=" * (4 - len(p) % 4)
        return json.loads(base64.urlsafe_b64decode(p))
    except Exception:
        return {}


def cmd_send(args):
    with httpx.Client(timeout=20) as c:
        r = c.post(SEND_SMS_URL, headers=plugin_headers(), json={"phone": args.phone})
        log(f"[send-sms] HTTP {r.status_code}")
        try:
            d = r.json()
        except Exception:
            log("[send-sms] 非 JSON 响应: " + r.text[:300])
            return
        log("[send-sms] " + json.dumps(d, ensure_ascii=False)[:400])
        if d.get("code") == 0:
            exp = (d.get("data") or {}).get("expires_in", "?")
            log(f"[send-sms] ✅ 验证码已发送（有效期 {exp}s）")
            log("==> 收到验证码后执行：login --phone <手机号> --code <验证码>")
        else:
            log(f"[send-sms] ❌ 发送失败：{d.get('msg')}")


def cmd_login(args):
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    with httpx.Client(timeout=30) as c:
        r = c.post(LOGIN_URL, headers=plugin_headers(),
                   json={"login_method": "phone", "phone": args.phone, "sms_code": args.code})
        log(f"[login] HTTP {r.status_code}")
        d = r.json()
        if d.get("code") != 0:
            log("[login] ❌ 登录失败：" + json.dumps(d, ensure_ascii=False)[:400])
            return
        data = d.get("data") or {}
        at = data.get("accessToken") or data.get("access_token")
        rt = data.get("refreshToken") or data.get("refresh_token")
        if not at:
            log("[login] ❌ 响应缺少 accessToken：" + json.dumps(d, ensure_ascii=False)[:400])
            return
        log(f"[login] ✅ 登录成功 accessToken={len(at)}字符 refreshToken={'有' if rt else '无'}")

        pay = jwt_payload(at)
        exp = pay.get("exp")
        exp_str = time.strftime("%Y-%m-%d %H:%M", time.localtime(exp)) if exp else "?"
        log(f"[login] AT 载荷键={sorted(pay.keys())}")
        log(f"[login] AT 过期={exp_str} preferred_username={pay.get('preferred_username') or '-'}")
        uid = pay.get("sub") or pay.get("uid") or pay.get("user_id")

        OUT.write_text(json.dumps({
            "host": SMS_HOST, "loginMethod": "phone", "phone": args.phone,
            "accessToken": at, "refreshToken": rt,
            "expiresIn": data.get("expiresIn"),
            "uid": str(uid) if uid else None,
            "savedAt": int(time.time()),
        }, ensure_ascii=False, indent=2))
        OUT.chmod(0o600)
        log(f"[save ] 已落盘 {OUT}（0600）")

        # RT 刷新验证（确认可长期复用）
        if rt:
            rr = c.post(REFRESH_URL,
                        headers={"X-Refresh-Token": rt, "X-Auth-Refresh-Source": "plugin",
                                 "Content-Type": "application/json"},
                        json={})
            log(f"[refresh] HTTP {rr.status_code}")
            try:
                rd = rr.json()
            except Exception:
                rd = {}
            new_at = (rd.get("data") or {}).get("accessToken")
            if rd.get("code") == 0 and new_at:
                log(f"[refresh] ✅ RT 可用，已换发新 AT（{len(new_at)}字符）")
                saved = json.loads(OUT.read_text())
                saved["accessToken"] = new_at
                saved["refreshedAt"] = int(time.time())
                OUT.write_text(json.dumps(saved, ensure_ascii=False, indent=2))
                OUT.chmod(0o600)
                at = new_at
            else:
                log("[refresh] ⚠️ 刷新未通过：" + json.dumps(rd, ensure_ascii=False)[:300])

        # 只读验证：签到状态（与 OAuth 路径同接口，验证 SMS token 等价可用）
        bh = {"Authorization": f"Bearer {at}", "User-Agent": "WorkBuddy/5.5.6",
              "Accept": "application/json, text/plain, */*",
              "Origin": "https://www.codebuddy.cn", "Referer": "https://www.codebuddy.cn/",
              "Content-Type": "application/json"}
        if uid:
            bh["X-User-Id"] = str(uid)
        for bhost in BILLING_HOSTS:
            try:
                br = c.post(f"{bhost}/v2/billing/meter/checkin-activity-status", headers=bh, json={})
                log(f"[checkin-status] {bhost} HTTP {br.status_code}")
                if br.status_code == 200:
                    bd = br.json().get("data") or {}
                    log(f"[checkin-status] today_checked_in={bd.get('today_checked_in')} "
                        f"streak_days={bd.get('streak_days')} total_credits={bd.get('total_credits')}")
                    break
            except Exception as exc:  # noqa: BLE001
                log(f"[checkin-status] {bhost} FAIL {type(exc).__name__}: {exc}")
        log("[done ] 探针结束（未触发任何签到动作）")


def main():
    ap = argparse.ArgumentParser(description="WorkBuddy 手机号验证码直登探针")
    sub = ap.add_subparsers(dest="cmd", required=True)
    s1 = sub.add_parser("send", help="发送短信验证码")
    s1.add_argument("--phone", required=True)
    s1.set_defaults(fn=cmd_send)
    s2 = sub.add_parser("login", help="提交验证码登录并验证")
    s2.add_argument("--phone", required=True)
    s2.add_argument("--code", required=True)
    s2.set_defaults(fn=cmd_login)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
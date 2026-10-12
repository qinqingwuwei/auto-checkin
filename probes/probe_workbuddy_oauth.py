#!/usr/bin/env python3
"""Phase 0.3 / 0.4 探针 v2：WorkBuddy 插件 OAuth 登录 → 只读签到状态 + 积分批次台账。

v2 变更（v1 失败：Playwright Chromium 窗口未出现在用户桌面，300s 超时）：
- 改用 **系统默认浏览器** 打开 authUrl（与计划 §5.1 设计一致：插件 OAuth = 系统浏览器 + state 轮询）
- URL 同时写入剪贴板兜底；打印 state 响应完整内容与最终 auth_url
- 轮询不再依赖 Playwright 事件泵；首次轮询会打印"未登录"响应形态

流程：
1. POST {host}/v2/plugin/auth/state?platform=CLI → state + authUrl
2. 系统浏览器打开 authUrl，等完成登录
3. 轮询 GET /v2/plugin/auth/token?state=<state>（3s × 100 次）
4. 保存 accessToken / refreshToken → ~/.checkin/workbuddy-oauth.json（0600）
5. GET /v2/plugin/login/account?state= → uid
6. POST www.codebuddy.cn/v2/billing/meter/checkin-activity-status（**只读**）
7. POST www.codebuddy.cn/v2/billing/meter/get-user-resource（**只读**，批次台账）

约束：**绝不调用 daily-checkin**（签到动作），只做只读探测；不打印 token 明文。
"""
import json
import pathlib
import subprocess
import time

import httpx

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
CRED_FILE = STATE_DIR / "workbuddy-oauth.json"

PLUGIN_HOSTS = ["https://copilot.tencent.com", "https://www.codebuddy.cn", "https://www.workbuddy.cn"]
BILLING_HOSTS = ["https://www.codebuddy.cn", "https://www.workbuddy.cn"]

PLUGIN_HEADERS = {
    "User-Agent": "CLI/2.63.2 CodeBuddy/2.63.2",
    "Accept": "application/json, text/plain, */*",
    "X-Requested-With": "XMLHttpRequest",
    "Origin": "https://www.codebuddy.cn",
    "Referer": "https://www.codebuddy.cn/",
    "Content-Type": "application/json",
}
PLUGIN_LOGIN_WAIT = 300


def log(msg):
    print(msg, flush=True)


def dig(obj, keys):
    """递归找第一个命中的键（候选名不区分大小写，跳过空值）。"""
    wanted = {k.lower() for k in keys}
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k.lower() in wanted and v not in (None, ""):
                return v
        for v in obj.values():
            hit = dig(v, keys)
            if hit is not None:
                return hit
    elif isinstance(obj, list):
        for v in obj[:5]:
            hit = dig(v, keys)
            if hit is not None:
                return hit
    return None


def create_state(client):
    for host in PLUGIN_HOSTS:
        try:
            r = client.post(f"{host}/v2/plugin/auth/state?platform=CLI", headers=PLUGIN_HEADERS, json={})
            log(f"[state] {host} → HTTP {r.status_code}")
            if r.status_code != 200:
                continue
            data = r.json()
            log("[state] 响应: " + json.dumps(data, ensure_ascii=False)[:600])
            state = dig(data, ["state", "authState", "auth_state"])
            url = dig(data, ["authUrl", "auth_url", "login_url", "loginUrl", "redirect_url", "url"])
            if state:
                return host, str(state), (str(url) if url else "")
        except Exception as exc:  # noqa: BLE001
            log(f"[state] {host} FAIL {type(exc).__name__}: {exc}")
    return None, None, None


def open_in_browser(url):
    """用系统默认浏览器打开 URL；URL 同时写入剪贴板兜底。返回 open 是否成功。"""
    ok = False
    try:
        res = subprocess.run(["open", url], capture_output=True, timeout=20)
        ok = res.returncode == 0
        if not ok:
            log(f"[open ] open 返回码={res.returncode}，stderr={res.stderr.decode(errors='replace')[:150]}")
    except Exception as exc:  # noqa: BLE001
        log(f"[open ] FAIL {type(exc).__name__}: {exc}")
    try:
        subprocess.run(["pbcopy"], input=url.encode(), capture_output=True, timeout=10)
    except Exception:  # noqa: BLE001
        pass
    return ok


def billing_headers(token, uid):
    h = {
        "Authorization": f"Bearer {token}",
        "User-Agent": "WorkBuddy/5.5.6",
        "Accept": "application/json, text/plain, */*",
        "Origin": "https://www.codebuddy.cn",
        "Referer": "https://www.codebuddy.cn/",
        "Content-Type": "application/json",
    }
    if uid:
        h["X-User-Id"] = str(uid)
    return h


def parse_end_seconds(acct):
    """按优先级取到期时刻 → (epoch 秒, 字段名)。"""
    for key in ("DeductionEndTime", "ExpiredTime", "PackageEndTime", "CycleEndTime"):
        v = acct.get(key)
        if isinstance(v, (int, float)) and v > 10**10:
            return int(v / 1000), key
        if isinstance(v, str) and v.strip():
            s = v.strip()
            if s.isdigit() and int(s) > 10**10:
                return int(s) // 1000, key
            try:
                return int(time.mktime(time.strptime(s, "%Y-%m-%d %H:%M:%S"))), key
            except ValueError:
                continue
    return None, None


def main():
    STATE_DIR.mkdir(parents=True, exist_ok=True)

    with httpx.Client(timeout=20) as client:
        host, state, auth_url = create_state(client)
        if not state:
            log("[state] 所有 host 均失败，中止")
            return
        if not auth_url:
            auth_url = f"{host}/login?platform=CLI&state={state}"
            log("[state] 未提取到 authUrl，使用缺省构造")
        log(f"[state] state={state}")
        log(f"[state] auth_url={auth_url}")

        opened = open_in_browser(auth_url)
        log("=" * 68)
        log("已用系统默认浏览器打开 WorkBuddy 登录页（URL 已复制到剪贴板）。")
        log("请完成登录 / 授权（最多等 300 秒，登录后本探针自动继续，只读查询、不会签到）。")
        log(f"（浏览器唤起结果={opened}；若没有窗口弹出，手动粘贴剪贴板里的链接打开即可）")
        log("=" * 68)

        token = refresh = expires = None
        deadline = time.time() + PLUGIN_LOGIN_WAIT
        last_note = time.time()
        first_poll_logged = False
        while time.time() < deadline:
            try:
                r = client.get(f"{host}/v2/plugin/auth/token?state={state}", headers=PLUGIN_HEADERS)
                if r.status_code == 200:
                    data = r.json()
                    token = dig(data, ["accessToken", "access_token"])
                    if token:
                        refresh = dig(data, ["refreshToken", "refresh_token"])
                        expires = dig(data, ["expiresIn", "expires_in"])
                        break
                    if not first_poll_logged:
                        log("[poll ] 首次轮询（未登录形态）: "
                            + json.dumps(data, ensure_ascii=False)[:300])
                        first_poll_logged = True
                else:
                    log(f"[poll ] HTTP {r.status_code}: {r.text[:120]}")
            except Exception as exc:  # noqa: BLE001
                log(f"[poll ] FAIL {type(exc).__name__}: {exc}")
            if time.time() - last_note > 30:
                log(f"[wait ] 等待登录完成… 已等待 {int(time.time() - (deadline - PLUGIN_LOGIN_WAIT))}s")
                last_note = time.time()
            time.sleep(3)

        if not token:
            log("[token] 超时未拿到 accessToken（浏览器未完成登录？）")
            log("[done ] 探针结束（未触发任何签到动作）")
            return

        log(f"[token] 登录成功：accessToken 长度={len(token)}，refreshToken={'有' if refresh else '无'}，"
            f"expiresIn={expires}")

        CRED_FILE.write_text(json.dumps({
            "host": host, "state": state, "accessToken": token, "refreshToken": refresh,
            "expiresIn": expires, "uid": None, "savedAt": int(time.time()),
        }, ensure_ascii=False, indent=2))
        CRED_FILE.chmod(0o600)
        log(f"[save ] 凭据已存到 {CRED_FILE}（权限 600）")

        # 账号信息（uid）
        uid = None
        try:
            r = client.get(f"{host}/v2/plugin/login/account?state={state}",
                           headers={**PLUGIN_HEADERS, "Authorization": f"Bearer {token}"})
            data = r.json()
            log(f"[acct ] HTTP {r.status_code}，响应前 400 字: "
                + json.dumps(data, ensure_ascii=False)[:400])
            uid = dig(data, ["uid", "userId", "user_id"])
            log(f"[acct ] uid={'有(' + str(len(str(uid))) + '位)' if uid else '无'} "
                f"edition={dig(data, ['editionType', 'edition_type'])}")
            if uid:
                saved = json.loads(CRED_FILE.read_text())
                saved["uid"] = str(uid)
                CRED_FILE.write_text(json.dumps(saved, ensure_ascii=False, indent=2))
                CRED_FILE.chmod(0o600)
        except Exception as exc:  # noqa: BLE001
            log(f"[acct ] FAIL {type(exc).__name__}: {exc}")

        # 只读：签到活动状态
        bh = billing_headers(token, uid)
        for bhost in BILLING_HOSTS:
            try:
                r = client.post(f"{bhost}/v2/billing/meter/checkin-activity-status", headers=bh, json={})
                log(f"[checkin-status] {bhost} HTTP {r.status_code}")
                if r.status_code == 200:
                    d = r.json()
                    log("[checkin-status] 响应前 500 字: " + json.dumps(d, ensure_ascii=False)[:500])
                    data = d.get("data") if isinstance(d.get("data"), dict) else d
                    for k in ("today_checked_in", "checked_in", "todayCheckedIn", "active",
                              "streak_days", "total_credits", "today_credit", "activity_name"):
                        if isinstance(data, dict) and k in data:
                            log(f"    {k} = {data[k]}")
                    break
            except Exception as exc:  # noqa: BLE001
                log(f"[checkin-status] {bhost} FAIL {type(exc).__name__}: {exc}")

        # 只读：积分批次台账
        now_ms = int(time.time() * 1000)
        body = {"PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca", "Status": [0, 3],
                "PackageEndTimeRangeBegin": now_ms, "PackageEndTimeRangeEnd": now_ms + 101 * 365 * 86400 * 1000}
        for bhost in BILLING_HOSTS:
            try:
                r = client.post(f"{bhost}/v2/billing/meter/get-user-resource", headers=bh, json=body)
                log(f"[resource] {bhost} HTTP {r.status_code}")
                if r.status_code != 200:
                    continue
                d = r.json()
                accts = dig(d, ["Accounts"])
                if accts is None:
                    log("[resource] 响应中未找到 Accounts，响应前 400 字: " + r.text[:400])
                    continue
                log(f"[resource] 包数={len(accts)}")
                for a in accts[:8]:
                    log("    pkg " + json.dumps(
                        {k: a.get(k) for k in (
                            "DeductionBeginTime", "DeductionEndTime", "CycleEndTime", "ExpiredTime",
                            "PackageEndTime", "CapacityRemain", "CapacityUsed", "CapacitySize",
                            "ProductCode", "Status") if k in a}, ensure_ascii=False))
                now = time.time()
                exp7 = exp31 = 0.0
                for a in accts:
                    end, _ = parse_end_seconds(a)
                    remain = a.get("CapacityRemain") or a.get("Remain") or 0
                    if not end or remain <= 0 or end <= now:
                        continue
                    if end <= now + 7 * 86400:
                        exp7 += remain
                    if end <= now + 31 * 86400:
                        exp31 += remain
                log(f"[resource] 7 天内到期={round(exp7, 2)}  31 天内到期={round(exp31, 2)}")
                break
            except Exception as exc:  # noqa: BLE001
                log(f"[resource] {bhost} FAIL {type(exc).__name__}: {exc}")

        log("[done ] 探针结束（未触发任何签到动作）")


if __name__ == "__main__":
    main()
#!/usr/bin/env python3
"""Phase 0.2 验证：纯 HTTP（不启动浏览器）完成 Trae 全链路。

链路：长期 cookie（trae-web-state.json）→ GetUserToken 换 8h JWT → 调全部数据接口。
- 只读，绝不调用 claim
- 只打印统计量，不打印 token / cookie / 对话内容
"""
import json
import pathlib
import time

import httpx

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
STATE_FILE = STATE_DIR / "trae-web-state.json"
BASE = "https://api.trae.cn"
TOKEN_URL = f"{BASE}/cloudide/api/v3/common/GetUserToken"
COOKIE_NAME = "X-Cloudide-Session"

CST = 8 * 3600


def auth_headers(jwt):
    return {
        "Authorization": f"Cloud-IDE-JWT {jwt}",
        "X-User-Region": "cn",
        "Content-Type": "application/json",
        "Accept": "application/json, text/plain, */*",
        "Origin": "https://www.trae.cn",
        "Referer": "https://www.trae.cn/",
        "User-Agent": (
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
            "(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
        ),
    }


def main():
    cookies = json.loads(STATE_FILE.read_text())
    session = next(c["value"] for c in cookies if c["name"] == COOKIE_NAME)

    now = int(time.time())
    day_start = ((now + CST) // 86400) * 86400 - CST
    end = day_start + 86400 - 1
    start = end - 7 * 86400

    with httpx.Client(timeout=30) as c:
        # 1) cookie → JWT
        r = c.post(TOKEN_URL, headers={"X-Cloudide-Session": session}, json={})
        print(f"[换票] HTTP {r.status_code}")
        result = (r.json() or {}).get("Result") or {}
        jwt = result.get("Token")
        print(f"[换票] 拿到 JWT: {bool(jwt)}, 长度 {len(jwt or '')}, ExpiredAt={result.get('ExpiredAt')}")
        if not jwt:
            print("[换票] 失败，中止")
            return
        h = auth_headers(jwt)

        # 2) 签到状态（只读）
        r = c.post(f"{BASE}/trae/api/v2/ug/checkin_credits/status", headers=h,
                   json={"trae_client": "IDE", "req_source": 1, "client_version": "3.4.2"})
        d = r.json()
        print(f"[status ] HTTP {r.status_code} " + json.dumps(
            {k: d.get(k) for k in ("checked_in", "code", "credits", "enable", "did_checked_in", "extra_credits")},
            ensure_ascii=False))

        # 3) billing 状态
        r = c.post(f"{BASE}/trae/api/v2/pay/cn_credits_billing_status", headers=h, json={})
        print(f"[billing] HTTP {r.status_code} body={r.text[:200]}")

        # 4) 积分批次台账
        r = c.post(f"{BASE}/trae/api/v2/pay/user_current_entitlement_list", headers=h,
                   json={"require_usage": True, "full_data": True})
        d = r.json()
        summary = d.get("usage_summary") or {}
        packs = d.get("user_entitlement_pack_list") or []
        total = summary.get("total_amount")
        consumed = summary.get("consumed_amount")
        print(f"[ents   ] HTTP {r.status_code} 批次数={len(packs)} total={total} consumed={consumed} "
              f"剩余={None if total is None else round(total - (consumed or 0), 2)}")
        exp7 = exp31 = 0.0
        near = []
        for p in packs:
            base = p.get("entitlement_base_info") or {}
            et = p.get("expire_time") or base.get("end_time")
            if not isinstance(et, (int, float)):
                continue
            limit = (p.get("quota") or base.get("quota") or {}).get("credits_limit") or 0
            used = (p.get("usage") or {}).get("credits_amount") or 0
            remain = limit - used
            if remain <= 0:
                continue
            if et <= now:
                continue
            if et <= now + 7 * 86400:
                exp7 += remain
                near.append({"id": p.get("entitlement_id") or base.get("entitlement_id"),
                             "remain": round(remain, 2),
                             "expire": time.strftime("%Y-%m-%d %H:%M", time.localtime(et))})
            if et <= now + 31 * 86400:
                exp31 += remain
        print(f"[ents   ] 7 天内到期={round(exp7, 2)}  31 天内到期={round(exp31, 2)}")
        for item in near[:10]:
            print(f"          - {item['id']} 剩 {item['remain']} 于 {item['expire']} 过期")

        # 5) 过期记录
        r = c.post(f"{BASE}/trae/api/v2/pay/expired_ents", headers=h, json={})
        d = r.json()
        lst = d.get("expired_ent_list") or []
        print(f"[expired] HTTP {r.status_code} 条数={len(lst)} 总额={sum(x.get('credits_limit', 0) for x in lst)}")

        # 6) 会话用量（近 7 天）
        r = c.post(f"{BASE}/trae/api/v1/pay/query_user_usage_group_by_session", headers=h,
                   json={"start_time": start, "end_time": end, "page_size": 20, "page_num": 1, "usage_type": [7]})
        d = r.json()
        sessions = d.get("user_usage_group_by_sessions") or []
        print(f"[usage  ] HTTP {r.status_code} total={d.get('total')} 返回条数={len(sessions)} "
              f"近7天消耗={round(sum(s.get('credits_float', 0) for s in sessions), 2)}")


if __name__ == "__main__":
    main()
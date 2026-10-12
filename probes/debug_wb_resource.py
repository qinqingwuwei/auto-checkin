#!/usr/bin/env python3
"""WorkBuddy 积分批次台账：完整拉取 + 到期语义分析（只读）。

结论（2026-10-12）：
- `PackageEndTimeRangeBegin/End` 必须是**字符串**（Go 后端 string 字段），数字会 400；
- 最小体 `{"PageNumber":1,"PageSize":100,"ProductCode":"p_tcaca"}` 即可返回全部台账。

全量响应落盘 ~/.checkin/probe-capture-wb/resource-full.json。
"""
import json
import pathlib
import time

import httpx

CAP = pathlib.Path.home() / ".checkin" / "probe-capture-wb"
CAP.mkdir(parents=True, exist_ok=True)

cred = json.loads((pathlib.Path.home() / ".checkin" / "workbuddy-oauth.json").read_text())
token, uid = cred["accessToken"], cred["uid"]

H = {
    "Authorization": f"Bearer {token}",
    "User-Agent": "WorkBuddy/5.5.6",
    "Accept": "application/json, text/plain, */*",
    "Origin": "https://www.codebuddy.cn",
    "Referer": "https://www.codebuddy.cn/",
    "Content-Type": "application/json",
    "X-User-Id": str(uid),
}

with httpx.Client(timeout=20) as c:
    r = c.post("https://www.codebuddy.cn/v2/billing/meter/get-user-resource", headers=H,
               json={"PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca"})
    print("[resource] HTTP", r.status_code)
    d = r.json()
    (CAP / "resource-full.json").write_text(json.dumps(d, ensure_ascii=False, indent=1))
    data = d.get("data", {}).get("Response", {}).get("Data", {})
    accts = data.get("Accounts", [])
    print(f"[resource] 包数={len(accts)}  TotalCount={data.get('TotalCount')}  "
          f"TotalDosage={data.get('TotalDosage')}")
    if accts:
        print("[resource] 首包全字段: " + json.dumps(accts[0], ensure_ascii=False)[:1500])
    now = time.time()
    exp7 = exp31 = 0.0
    for i, a in enumerate(accts):
        fields = {k: a.get(k) for k in (
            "DealName", "CapacityRemain", "CapacityUsed", "CapacitySize",
            "DeductionBeginTime", "DeductionEndTime", "CycleStartTime", "CycleEndTime",
            "Status", "PackageName", "ProductName") if k in a}
        print(f"  [{i}] " + json.dumps(fields, ensure_ascii=False))
        end = a.get("DeductionEndTime")
        end_s = end / 1000 if isinstance(end, (int, float)) and end > 10**10 else None
        remain = a.get("CapacityRemain") or 0
        if end_s and remain > 0 and end_s > now:
            if end_s <= now + 7 * 86400:
                exp7 += remain
            if end_s <= now + 31 * 86400:
                exp31 += remain
    print(f"[resource] 7 天内到期={round(exp7, 2)}  31 天内到期={round(exp31, 2)}")
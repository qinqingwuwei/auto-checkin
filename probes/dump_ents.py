#!/usr/bin/env python3
"""受控对比：找出让 user_current_entitlement_list 返回「完整批次字段」的必要条件。

变体：仅 JWT / JWT+cookie / 仅 cookie
"""
import json
import pathlib

import httpx

HOME = pathlib.Path.home()
STATE_DIR = HOME / ".checkin"
URL = "https://api.trae.cn/trae/api/v2/pay/user_current_entitlement_list"
BODY = {"require_usage": True, "full_data": True}

cookies = json.loads((STATE_DIR / "trae-web-state.json").read_text())
session = next(c["value"] for c in cookies if c["name"] == "X-Cloudide-Session")
cookie_str = "; ".join(c["name"] + "=" + c["value"] for c in cookies if "trae" in c.get("domain", ""))

BASE = {
    "Content-Type": "application/json",
    "Accept": "application/json, text/plain, */*",
    "Accept-Language": "zh-CN,zh;q=0.9",
    "Origin": "https://www.trae.cn",
    "Referer": "https://www.trae.cn/",
    "User-Agent": ("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
                   "(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"),
}


def probe(label, extra):
    h = dict(BASE)
    h.update(extra)
    try:
        with httpx.Client(timeout=30) as c:
            r = c.post(URL, headers=h, json=BODY)
        d = r.json()
        packs = d.get("user_entitlement_pack_list") or []
        first = packs[0] if packs else {}
        quota = (first.get("quota") or {}).get("credits_limit")
        has_base = bool(first.get("entitlement_base_info"))
        print(f"[{label:14s}] HTTP {r.status_code} packs={len(packs)} "
              f"quota={'OK:' + str(quota) if quota else 'null'} base_info={has_base}")
    except Exception as exc:  # noqa: BLE001
        print(f"[{label:14s}] FAIL {type(exc).__name__}: {exc}")


with httpx.Client(timeout=30) as c:
    jwt = c.post("https://api.trae.cn/cloudide/api/v3/common/GetUserToken",
                 headers={"X-Cloudide-Session": session}, json={}).json()["Result"]["Token"]

jwt_h = {"Authorization": f"Cloud-IDE-JWT {jwt}", "X-User-Region": "cn"}
probe("仅 JWT", jwt_h)
probe("JWT+cookie", {**jwt_h, "Cookie": cookie_str})
probe("仅 cookie", {"Cookie": cookie_str})
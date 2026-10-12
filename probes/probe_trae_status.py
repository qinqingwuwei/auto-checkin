#!/usr/bin/env python3
"""Phase 0.1 探针（变体矩阵）：定位 Trae CN `checkin_credits/status` 的正确鉴权方式。

约束：
- 只调用 status（查询），绝不调用 claim（领取）
- 不打印任何 token 明文；只打印长度与来源标签
- 依赖仅标准库
"""
import base64
import json
import pathlib
import urllib.error
import urllib.request

HOME = pathlib.Path.home()
JWT_PATH = HOME / ".trae-cn" / "trae-jwt-token"
MACHINE_PATH = HOME / "Library/Application Support/Trae CN/machineid"
STORAGE_PATH = HOME / "Library/Application Support/Trae CN/User/globalStorage/storage.json"

API = "https://api.trae.cn/trae/api/v2/ug/checkin_credits/status"


def b64url_decode(seg: str) -> bytes:
    return base64.urlsafe_b64decode(seg + "=" * (-len(seg) % 4))


def describe_jwt(token: str) -> dict:
    """解析 JWT payload，只输出非敏感信息（claim 名、iss/type/exp）。"""
    payload = json.loads(b64url_decode(token.split(".")[1]))
    data = payload.get("data", {})
    print("[jwt ] claims:", sorted(payload.keys()))
    print("[jwt ] iss:", payload.get("iss"), "| data.type:", data.get("type"))
    print("[jwt ] exp:", payload.get("exp"), "| data keys:", sorted(data.keys()))
    return payload


def call(label, auth_value, extra=None):
    body = json.dumps({"req_source": 1}).encode()
    req = urllib.request.Request(API, data=body, method="POST")
    req.add_header("Authorization", auth_value)
    req.add_header("Content-Type", "application/json")
    req.add_header("x-device-id", MACHINE_PATH.read_text().strip())
    req.add_header("X-User-Region", "cn")
    req.add_header("User-Agent", "Trae/3.4.2")
    for k, v in (extra or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            text = resp.read().decode("utf-8", "replace")[:600]
            print(f"[{label}] HTTP {resp.status} {text}")
    except urllib.error.HTTPError as exc:
        print(f"[{label}] HTTP {exc.code} {exc.read().decode('utf-8', 'replace')[:600]}")
    except Exception as exc:  # noqa: BLE001
        print(f"[{label}] FAIL {type(exc).__name__}: {exc}")


def main() -> None:
    jwt = JWT_PATH.read_text().strip()
    payload = describe_jwt(jwt)
    data = payload.get("data", {})

    storage = json.loads(STORAGE_PATH.read_text())
    icube = {
        k: v
        for k, v in storage.items()
        if isinstance(k, str) and k.startswith("iCubeAuthInfo://")
    }
    print("[store] iCubeAuthInfo keys:", list(icube.keys()))

    call("B_bearer_jwt", f"Bearer {jwt}")
    call(
        "C_cloudide_jwt_extra",
        f"Cloud-IDE-JWT {jwt}",
        {"x-tenant-id": str(data.get("tenant_id", "")), "x-uid": str(data.get("user_id", ""))},
    )
    for key, val in icube.items():
        if isinstance(val, str) and len(val) > 100:
            call(f"D_{key.split('://')[1][:18]}_len{len(val)}", f"Cloud-IDE-JWT {val}")


if __name__ == "__main__":
    main()
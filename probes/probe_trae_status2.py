#!/usr/bin/env python3
"""Phase 0.1 探针（第二轮）：Trae CN `checkin_credits/status` 鉴权定位。

约束：
- 只调用 status（查询），绝不调用 claim（领取）
- 不打印任何 token 明文；只打印长度与来源标签
- 依赖仅标准库（兼容 python3.9）
"""
import json
import pathlib
import urllib.error
import urllib.request

HOME = pathlib.Path.home()
JWT_PATH = HOME / ".trae-cn" / "trae-jwt-token"
MACHINE_PATH = HOME / "Library/Application Support/Trae CN/machineid"
STORAGE_PATH = HOME / "Library/Application Support/Trae CN/User/globalStorage/storage.json"

API = "https://api.trae.cn/trae/api/v2/ug/checkin_credits/status"
WEB_HEADERS = {
    "Origin": "https://www.trae.cn",
    "Referer": "https://www.trae.cn/",
}


def call(label, auth_value=None, device_id=None, req_source=1, web_headers=False):
    body = json.dumps({"req_source": req_source}).encode()
    req = urllib.request.Request(API, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-User-Region", "cn")
    req.add_header("User-Agent", "Trae/3.4.2")
    if auth_value:
        req.add_header("Authorization", auth_value)
    if device_id:
        req.add_header("x-device-id", device_id)
    if web_headers:
        for k, v in WEB_HEADERS.items():
            req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            text = resp.read().decode("utf-8", "replace")[:500]
            print(f"[{label}] HTTP {resp.status} {text}")
    except urllib.error.HTTPError as exc:
        print(f"[{label}] HTTP {exc.code} {exc.read().decode('utf-8', 'replace')[:500]}")
    except Exception as exc:  # noqa: BLE001
        print(f"[{label}] FAIL {type(exc).__name__}: {exc}")


def main():
    jwt = JWT_PATH.read_text().strip()
    machine = MACHINE_PATH.read_text().strip()
    storage = json.loads(STORAGE_PATH.read_text())
    dev_device_id = storage.get("telemetry.devDeviceId")

    print(f"[info] jwt len={len(jwt)} | machineid len={len(machine)} "
          f"| devDeviceId len={len(dev_device_id or '')}")

    call("H_no_auth_control")
    call("E_jwt_webhdr", f"Cloud-IDE-JWT {jwt}", machine, web_headers=True)
    call("F_jwt_webhdr_src2", f"Cloud-IDE-JWT {jwt}", machine, req_source=2, web_headers=True)
    call("G_devdeviceid", f"Cloud-IDE-JWT {jwt}", dev_device_id, web_headers=True)


if __name__ == "__main__":
    main()
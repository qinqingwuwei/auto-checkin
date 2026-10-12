#!/usr/bin/env python3
"""Phase 0.1 探针（第三轮）：按客户端源码还原完整请求，并用垃圾 token 做对照。

依据 /Applications/Trae CN.app/.../out/main.js 中客户端自己的构造：
  Authorization: `Cloud-IDE-JWT ${token}` , X-User-Region: <region>
  fb(): x-device-id / x-device-brand / x-device-type / x-os-version / x-app-version
  Dqe(): body = { trae_client, device_id, req_source, client_version }
约束：只调 status，绝不调 claim；不打印任何凭据明文。
"""
import json
import pathlib
import subprocess
import urllib.error
import urllib.request

HOME = pathlib.Path.home()
JWT_PATH = HOME / ".trae-cn" / "trae-jwt-token"
MACHINE_PATH = HOME / "Library/Application Support/Trae CN/machineid"
STORAGE_PATH = HOME / "Library/Application Support/Trae CN/User/globalStorage/storage.json"

API = "https://api.trae.cn/trae/api/v2/ug/checkin_credits/status"
APP_VERSION = "3.4.2"


def sh(cmd, default=""):
    try:
        return subprocess.check_output(cmd, text=True).strip()
    except Exception:  # noqa: BLE001
        return default


def build_headers(token, device_id):
    return {
        "Authorization": f"Cloud-IDE-JWT {token}",
        "X-User-Region": "cn",
        "Content-Type": "application/json",
        "x-device-id": device_id,
        "x-device-brand": sh(["sysctl", "-n", "hw.model"], "MacBookPro"),
        "x-device-type": "macOS",
        "x-os-version": sh(["sw_vers", "-productVersion"], "15.0"),
        "x-app-version": APP_VERSION,
        "User-Agent": f"Trae/{APP_VERSION}",
    }


def call(label, token, device_id):
    body = json.dumps({
        "trae_client": "IDE",
        "device_id": device_id,
        "req_source": 1,
        "client_version": APP_VERSION,
    }).encode()
    req = urllib.request.Request(API, data=body, method="POST")
    for k, v in build_headers(token, device_id).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            print(f"[{label}] HTTP {resp.status} {resp.read().decode('utf-8','replace')[:400]}")
    except urllib.error.HTTPError as exc:
        print(f"[{label}] HTTP {exc.code} {exc.read().decode('utf-8','replace')[:400]}")
    except Exception as exc:  # noqa: BLE001
        print(f"[{label}] FAIL {type(exc).__name__}: {exc}")


def main():
    jwt = JWT_PATH.read_text().strip()
    machine = MACHINE_PATH.read_text().strip()
    storage = json.loads(STORAGE_PATH.read_text())
    dev_device = storage.get("telemetry.devDeviceId", "")

    print(f"[env ] hw={sh(['sysctl','-n','hw.model'])} os={sh(['sw_vers','-productVersion'])}")
    print(f"[info] jwt len={len(jwt)} machineid len={len(machine)} devDeviceId len={len(dev_device)}")

    call("A_garbage_token_control", "AAAA.garbage.BBBB", machine)
    call("B_real_jwt_machineid", jwt, machine)
    call("C_real_jwt_devDeviceId", jwt, dev_device)


if __name__ == "__main__":
    main()
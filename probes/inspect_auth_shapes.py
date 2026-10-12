#!/usr/bin/env python3
"""检查 storage.json 中三个 iCubeAuthInfo 凭据形态（只读，不打印凭据内容）。"""
import base64
import json
import pathlib

STORAGE = (pathlib.Path.home() / "Library/Application Support/Trae CN"
           / "User/globalStorage/storage.json")


def describe(value):
    dots = value.count(".")
    info = {
        "len": len(value),
        "jwt_shaped": dots == 2 and value.startswith("ey"),
        "dots": dots,
        "charset": "urlsafe-b64" if all(
            c.isalnum() or c in "-_=" for c in value[:200]) else "mixed",
    }
    if info["jwt_shaped"]:
        try:
            seg = value.split(".")[1]
            payload = json.loads(base64.urlsafe_b64decode(seg + "=" * (-len(seg) % 4)))
            info["claims"] = sorted(payload.keys())
            info["iss"] = payload.get("iss")
            info["exp"] = payload.get("exp")
        except Exception as exc:  # noqa: BLE001
            info["payload_err"] = type(exc).__name__
    return info


def main():
    storage = json.loads(STORAGE.read_text())
    for key, val in storage.items():
        if isinstance(key, str) and key.startswith("iCubeAuthInfo://") and isinstance(val, str):
            print(f"{key}\n   -> {describe(val)}")


if __name__ == "__main__":
    main()
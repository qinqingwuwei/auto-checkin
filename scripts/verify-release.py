#!/usr/bin/env python3
"""Validate the complete release asset set before publication."""
import hashlib
from pathlib import Path
import sys
import zipfile

directory = Path(sys.argv[1])
version = sys.argv[2]
targets = ("darwin-arm64", "darwin-amd64", "windows-amd64", "linux-amd64")
expected = {f"checkin-{version}-{target}.zip" for target in targets}
actual = {p.name for p in directory.glob("*.zip")}
if actual != expected:
    raise SystemExit(f"发布 ZIP 集合不匹配：{actual ^ expected}")
checksums = {}
for line in (directory / "SHA256SUMS").read_text().splitlines():
    digest, name = line.split(maxsplit=1)
    if name in checksums:
        raise SystemExit(f"重复的校验项：{name}")
    checksums[name] = digest
if checksums.keys() != expected:
    raise SystemExit("SHA256SUMS 必须且只能覆盖四个平台 ZIP")
for name in sorted(expected):
    path = directory / name
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if digest != checksums[name]:
        raise SystemExit(f"SHA256 不匹配：{name}")
    windows = "-windows-" in name
    executable = "checkin.exe" if windows else "checkin"
    setup = "setup.ps1" if windows else "setup.sh"
    with zipfile.ZipFile(path) as archive:
        if set(archive.namelist()) != {executable, setup, "README.md"} or len(archive.infolist()) != 3:
            raise SystemExit(f"ZIP 内容不匹配：{name}")
        if archive.testzip() is not None:
            raise SystemExit(f"ZIP 损坏：{name}")
        binary = archive.getinfo(executable)
        if binary.file_size > 12 * 1024 * 1024:
            raise SystemExit(f"二进制超过 12 MiB：{name}")
        if not windows and ((binary.external_attr >> 16) & 0o111) != 0o111:
            raise SystemExit(f"二进制缺少执行权限：{name}")
    print(f"已验证 {name} ({path.stat().st_size} bytes)")

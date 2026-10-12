"""凭据读写公共约定：一律落 ``~/.checkin/``、一律 0600、一律原子写。"""
from __future__ import annotations

import logging
import pathlib

from checkin.util import read_json, write_json

log = logging.getLogger(__name__)


class CredentialError(Exception):
    """凭据缺失 / 无效 / 无法续期（需要用户重新登录）。"""


def load_credential(path: pathlib.Path) -> dict | None:
    data = read_json(path)
    if data is None:
        return None
    if path.exists():
        mode = path.stat().st_mode & 0o777
        if mode != 0o600:
            log.warning("凭据文件权限为 %o（建议 600）：%s", mode, path)
    return data if isinstance(data, dict) else None


def save_credential(path: pathlib.Path, data: dict) -> None:
    write_json(path, data, mode=0o600)
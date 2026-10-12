"""日志：控制台 + ``~/.checkin/logs/checkin.log``（轮转，始终写）。

本地日志是通知的兜底通道：即使钉钉/本地通知都不可用，历史也一定可查。
"""
from __future__ import annotations

import logging
import logging.handlers

from checkin import paths

LOG_FILE_MAX_BYTES = 2 * 1024 * 1024
LOG_FILE_BACKUPS = 5

_configured = False
_console_handler: logging.StreamHandler | None = None


def setup_logging(verbose: bool = False) -> logging.Logger:
    """初始化日志（幂等；verbose 只影响控制台级别，文件始终记录全量）。"""
    global _configured, _console_handler
    logger = logging.getLogger("checkin")
    if _configured:
        if _console_handler is not None:
            _console_handler.setLevel(logging.DEBUG if verbose else logging.INFO)
        return logger

    logger.setLevel(logging.DEBUG)
    logger.propagate = False
    paths.ensure_dirs()

    fmt = logging.Formatter(
        "%(asctime)s %(levelname)-7s %(name)s: %(message)s", "%Y-%m-%d %H:%M:%S"
    )
    file_handler = logging.handlers.RotatingFileHandler(
        paths.log_path(), maxBytes=LOG_FILE_MAX_BYTES, backupCount=LOG_FILE_BACKUPS, encoding="utf-8"
    )
    file_handler.setLevel(logging.DEBUG)
    file_handler.setFormatter(fmt)

    console = logging.StreamHandler()
    console.setLevel(logging.DEBUG if verbose else logging.INFO)
    console.setFormatter(logging.Formatter("%(message)s"))

    logger.addHandler(file_handler)
    logger.addHandler(console)
    _console_handler = console

    # 第三方库降噪
    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)
    logging.getLogger("playwright").setLevel(logging.WARNING)

    _configured = True
    return logger
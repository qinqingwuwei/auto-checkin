"""HTTP 重试封装。

策略（与计划一致）：**只对网络错误与 5xx（含 429）做指数退避重试（带抖动）；
4xx 一律不重试**（多为签名/配置/鉴权错误，重试无意义）。
"""
from __future__ import annotations

import logging
import random
import time

import httpx

log = logging.getLogger(__name__)

RETRY_STATUS = {429, 500, 502, 503, 504}


def request(
    client: httpx.Client,
    method: str,
    url: str,
    *,
    retries: int = 3,
    backoff: float = 1.0,
    label: str = "",
    **kwargs,
) -> httpx.Response:
    """带重试的请求。重试耗尽后：网络错误 raise，5xx 返回最后一次响应。"""
    who = label or url
    last_exc: Exception | None = None
    for attempt in range(retries):
        try:
            resp = client.request(method, url, **kwargs)
        except httpx.TransportError as exc:
            last_exc = exc
            if attempt == retries - 1:
                raise
            delay = backoff * (2 ** attempt) + random.uniform(0, backoff)
            log.warning(
                "%s 网络错误（%s），%.1fs 后重试（第 %d/%d 次）",
                who, type(exc).__name__, delay, attempt + 1, retries,
            )
            time.sleep(delay)
            continue
        if resp.status_code in RETRY_STATUS and attempt < retries - 1:
            delay = backoff * (2 ** attempt) + random.uniform(0, backoff)
            log.warning(
                "%s HTTP %s，%.1fs 后重试（第 %d/%d 次）",
                who, resp.status_code, delay, attempt + 1, retries,
            )
            time.sleep(delay)
            continue
        return resp
    if last_exc is not None:  # pragma: no cover - 逻辑上不可达
        raise last_exc
    raise RuntimeError("unreachable")  # pragma: no cover
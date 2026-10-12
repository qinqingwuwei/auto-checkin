"""钉钉加签单测：冻结向量（独立用 Python 现算后硬编码，防止实现与测试同源偏移）。"""
from __future__ import annotations

import base64
import hashlib
import hmac
import urllib.parse

from checkin.notify import dingtalk

SECRET = "SECtest0123456789abcdef"
TIMESTAMP = "1700000000000"
# 由 `python3 -c` 用标准库现算并冻结（2026-10-12）：
#   quote_plus(base64(hmac_sha256(secret, f"{ts}\n{secret}")))
EXPECTED_SIGN = "%2FlfnxUUu80FwVTsIlv2F1cMGBb%2BPxEKAwYh%2FbsRxXcc%3D"


def test_frozen_signature_vector():
    assert dingtalk.sign(SECRET, TIMESTAMP) == EXPECTED_SIGN


def test_sign_matches_reference_implementation():
    """与测试内独立实现对齐（双重保险，防止冻结向量被误改）。"""
    string_to_sign = f"{TIMESTAMP}\n{SECRET}"
    expected = urllib.parse.quote_plus(
        base64.b64encode(
            hmac.new(SECRET.encode(), string_to_sign.encode(), hashlib.sha256).digest()
        ).decode()
    )
    assert dingtalk.sign(SECRET, TIMESTAMP) == expected


def test_signed_url_with_query():
    url = dingtalk.signed_url("https://oapi.dingtalk.com/robot/send?access_token=tok", SECRET, TIMESTAMP)
    assert url.startswith("https://oapi.dingtalk.com/robot/send?access_token=tok&timestamp=")
    assert f"timestamp={TIMESTAMP}" in url
    assert f"sign={EXPECTED_SIGN}" in url


def test_signed_url_without_query_uses_question_mark():
    url = dingtalk.signed_url("https://example.com/send", SECRET, TIMESTAMP)
    assert url.startswith("https://example.com/send?timestamp=")


def test_signed_url_without_secret_returns_raw():
    assert dingtalk.signed_url("https://example.com/send", "", TIMESTAMP) == "https://example.com/send"


def test_payload_shape():
    data = dingtalk.payload("标题", "正文")
    assert data["msgtype"] == "markdown"
    assert data["markdown"]["title"] == "标题"
    assert data["markdown"]["text"] == "正文"
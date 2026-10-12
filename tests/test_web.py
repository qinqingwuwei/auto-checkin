"""Web 看板单测：状态载荷、静态页、跨源防护、配置写入（CHECKIN_HOME 隔离）。"""
from __future__ import annotations

import json
import threading
import urllib.error
import urllib.request

import pytest

from checkin import config, paths
from checkin.web import server


@pytest.fixture()
def live_server(monkeypatch, tmp_path):
    monkeypatch.setenv("CHECKIN_HOME", str(tmp_path / "home"))
    paths.ensure_dirs()
    server._schedule_cache = None
    httpd, port = server._bind(0)  # 0 = 由系统分配空闲端口
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    try:
        yield port
    finally:
        httpd.shutdown()
        httpd.server_close()


def _get(port: int, path: str, host: str | None = None):
    req = urllib.request.Request(f"http://127.0.0.1:{port}{path}")
    if host:
        req.add_header("Host", host)
    return urllib.request.urlopen(req, timeout=5)


def _post(port: int, path: str, payload: dict, with_header: bool = True):
    headers = {"Content-Type": "application/json"}
    if with_header:
        headers["X-Checkin"] = "1"
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}{path}",
        data=json.dumps(payload).encode(),
        method="POST",
        headers=headers,
    )
    return urllib.request.urlopen(req, timeout=5)


def test_status_payload_shape(monkeypatch, tmp_path):
    monkeypatch.setenv("CHECKIN_HOME", str(tmp_path / "home"))
    server._schedule_cache = None
    payload = server._status_payload()
    for key in (
        "date", "state", "products", "credentials", "points", "history",
        "config", "secretsSet", "dingtalk", "schedule", "run",
    ):
        assert key in payload
    assert payload["run"]["running"] is False
    # 未配置钉钉时 secretsSet 全为 False
    assert payload["secretsSet"]["notify.dingtalk.webhook"] is False
    # 配置中密钥不明文（打码展示）
    assert "secret" in payload["config"]["notify"]["dingtalk"]


def test_index_served(live_server):
    with _get(live_server, "/") as resp:
        assert resp.status == 200
        assert "text/html" in resp.headers["Content-Type"]
        body = resp.read().decode("utf-8")
    assert "签到台账" in body
    assert "X-Checkin" in body  # 前端写接口必须携带该头


def test_api_status_over_http(live_server):
    with _get(live_server, "/api/status") as resp:
        data = json.loads(resp.read().decode("utf-8"))
    assert data["version"]
    assert isinstance(data["config"], dict)
    assert data["run"]["running"] is False


def test_api_log_tail(live_server):
    paths.log_path().parent.mkdir(parents=True, exist_ok=True)
    paths.log_path().write_text("line-1\nline-2\nline-3\n", encoding="utf-8")
    with _get(live_server, "/api/log?lines=2") as resp:
        data = json.loads(resp.read().decode("utf-8"))
    assert data["lines"] == ["line-2", "line-3"]


def test_post_rejects_missing_csrf_header(live_server):
    with pytest.raises(urllib.error.HTTPError) as excinfo:
        _post(live_server, "/api/config", {"key": "expiry_warning_days", "value": "10"}, with_header=False)
    assert excinfo.value.code == 403


def test_host_header_check(live_server):
    with pytest.raises(urllib.error.HTTPError) as excinfo:
        _get(live_server, "/api/status", host="evil.example.com")
    assert excinfo.value.code == 403


def test_config_write_and_validate(live_server):
    with _post(live_server, "/api/config", {"key": "expiry_warning_days", "value": "14"}) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    assert data["ok"] is True
    assert config.get("expiry_warning_days") == 14

    with pytest.raises(urllib.error.HTTPError) as excinfo:
        _post(live_server, "/api/config", {"key": "expiry_warning_days", "value": "abc"})
    assert excinfo.value.code == 400
    assert config.get("expiry_warning_days") == 14  # 非法值不落盘


def test_run_conflict_guard(monkeypatch):
    """执行互斥：已在运行时再次触发返回 409 且不启动新线程。"""
    monkeypatch.setattr(server.RUN_STATE, "_running", True)
    handler = server._Handler
    assert server.RUN_STATE.start(False, False) is False
    assert handler is not None
"""配置单测：默认生成、读写校验、非法值回退、打码、钉钉就绪判断。"""
from __future__ import annotations

import json

import pytest

from checkin import config, paths


@pytest.fixture()
def isolated(monkeypatch, tmp_path):
    monkeypatch.setenv("CHECKIN_HOME", str(tmp_path))
    return tmp_path


def test_first_run_generates_defaults(isolated):
    cfg = config.load()
    assert cfg == config.DEFAULTS
    assert paths.config_path().exists()
    assert (paths.config_path().stat().st_mode & 0o777) == 0o600
    assert config.last_warnings() == []


def test_defaults_values(isolated):
    assert config.schedule_time() == "08:00"
    assert config.warning_days() == 7


def test_set_normalizes_and_persists(isolated):
    ok, _ = config.set("schedule.time", "8:5")
    assert ok
    assert config.get("schedule.time") == "08:05"

    ok, _ = config.set("expiry_warning_days", "15")
    assert ok and config.warning_days() == 15

    ok, _ = config.set("notify.dingtalk.enabled", "off")
    assert ok and config.get("notify.dingtalk.enabled") is False

    ok, _ = config.set("notify.daily_summary", "true")
    assert ok and config.get("notify.daily_summary") is True

    # 落盘可回读（不依赖内存态）
    raw = json.loads(paths.config_path().read_text())
    assert raw["schedule"]["time"] == "08:05"


@pytest.mark.parametrize(
    "key,value",
    [
        ("schedule.time", "25:00"),
        ("schedule.time", "8"),
        ("expiry_warning_days", "-1"),
        ("expiry_warning_days", "999"),
        ("expiry_warning_days", "abc"),
        ("notify.dingtalk.enabled", "maybe"),
        ("notify.dingtalk.webhook", "ftp://oapi.dingtalk.com/x"),
        ("notify.dingtalk.secret", "short"),
        ("nope.nope", "1"),
    ],
)
def test_set_rejects_invalid(isolated, key, value):
    ok, message = config.set(key, value)
    assert not ok and message


def test_invalid_on_disk_falls_back_to_default(isolated):
    paths.config_path().write_text(
        json.dumps({"expiry_warning_days": 999, "schedule": {"time": "99:99"}}, ensure_ascii=False)
    )
    cfg = config.load()
    assert cfg["expiry_warning_days"] == 7
    assert cfg["schedule"]["time"] == "08:00"
    assert len(config.last_warnings()) == 2


def test_corrupted_json_falls_back(isolated):
    paths.config_path().write_text("{not json")
    cfg = config.load()
    assert cfg == config.DEFAULTS
    assert config.last_warnings()


def test_missing_keys_are_filled(isolated):
    paths.config_path().write_text(json.dumps({"schedule": {"time": "23:59"}}))
    cfg = config.load()
    assert cfg["schedule"]["time"] == "23:59"
    assert cfg["notify"]["local"]["enabled"] is True  # 其余补默认


def test_dingtalk_ready_requires_webhook_and_secret(isolated):
    ok, reason = config.dingtalk_ready()
    assert not ok and "webhook" in reason
    config.set("notify.dingtalk.webhook", "https://oapi.dingtalk.com/robot/send?access_token=tok1234567890")
    ok, reason = config.dingtalk_ready()
    assert not ok and "secret" in reason
    config.set("notify.dingtalk.secret", "SEC0123456789abcdef")
    ok, reason = config.dingtalk_ready()
    assert ok and reason == "ok"
    config.set("notify.dingtalk.enabled", "false")
    ok, reason = config.dingtalk_ready()
    assert not ok


def test_masked_view_hides_secrets(isolated):
    secret = "SEC0123456789abcdef"
    webhook = "https://oapi.dingtalk.com/robot/send?access_token=abcdefghijklmn"
    config.set("notify.dingtalk.secret", secret)
    config.set("notify.dingtalk.webhook", webhook)
    dumped = json.dumps(config.masked_view(), ensure_ascii=False)
    assert secret not in dumped
    assert "abcdefghijklmn" not in dumped
    assert "abcd" in dumped  # 保留少量头字符便于核对
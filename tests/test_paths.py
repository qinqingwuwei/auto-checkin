"""路径解析单测：CHECKIN_HOME 覆盖、默认布局、项目根。"""
from __future__ import annotations

import pathlib

from checkin import paths


def test_state_dir_respects_env(monkeypatch, tmp_path):
    monkeypatch.setenv("CHECKIN_HOME", str(tmp_path / "home"))
    assert paths.state_dir() == tmp_path / "home"
    assert paths.config_path() == tmp_path / "home" / "config.json"
    assert paths.state_path() == tmp_path / "home" / "state.json"
    assert paths.points_path() == tmp_path / "home" / "points.jsonl"
    assert paths.trae_cred_path() == tmp_path / "home" / "credentials" / "trae.json"
    assert paths.workbuddy_cred_dir() == tmp_path / "home" / "credentials" / "workbuddy"
    assert paths.log_path() == tmp_path / "home" / "logs" / "checkin.log"


def test_state_dir_default(monkeypatch):
    monkeypatch.delenv("CHECKIN_HOME", raising=False)
    assert paths.state_dir() == pathlib.Path.home() / ".checkin"


def test_project_dir_has_pyproject():
    assert (paths.project_dir() / "pyproject.toml").exists()


def test_ensure_dirs_creates_layout(monkeypatch, tmp_path):
    monkeypatch.setenv("CHECKIN_HOME", str(tmp_path / "h"))
    paths.ensure_dirs()
    assert paths.logs_dir().is_dir()
    assert paths.credentials_dir().is_dir()
    assert paths.workbuddy_cred_dir().is_dir()


def test_legacy_paths_under_state_dir(monkeypatch, tmp_path):
    monkeypatch.setenv("CHECKIN_HOME", str(tmp_path / "h"))
    assert paths.legacy_trae_state_path() == tmp_path / "h" / "trae-web-state.json"
    legacy = paths.legacy_workbuddy_paths()
    assert legacy[0].name == "workbuddy-sms.json"  # SMS 版更新，优先导入
    assert legacy[1].name == "workbuddy-oauth.json"
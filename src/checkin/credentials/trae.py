"""Trae CN 凭据：网页会话 cookie（``X-Cloudide-Session``）→ ``Cloud-IDE-JWT``。

链路（计划 §5.4，✅ 已实测）::

    Playwright 持久化 profile 登录 trae.cn → cookie X-Cloudide-Session
      → POST api.trae.cn/cloudide/api/v3/common/GetUserToken → 8 小时 JWT

凭据文件：``~/.checkin/credentials/trae.json``（0600）::

    {session, sessionExpires, jwt, jwtExp, loginAt, savedAt, source}

**浏览器零下载（硬约束）**：登录优先复用系统已装 Chrome / Edge（Playwright
``channel``），仅当两者都不存在时才回退自带 Chromium（由 doctor 提示安装）。
"""
from __future__ import annotations

import logging
import os
import pathlib
import time

import httpx

from checkin import paths
from checkin.credentials.base import CredentialError, load_credential, save_credential
from checkin.net import request
from checkin.util import jwt_exp, read_json

log = logging.getLogger(__name__)

SESSION_COOKIE = "X-Cloudide-Session"
BASE = "https://api.trae.cn"
TOKEN_URL = BASE + "/cloudide/api/v3/common/GetUserToken"
LOGIN_URL = "https://www.trae.cn/"
LOGIN_WAIT_SECONDS = 480
JWT_REFRESH_MARGIN = 600  # 提前 10 分钟换票
BROWSER_CHANNELS = ("chrome", "msedge", "chromium")

_CHROME_PATHS = (
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    os.path.expandvars(r"%ProgramFiles%\Google\Chrome\Application\chrome.exe"),
    os.path.expandvars(r"%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe"),
    os.path.expandvars(r"%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe"),
)
_EDGE_PATHS = (
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    os.path.expandvars(r"%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe"),
    os.path.expandvars(r"%ProgramFiles%\Microsoft\Edge\Application\msedge.exe"),
)


class _ChannelUnavailable(Exception):
    """该浏览器通道不可用（换下一个通道试）。"""


def _channel_executable(channel: str) -> str | None:
    candidates = _CHROME_PATHS if channel == "chrome" else _EDGE_PATHS
    for candidate in candidates:
        if candidate and pathlib.Path(candidate).exists():
            return candidate
    return None


def browser_channels_available() -> list[str]:
    """系统已装的可用浏览器通道（按优先序，零下载）。"""
    return [ch for ch in BROWSER_CHANNELS if ch != "chromium" and _channel_executable(ch)]


def bundled_chromium_installed() -> bool:
    """Playwright 自带 Chromium 是否已安装（仅在前两者都缺失时才会用到）。"""
    if paths.is_windows():
        root = pathlib.Path(os.environ.get("LOCALAPPDATA", "")) / "ms-playwright"
    elif paths.is_macos():
        root = pathlib.Path.home() / "Library" / "Caches" / "ms-playwright"
    else:
        root = pathlib.Path.home() / ".cache" / "ms-playwright"
    try:
        return any(p.name.startswith("chromium") for p in root.iterdir() if p.is_dir())
    except OSError:
        return False


# ------------------------------------------------------------------ 读写

def load() -> dict | None:
    """读取凭据（缺失时尝试从 Phase 0 遗留 cookie 文件一次性导入）。"""
    cred = load_credential(paths.trae_cred_path())
    if cred and cred.get("session"):
        return cred
    return import_legacy()


def import_legacy() -> dict | None:
    """从 ``~/.checkin/trae-web-state.json``（Phase 0 探针遗留）导入会话 cookie。"""
    legacy = paths.legacy_trae_state_path()
    data = read_json(legacy)
    if not isinstance(data, list):
        return None
    cookie = next(
        (c for c in data if isinstance(c, dict) and c.get("name") == SESSION_COOKIE), None
    )
    if not cookie or not cookie.get("value"):
        return None
    expires = cookie.get("expires")
    cred = {
        "session": cookie["value"],
        "sessionExpires": float(expires) if isinstance(expires, (int, float)) and expires > 0 else None,
        "jwt": None,
        "jwtExp": None,
        "loginAt": None,
        "savedAt": int(time.time()),
        "source": "legacy:trae-web-state.json",
    }
    save_credential(paths.trae_cred_path(), cred)
    log.info("已从遗留文件导入 Trae 会话凭据：%s", legacy)
    return cred


def save(session: str, session_expires: float | None, source: str, **extra) -> dict:
    cred = {
        "session": session,
        "sessionExpires": session_expires,
        "savedAt": int(time.time()),
        "source": source,
    }
    cred.update(extra)
    cred.setdefault("loginAt", cred["savedAt"])
    save_credential(paths.trae_cred_path(), cred)
    return cred


def credential_status() -> dict:
    cred = load()
    if not cred:
        return {
            "configured": False,
            "message": "未登录：运行 `checkin login trae`（复用系统已装浏览器，零下载）",
        }
    return {
        "configured": bool(cred.get("session")),
        "source": cred.get("source") or "-",
        "sessionExpires": cred.get("sessionExpires"),
        "jwtExp": cred.get("jwtExp"),
        "loginAt": cred.get("loginAt"),
        "savedAt": cred.get("savedAt"),
    }


# ------------------------------------------------------------------ 换票

def mint_jwt(session: str) -> tuple[str, int | None]:
    """用网页会话 cookie 换取 Cloud-IDE-JWT。"""
    try:
        with httpx.Client(timeout=30) as client:
            resp = request(
                client,
                "POST",
                TOKEN_URL,
                headers={"X-Cloudide-Session": session, "Content-Type": "application/json"},
                json={},
                label="Trae 换票",
            )
    except httpx.HTTPError as exc:
        raise CredentialError(f"换票请求失败（网络）：{type(exc).__name__}: {exc}") from exc
    if resp.status_code in (401, 403):
        raise CredentialError("网页会话已失效（HTTP %d），请重新登录：checkin login trae" % resp.status_code)
    if resp.status_code != 200:
        raise CredentialError(f"换票失败 HTTP {resp.status_code}：{resp.text[:200]}")
    try:
        payload = resp.json()
    except ValueError as exc:
        raise CredentialError(f"换票响应非 JSON：{resp.text[:200]}") from exc
    result = payload.get("Result") or {}
    token = result.get("Token")
    if not token:
        raise CredentialError(
            "换票响应缺少 Result.Token（会话可能已失效，请重新登录：checkin login trae）"
        )
    return str(token), jwt_exp(str(token))


def jwt_token(force: bool = False) -> str:
    """取可用的 JWT（优先复用缓存，过期前自动换票）。"""
    cred = load()
    if not cred or not cred.get("session"):
        raise CredentialError("未登录：运行 `checkin login trae`")
    now = time.time()
    cached, cached_exp = cred.get("jwt"), cred.get("jwtExp")
    if not force and cached and cached_exp and int(cached_exp) > now + JWT_REFRESH_MARGIN:
        return str(cached)
    token, exp = mint_jwt(cred["session"])
    cred.update({"jwt": token, "jwtExp": exp, "refreshedAt": int(now)})
    save_credential(paths.trae_cred_path(), cred)
    log.info("Trae JWT 已换发（%s 过期）", time.strftime("%Y-%m-%d %H:%M", time.localtime(exp)) if exp else "未知时间")
    return token


# ------------------------------------------------------------------ 浏览器登录

def _run_browser_login(channel: str, timeout: float) -> tuple[str, float | None]:
    """在指定浏览器通道里打开独立配置的登录窗口，等 cookie 出现。"""
    try:
        from playwright.sync_api import sync_playwright
    except ImportError as exc:  # pragma: no cover
        raise CredentialError("playwright 未安装：执行 `uv sync` 后重试") from exc

    profile = paths.trae_browser_profile_dir()
    profile.mkdir(parents=True, exist_ok=True)
    launch_kwargs = {"headless": False}
    if channel != "chromium":
        if not _channel_executable(channel):
            raise _ChannelUnavailable(f"未找到 {channel} 浏览器")
        launch_kwargs["channel"] = channel

    with sync_playwright() as pw:
        try:
            context = pw.chromium.launch_persistent_context(str(profile), **launch_kwargs)
        except Exception as exc:  # noqa: BLE001 - 通道不可用/被占用
            raise _ChannelUnavailable(f"启动 {channel} 失败：{type(exc).__name__}: {exc}") from exc
        try:
            page = context.new_page()
            try:
                page.goto(LOGIN_URL, wait_until="commit", timeout=60000)
            except Exception as exc:  # noqa: BLE001
                log.warning("登录页导航告警（继续等待）：%s", exc)

            names = {c["name"] for c in context.cookies()}
            if SESSION_COOKIE in names:
                log.info("检测到已保存的登录会话（复用持久化 profile）")
            else:
                log.info(
                    "请在打开的 %s 窗口中登录 trae.cn（最多等待 %d 秒）",
                    "Chrome" if channel == "chrome" else ("Edge" if channel == "msedge" else "Chromium"),
                    int(timeout),
                )
            deadline = time.time() + timeout
            while SESSION_COOKIE not in {c["name"] for c in context.cookies()}:
                if time.time() >= deadline:
                    raise CredentialError(
                        f"登录超时（{int(timeout)} 秒内未检测到 {SESSION_COOKIE}），请重试"
                    )
                page.wait_for_timeout(2000)

            cookies = context.cookies()
            cookie = next(c for c in cookies if c["name"] == SESSION_COOKIE)
            expires = cookie.get("expires")
            session = cookie["value"]
            # 整份 cookie 一起留档，便于排障（0600）
            save_credential(
                paths.state_dir() / "browser" / "trae-cookies.json",
                {c["name"]: c for c in cookies},
            )
        finally:
            context.close()
    return session, float(expires) if isinstance(expires, (int, float)) and expires > 0 else None


def browser_login(timeout: float = LOGIN_WAIT_SECONDS, channel: str | None = None) -> dict:
    """登录并保存会话 cookie（通道优先序：chrome → msedge → chromium）。"""
    paths.ensure_dirs()
    channels = [channel] if channel else (browser_channels_available() or BROWSER_CHANNELS[-1:])
    last_error: Exception | None = None
    for ch in channels:
        if ch == "chromium" and not bundled_chromium_installed():
            raise CredentialError(
                "系统未安装 Chrome / Edge，且未找到 Playwright 自带 Chromium。"
                "请安装 Chrome，或执行：uv run playwright install chromium（约 170MB，一次性）"
            )
        try:
            session, expires = _run_browser_login(ch, timeout)
        except _ChannelUnavailable as exc:
            log.warning("浏览器通道 %s 不可用：%s", ch, exc)
            last_error = exc
            continue
        cred = save(session, expires, source=f"browser:{ch}", loginAt=int(time.time()))
        log.info("Trae 登录成功（通道 %s），会话有效期至 %s", ch, _fmt_expire(expires))
        return cred
    raise CredentialError(f"所有浏览器通道均不可用：{last_error}")


def _fmt_expire(expires: float | None) -> str:
    if not expires:
        return "未知"
    return time.strftime("%Y-%m-%d %H:%M", time.localtime(expires))
"""WorkBuddy 凭据：插件 OAuth（系统浏览器）或 SMS 直登（纯 API），均支持自动续期。

两条一等公民登录路径（计划 §7.4，✅ 均已实测）：
1. OAuth：``POST /v2/plugin/auth/state?platform=CLI`` → 系统默认浏览器登录（微信扫码）
   → 轮询 ``/v2/plugin/auth/token?state=``；
2. SMS：``POST www.workbuddy.cn/v2/plugin/login/send-sms`` → ``/v2/plugin/login/token``
   （``login_method=phone``），纯 API 零浏览器。

续期：``POST copilot.tencent.com/v2/plugin/auth/token/refresh``（``X-Refresh-Token`` +
``X-Auth-Refresh-Source: plugin``），refresh token 实测有效期约 60 天。

多账号：``~/.checkin/credentials/workbuddy/<uid>.json``（0600，每账号独立文件、
不共用任何设备标识）。
"""
from __future__ import annotations

import logging
import time

import httpx

from checkin import paths
from checkin.credentials.base import CredentialError, load_credential, save_credential
from checkin.net import request
from checkin.util import copy_to_clipboard, jwt_exp, jwt_payload, mask_phone, read_json

log = logging.getLogger(__name__)

SMS_HOST = "https://www.workbuddy.cn"
SEND_SMS_URL = SMS_HOST + "/v2/plugin/login/send-sms"
LOGIN_URL = SMS_HOST + "/v2/plugin/login/token"
REFRESH_URL = "https://copilot.tencent.com/v2/plugin/auth/token/refresh"
PLUGIN_HOSTS = (
    "https://copilot.tencent.com",
    "https://www.codebuddy.cn",
    "https://www.workbuddy.cn",
)
OAUTH_WAIT_SECONDS = 300
REFRESH_MARGIN = 6 * 3600  # AT 剩余不足 6 小时就提前续期

PLUGIN_UA = "CLI/2.63.2 CodeBuddy/2.63.2"
SMS_UA = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/138.0.7204.251 Safari/537.36"
)
PLUGIN_HEADERS = {
    "Accept": "application/json, text/plain, */*",
    "X-Requested-With": "XMLHttpRequest",
    "Origin": "https://www.codebuddy.cn",
    "Referer": "https://www.codebuddy.cn/",
    "Content-Type": "application/json",
}


def _plugin_headers(ua: str = PLUGIN_UA) -> dict:
    return {**PLUGIN_HEADERS, "User-Agent": ua}


def _sms_headers() -> dict:
    return {
        "Content-Type": "application/json",
        "Accept": "application/json, text/plain, */*",
        "Origin": SMS_HOST,
        "Referer": SMS_HOST + "/",
        "User-Agent": SMS_UA,
    }


def _dig(obj, keys: list[str]):
    """递归取第一个命中的键（不区分大小写，跳过空值）。"""
    from checkin.util import dig  # 局部导入避免循环

    return dig(obj, keys)


# ------------------------------------------------------------------ 读写

def _cred_path(uid: str):
    return paths.workbuddy_cred_dir() / f"{uid}.json"


def _import_legacy() -> list[str]:
    """从 Phase 0 遗留凭据（SMS 优先）导入到多账号目录，返回导入的 uid 列表。"""
    imported: list[str] = []
    for legacy in paths.legacy_workbuddy_paths():
        data = read_json(legacy)
        if not isinstance(data, dict) or not data.get("accessToken"):
            continue
        uid = str(data.get("uid") or _dig(jwt_payload(data["accessToken"]), ["sub", "uid", "user_id"]) or "").strip()
        if not uid:
            log.warning("遗留凭据缺少 uid，跳过：%s", legacy)
            continue
        target = _cred_path(uid)
        if target.exists():
            continue
        expires = jwt_exp(data["accessToken"])
        if not expires and data.get("expiresIn"):
            try:
                expires = int(data["savedAt"]) + int(data["expiresIn"])
            except (TypeError, ValueError):
                expires = None
        login_method = "phone" if data.get("loginMethod") == "phone" else "browser"
        save_credential(
            target,
            {
                "uid": uid,
                "accessToken": data["accessToken"],
                "refreshToken": data.get("refreshToken"),
                "expiresAt": expires,
                "refreshExpiresAt": None,
                "loginMethod": login_method,
                "phone": data.get("phone"),
                "savedAt": int(data.get("savedAt") or time.time()),
                "source": f"legacy:{legacy.name}",
            },
        )
        imported.append(uid)
        log.info("已从遗留文件导入 WorkBuddy 凭据（uid=%s…）：%s", uid[:8], legacy)
    return imported


def load_all() -> list[dict]:
    """全部账号凭据（含一次性遗留导入），按保存时间排序。"""
    paths.ensure_dirs()
    try:
        _import_legacy()
    except Exception as exc:  # noqa: BLE001 - 迁移失败不影响正常流程
        log.warning("遗留凭据导入失败：%s", exc)
    out: list[dict] = []
    for path in sorted(paths.workbuddy_cred_dir().glob("*.json")):
        cred = load_credential(path)
        if cred and cred.get("accessToken"):
            cred.setdefault("uid", path.stem)
            out.append(cred)
    out.sort(key=lambda c: c.get("savedAt") or 0)
    return out


def load(uid: str | None = None) -> dict | None:
    """读取指定账号（uid 省略时取第一个账号）。"""
    accounts = load_all()
    if uid:
        return next((c for c in accounts if str(c.get("uid")) == str(uid)), None)
    return accounts[0] if accounts else None


def save(cred: dict) -> dict:
    cred = dict(cred)
    cred.setdefault("savedAt", int(time.time()))
    save_credential(_cred_path(str(cred["uid"])), cred)
    return cred


def short_uid(uid: str | None) -> str:
    return f"{str(uid)[:8]}…" if uid else "-"


# ------------------------------------------------------------------ 续期

def refresh(cred: dict) -> dict:
    """用 refresh token 换发新 AT（失败抛 CredentialError）。"""
    rt = cred.get("refreshToken")
    if not rt:
        raise CredentialError("无 refresh token，需重新登录")
    try:
        with httpx.Client(timeout=30) as client:
            resp = request(
                client,
                "POST",
                REFRESH_URL,
                headers={
                    "X-Refresh-Token": rt,
                    "X-Auth-Refresh-Source": "plugin",
                    "Content-Type": "application/json",
                },
                json={},
                label="WorkBuddy 续期",
            )
    except httpx.HTTPError as exc:
        raise CredentialError(f"续期请求失败（网络）：{type(exc).__name__}: {exc}") from exc
    if resp.status_code in (401, 403):
        raise CredentialError("refresh token 已失效，请重新登录：checkin login workbuddy")
    data = resp.json() if resp.status_code == 200 else {}
    payload = data.get("data") or {}
    new_at = payload.get("accessToken") or payload.get("access_token")
    if data.get("code") not in (0, None) or not new_at:
        raise CredentialError(
            f"续期失败：code={data.get('code')} msg={data.get('msg') or resp.text[:150]}"
        )
    cred = dict(cred)
    cred["accessToken"] = new_at
    cred["refreshToken"] = payload.get("refreshToken") or payload.get("refresh_token") or rt
    exp = jwt_exp(str(new_at))
    expires_in = payload.get("expiresIn") or payload.get("expires_in")
    cred["expiresAt"] = exp or (int(time.time()) + int(expires_in) if expires_in else None)
    cred["refreshedAt"] = int(time.time())
    save(cred)
    log.info("WorkBuddy（%s）access token 已续期", short_uid(cred.get("uid")))
    return cred


def ensure_fresh(cred: dict, force: bool = False) -> dict:
    """确保 AT 可用：剩余不足 6 小时（或 force）时自动续期。"""
    now = time.time()
    exp = cred.get("expiresAt")
    if not force and exp and float(exp) > now + REFRESH_MARGIN:
        return cred
    rt_exp = cred.get("refreshExpiresAt")
    if rt_exp and float(rt_exp) <= now:
        raise CredentialError("refresh token 已过期，请重新登录：checkin login workbuddy")
    return refresh(cred)


def credential_status(cred: dict) -> dict:
    now = time.time()
    exp = cred.get("expiresAt")
    return {
        "uid": cred.get("uid"),
        "loginMethod": cred.get("loginMethod") or "-",
        "phone": mask_phone(cred.get("phone")),
        "source": cred.get("source") or "-",
        "expiresAt": exp,
        "expired": bool(exp) and float(exp) <= now,
        "savedAt": cred.get("savedAt"),
        "refreshedAt": cred.get("refreshedAt"),
    }


# ------------------------------------------------------------------ SMS 直登

def send_sms(phone: str) -> tuple[bool, str]:
    """发送登录验证码；返回 ``(是否成功, 提示)``。"""
    try:
        with httpx.Client(timeout=20) as client:
            resp = request(
                client, "POST", SEND_SMS_URL, headers=_sms_headers(),
                json={"phone": phone}, label="WorkBuddy 发码",
            )
    except httpx.HTTPError as exc:
        return False, f"发码请求失败（网络）：{type(exc).__name__}: {exc}"
    try:
        data = resp.json()
    except ValueError:
        return False, f"发码响应异常 HTTP {resp.status_code}：{resp.text[:150]}"
    if data.get("code") != 0:
        return False, f"发码失败：{data.get('msg') or data}"
    expires = (data.get("data") or {}).get("expires_in", "?")
    return True, f"验证码已发送（{expires} 秒内有效，手机号 {mask_phone(phone)}）"


def login_sms(phone: str, code: str) -> dict:
    """手机号 + 验证码登录并保存凭据。"""
    try:
        with httpx.Client(timeout=30) as client:
            resp = request(
                client, "POST", LOGIN_URL, headers=_sms_headers(),
                json={"login_method": "phone", "phone": phone, "sms_code": code},
                label="WorkBuddy 短信登录",
            )
    except httpx.HTTPError as exc:
        raise CredentialError(f"登录请求失败（网络）：{type(exc).__name__}: {exc}") from exc
    try:
        data = resp.json()
    except ValueError as exc:
        raise CredentialError(f"登录响应非 JSON：{resp.text[:200]}") from exc
    if data.get("code") != 0:
        raise CredentialError(f"登录失败：{data.get('msg') or data}")
    payload = data.get("data") or {}
    at = payload.get("accessToken") or payload.get("access_token")
    rt = payload.get("refreshToken") or payload.get("refresh_token")
    if not at:
        raise CredentialError(f"登录响应缺少 accessToken：{str(data)[:200]}")
    uid = _dig(jwt_payload(str(at)), ["sub", "uid", "user_id"])
    if not uid:
        raise CredentialError("登录响应 token 中未找到 uid（sub）")
    expires_at = jwt_exp(str(at))
    expires_in = payload.get("expiresIn") or payload.get("expires_in")
    if not expires_at and expires_in:
        expires_at = int(time.time()) + int(expires_in)
    cred = {
        "uid": str(uid),
        "accessToken": str(at),
        "refreshToken": rt,
        "expiresAt": expires_at,
        "refreshExpiresAt": None,
        "loginMethod": "phone",
        "phone": phone,
        "loginAt": int(time.time()),
        "source": "sms",
    }
    save(cred)
    log.info("WorkBuddy 短信登录成功（uid=%s…）", short_uid(cred["uid"]))
    return cred


# ------------------------------------------------------------------ OAuth 登录

def _create_state(client: httpx.Client) -> tuple[str, str, str]:
    for host in PLUGIN_HOSTS:
        try:
            resp = client.post(
                f"{host}/v2/plugin/auth/state?platform=CLI", headers=_plugin_headers(), json={}
            )
        except httpx.HTTPError as exc:
            log.warning("state 端点 %s 失败：%s", host, exc)
            continue
        if resp.status_code != 200:
            log.warning("state 端点 %s HTTP %s", host, resp.status_code)
            continue
        data = resp.json()
        state = _dig(data, ["state", "authState", "auth_state"])
        url = _dig(data, ["authUrl", "auth_url", "login_url", "loginUrl", "redirect_url"])
        if state:
            return host, str(state), str(url or "")
    raise CredentialError("获取 OAuth state 失败（三个 host 均不可用），请稍后重试或改用短信登录")


def login_oauth(timeout: float = OAUTH_WAIT_SECONDS) -> dict:
    """插件 OAuth 登录：系统默认浏览器打开登录页，轮询换 token。"""
    from checkin.util import open_in_browser

    paths.ensure_dirs()
    with httpx.Client(timeout=20) as client:
        host, state, auth_url = _create_state(client)
        if not auth_url:
            auth_url = f"{host}/login?platform=CLI&state={state}"
        log.info("OAuth state=%s，登录页：%s", state, auth_url)
        opened = open_in_browser(auth_url)
        copy_to_clipboard(auth_url)
        log.info(
            "已用系统默认浏览器打开 WorkBuddy 登录页（链接已复制到剪贴板；浏览器唤起=%s）。"
            "请在 %d 秒内完成登录（支持微信扫码），登录后本工具会自动继续。",
            opened, int(timeout),
        )

        deadline = time.time() + timeout
        last_note = time.time()
        while time.time() < deadline:
            try:
                resp = client.get(
                    f"{host}/v2/plugin/auth/token?state={state}", headers=_plugin_headers()
                )
                if resp.status_code == 200:
                    data = resp.json()
                    at = _dig(data, ["accessToken", "access_token"])
                    if at:
                        rt = _dig(data, ["refreshToken", "refresh_token"])
                        expires_in = _dig(data, ["expiresIn", "expires_in"])
                        return _save_oauth(host, at, rt, expires_in, client, state)
            except httpx.HTTPError as exc:
                log.warning("轮询 token 出错（继续）：%s", exc)
            if time.time() - last_note > 30:
                log.info("等待登录完成…（剩余 %d 秒）", int(deadline - time.time()))
                last_note = time.time()
            time.sleep(3)
    raise CredentialError(f"OAuth 登录超时（{int(timeout)} 秒内未完成），请重试或改用短信登录")


def _save_oauth(host: str, at, rt, expires_in, client: httpx.Client, state: str) -> dict:
    at = str(at)
    uid = None
    try:
        resp = client.get(
            f"{host}/v2/plugin/login/account?state={state}",
            headers={**_plugin_headers(), "Authorization": f"Bearer {at}"},
        )
        if resp.status_code == 200:
            uid = _dig(resp.json(), ["uid", "userId", "user_id"])
    except httpx.HTTPError as exc:
        log.warning("account 查询失败（回退 JWT sub）：%s", exc)
    uid = str(uid or _dig(jwt_payload(at), ["sub", "uid", "user_id"]) or "")
    if not uid:
        raise CredentialError("登录成功但无法确定 uid（account 与 JWT 都没有）")
    expires_at = jwt_exp(at)
    if not expires_at and expires_in:
        expires_at = int(time.time()) + int(expires_in)
    cred = {
        "uid": uid,
        "accessToken": at,
        "refreshToken": rt,
        "expiresAt": expires_at,
        "refreshExpiresAt": None,
        "loginMethod": "browser",
        "loginAt": int(time.time()),
        "source": f"oauth:{host}",
    }
    save(cred)
    log.info("WorkBuddy OAuth 登录成功（uid=%s…）", short_uid(uid))
    return cred
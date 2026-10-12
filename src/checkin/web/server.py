"""本地 Web 看板服务（计划 §4.1 / Phase 2）：纯标准库 ``http.server``。

安全姿态（本地工具的边界）：
- 只监听 ``127.0.0.1``（不对外网暴露）；
- 校验 ``Host`` 头（防 DNS rebinding），不发送任何 CORS 头；
- 所有写接口要求自定义头 ``X-Checkin: 1``：浏览器跨源请求必然触发预检，
  而本服务不响应预检 → 外部页面无法借用户浏览器调用本机写入接口；
- token 永不进页面：凭据只暴露"是否有效 / 剩余时长 / 来源"。

接口：
- ``GET  /``            单页看板
- ``GET  /api/status``  状态 / 凭据 / 积分 / 配置 / 定时 / 当前执行
- ``GET  /api/log``     日志尾部（``?lines=N``）
- ``POST /api/run``     触发一次执行（后台线程，轮询 ``/api/status`` 的 run 字段）
- ``POST /api/config``  写入单个配置项（沿用 ``config.set`` 校验）
- ``POST /api/schedule`` 安装 / 卸载 launchd 定时任务
"""
from __future__ import annotations

import json
import logging
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from checkin import __version__, config, paths
from checkin.credentials import trae as trae_cred
from checkin.credentials import workbuddy as wb_cred
from checkin.scheduler import launchd
from checkin.util import fmt_remain, fmt_ts, read_json, read_jsonl, today_str

log = logging.getLogger(__name__)

DEFAULT_PORT = 8765
PORT_TRIES = 10
STATIC_FILE = Path(__file__).resolve().parent / "static" / "index.html"
MAX_BODY_BYTES = 64 * 1024
RUN_LOG_LINES = 500
LOG_TAIL_MAX = 5000
SCHEDULE_CACHE_SECONDS = 10


# ------------------------------------------------------------------ 执行状态

class _LineHandler(logging.Handler):
    """把执行期间 ``checkin`` 日志树的记录收集进内存（供页面实时展示）。"""

    def __init__(self, sink: list[str], lock: threading.Lock):
        super().__init__(level=logging.DEBUG)
        self._sink = sink
        self._lock = lock
        self.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s", "%H:%M:%S"))

    def emit(self, record: logging.LogRecord) -> None:
        if record.name.startswith("checkin.web"):  # 页面自身的轮询访问日志不进实时面板
            return
        try:
            line = self.format(record)
        except Exception:  # noqa: BLE001 - 日志格式化失败不能影响主流程
            return
        with self._lock:
            self._sink.append(line)
            if len(self._sink) > RUN_LOG_LINES:
                del self._sink[:-RUN_LOG_LINES]


class _RunState:
    """同一时刻只允许一次执行；结果与实时日志保留到下一次执行。"""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._thread: threading.Thread | None = None
        self._running = False
        self._dry_run = False
        self._started_at: float | None = None
        self._finished_at: float | None = None
        self._report: dict | None = None
        self._lines: list[str] = []

    def start(self, dry_run: bool, force: bool) -> bool:
        with self._lock:
            if self._running:
                return False
            self._running = True
            self._dry_run = dry_run
            self._started_at = time.time()
            self._finished_at = None
            self._report = None
            self._lines = []
            self._thread = threading.Thread(
                target=self._work, args=(dry_run, force), name="checkin-run", daemon=True
            )
            thread = self._thread
        thread.start()
        return True

    def _work(self, dry_run: bool, force: bool) -> None:
        from checkin import run as runner

        handler = _LineHandler(self._lines, self._lock)
        root = logging.getLogger("checkin")
        root.addHandler(handler)
        try:
            report = runner.execute(dry_run=dry_run, force=force)
            self._report = _report_json(report)
        except Exception as exc:  # noqa: BLE001
            log.exception("Web 触发的执行失败")
            self._report = {
                "error": f"{type(exc).__name__}: {exc}",
                "items": [],
                "dryRun": dry_run,
            }
        finally:
            root.removeHandler(handler)
            with self._lock:
                self._running = False
                self._finished_at = time.time()

    def snapshot(self) -> dict:
        with self._lock:
            return {
                "running": self._running,
                "dryRun": self._dry_run,
                "startedAt": self._started_at,
                "finishedAt": self._finished_at,
                "lines": list(self._lines),
                "report": self._report,
            }


RUN_STATE = _RunState()


def _report_json(report) -> dict:
    return {
        "date": report.date,
        "dryRun": report.dry_run,
        "finishedAt": report.finished_at,
        "ok": report.ok,
        "items": [
            {
                "key": item.key,
                "label": item.label,
                "account": item.account,
                "success": item.success,
                "already": item.already,
                "skipped": item.skipped,
                "error": item.error,
                "authExpired": item.auth_expired,
                "message": item.message,
                "creditToday": item.credit_today,
                "balance": item.balance,
                "batchCount": item.batch_count,
                "expiringDays": item.expiring_days,
                "expiring": item.expiring,
                "expiring30d": item.expiring_30d,
            }
            for item in report.items
        ],
    }


# ------------------------------------------------------------------ 状态组装

_schedule_cache: tuple[float, dict] | None = None
_schedule_lock = threading.Lock()


def _schedule_status() -> dict:
    """launchd 状态（带短缓存，避免轮询期间频繁起 launchctl 子进程）。"""
    global _schedule_cache
    now = time.time()
    with _schedule_lock:
        if _schedule_cache and now - _schedule_cache[0] < SCHEDULE_CACHE_SECONDS:
            return _schedule_cache[1]
    info = launchd.status()
    with _schedule_lock:
        _schedule_cache = (time.time(), info)
    return info


def _trae_cred_view() -> dict:
    view = trae_cred.credential_status()
    view["remain"] = fmt_remain(view.get("sessionExpires"))
    view["sessionExpiresText"] = fmt_ts(view.get("sessionExpires"))
    return view


def _wb_cred_view(cred: dict) -> dict:
    view = wb_cred.credential_status(cred)
    view["shortUid"] = wb_cred.short_uid(view.get("uid"))
    view["remain"] = fmt_remain(view.get("expiresAt"))
    view["expiresAtText"] = fmt_ts(view.get("expiresAt"))
    return view


def _product_rows(state: dict) -> list[dict]:
    rows: list[dict] = []
    for key, record in (state.get("products") or {}).items():
        key = str(key)
        if key == "trae":
            label, account = "Trae CN", "-"
        else:
            account = key.split(":")[-1]
            label = f"WorkBuddy（{wb_cred.short_uid(account)}）"
        rows.append(
            {
                "key": key,
                "label": label,
                "product": "trae" if key == "trae" else "workbuddy",
                "account": account,
                **record,
            }
        )
    rows.sort(key=lambda row: (row["product"] != "trae", row["key"]))
    return rows


def _latest_points(history: list[dict]) -> list[dict]:
    latest: dict[tuple, dict] = {}
    for row in history:
        key = (row.get("product"), row.get("account"))
        if key not in latest or (row.get("ts") or 0) > (latest[key].get("ts") or 0):
            latest[key] = row
    return sorted(latest.values(), key=lambda r: str(r.get("product")))


def _log_payload(query: dict) -> dict:
    try:
        lines = int((query.get("lines") or ["300"])[0])
    except ValueError:
        lines = 300
    lines = max(1, min(lines, LOG_TAIL_MAX))
    return {"lines": _log_tail(lines), "path": str(paths.log_path())}


def _log_tail(lines: int) -> list[str]:
    try:
        text = paths.log_path().read_text(encoding="utf-8", errors="replace")
    except OSError:
        return []
    return text.splitlines()[-lines:]


def _status_payload() -> dict:
    state = read_json(paths.state_path(), {}) or {}
    cfg = config.masked_view()
    warnings = config.last_warnings()  # 紧随 masked_view（其内部会 load）之后取，避免被后续 load 覆盖
    history = read_jsonl(paths.points_path())
    ready, reason = config.dingtalk_ready()
    return {
        "version": __version__,
        "date": today_str(),
        "now": time.time(),
        "stateDir": str(paths.state_dir()),
        "state": state,
        "products": _product_rows(state),
        "credentials": {
            "trae": _trae_cred_view(),
            "workbuddy": [_wb_cred_view(cred) for cred in wb_cred.load_all()],
        },
        "points": _latest_points(history),
        "history": history,
        "config": cfg,
        "configWarnings": warnings,
        "secretsSet": {
            "notify.dingtalk.webhook": bool(config.get("notify.dingtalk.webhook")),
            "notify.dingtalk.secret": bool(config.get("notify.dingtalk.secret")),
        },
        "dingtalk": {"ready": ready, "reason": reason},
        "schedule": _schedule_status(),
        "run": RUN_STATE.snapshot(),
    }


# ------------------------------------------------------------------ HTTP

class _Handler(BaseHTTPRequestHandler):
    server_version = f"checkin-ui/{__version__}"
    protocol_version = "HTTP/1.1"

    # -------------------------------------------------- 路由

    def do_GET(self) -> None:  # noqa: N802 - 标准库命名
        if not self._host_ok():
            return self._json(403, {"error": "Host 校验失败（本服务只服务 127.0.0.1）"})
        parsed = urlparse(self.path)
        if parsed.path in ("/", "/index.html"):
            return self._serve_index()
        if parsed.path == "/api/status":
            return self._json(200, _status_payload())
        if parsed.path == "/api/log":
            return self._json(200, _log_payload(parse_qs(parsed.query)))
        if parsed.path == "/favicon.ico":
            return self._empty(204)
        return self._json(404, {"error": "未知路径"})

    def do_POST(self) -> None:  # noqa: N802
        if not self._host_ok():
            return self._json(403, {"error": "Host 校验失败（本服务只服务 127.0.0.1）"})
        if self.headers.get("X-Checkin") != "1":
            return self._json(403, {"error": "缺少 X-Checkin 请求头（跨源写入防护）"})
        payload = self._read_json()
        if payload is None:
            return self._json(400, {"error": "请求体必须是 JSON 对象"})
        parsed = urlparse(self.path)
        if parsed.path == "/api/run":
            return self._handle_run(payload)
        if parsed.path == "/api/config":
            return self._handle_config(payload)
        if parsed.path == "/api/schedule":
            return self._handle_schedule(payload)
        return self._json(404, {"error": "未知路径"})

    # -------------------------------------------------- 处理

    def _handle_run(self, payload: dict) -> None:
        started = RUN_STATE.start(bool(payload.get("dry_run")), bool(payload.get("force")))
        self._json(
            200 if started else 409,
            {"started": started, "run": RUN_STATE.snapshot()},
        )

    def _handle_config(self, payload: dict) -> None:
        key = str(payload.get("key") or "")
        if not key or "value" not in payload:
            return self._json(400, {"ok": False, "message": "需要 key 与 value"})
        ok, message = config.set(key, payload.get("value"))
        self._json(200 if ok else 400, {"ok": ok, "message": message})

    def _handle_schedule(self, payload: dict) -> None:
        action = str(payload.get("action") or "")
        if action == "install":
            ok, message = launchd.install()
        elif action == "uninstall":
            ok, message = launchd.uninstall()
        else:
            return self._json(400, {"ok": False, "message": "action 应为 install / uninstall"})
        global _schedule_cache
        with _schedule_lock:
            _schedule_cache = None
        self._json(
            200 if ok else 500,
            {"ok": ok, "message": message, "schedule": _schedule_status()},
        )

    # -------------------------------------------------- 基础设施

    def _host_ok(self) -> bool:
        host = (self.headers.get("Host") or "").strip().lower()
        if host.startswith("["):
            name = host.split("]")[0].lstrip("[")
        else:
            name = host.split(":")[0]
        return name in ("127.0.0.1", "localhost", "::1")

    def _read_json(self) -> dict | None:
        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            return None
        if length > MAX_BODY_BYTES:
            return None
        if length <= 0:
            return {}
        try:
            data = json.loads(self.rfile.read(length).decode("utf-8"))
        except (ValueError, UnicodeDecodeError):
            return None
        return data if isinstance(data, dict) else None

    def _serve_index(self) -> None:
        try:
            body = STATIC_FILE.read_bytes()
        except OSError:
            return self._json(500, {"error": f"静态文件缺失：{STATIC_FILE}"})
        self._send(200, "text/html; charset=utf-8", body)

    def _json(self, code: int, payload: dict) -> None:
        body = json.dumps(payload, ensure_ascii=False, default=str).encode("utf-8")
        self._send(code, "application/json; charset=utf-8", body)

    def _empty(self, code: int) -> None:
        self._send(code, "text/plain; charset=utf-8", b"")

    def _send(self, code: int, content_type: str, body: bytes) -> None:
        self.send_response(code)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Referrer-Policy", "no-referrer")
        self.end_headers()
        if body:
            try:
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                pass

    def log_message(self, fmt: str, *args) -> None:  # noqa: A003
        log.debug("web %s %s", self.address_string(), fmt % args)


# ------------------------------------------------------------------ 启动

def _bind(port: int) -> tuple[ThreadingHTTPServer, int]:
    """绑定端口（被占用时顺延最多 ``PORT_TRIES`` 个），返回 (服务, 实际端口)。"""
    candidates = [port] if port == 0 else list(range(port, port + PORT_TRIES))
    last_error: Exception | None = None
    for candidate in candidates:
        try:
            httpd = ThreadingHTTPServer(("127.0.0.1", candidate), _Handler)
        except OSError as exc:
            last_error = exc
            continue
        return httpd, int(httpd.server_address[1])
    raise OSError(f"端口 {port}–{port + PORT_TRIES - 1} 均不可用：{last_error}")


def serve(port: int | None = None, open_browser: bool = True) -> int:
    """启动看板服务（阻塞直到 Ctrl+C）。"""
    paths.ensure_dirs()
    httpd, chosen = _bind(int(port or DEFAULT_PORT))
    url = f"http://127.0.0.1:{chosen}/"
    print(f"本地看板已启动：{url}（仅本机可访问，Ctrl+C 停止）")
    if open_browser:
        from checkin.util import open_in_browser

        if not open_in_browser(url):
            print(f"未能自动打开浏览器，请手动访问：{url}")
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n看板已停止")
    finally:
        httpd.server_close()
    return 0
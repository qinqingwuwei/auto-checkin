# checkin · Trae CN + WorkBuddy 签到与积分助手

个人自用的本机工具（macOS 优先）：每天定时完成 **Trae CN** 与 **WorkBuddy** 的每日签到，
拉取**逐批积分台账**，并在积分即将过期时预警。定位是"每天准时跑 + 坏了你知道"，
不承诺"永不失效"。

产物形态：Python CLI（`checkin`）+ 本地 Web 看板（`checkin ui`）。Windows 适配属 Phase 3。

## 快速开始

```bash
uv sync                     # 安装依赖（httpx + playwright，均在项目 venv 内）
uv run checkin doctor       # 自检：配置 / 凭据 / 网络 / 定时 / 浏览器
uv run checkin login trae         # 首次登录 Trae（复用系统已装 Chrome，零下载；微信扫码/账号登录）
uv run checkin login workbuddy --method browser   # 浏览器扫码登录（系统默认浏览器）
# 或纯 API 短信登录（零浏览器）：
uv run checkin login workbuddy --method sms --phone 138xxxxxxxx --code 123456
uv run checkin run --dry-run     # 干跑：完整走一遍但不领取、不落盘、不发通知
uv run checkin run               # 真跑一次
uv run checkin ui                # 本地 Web 看板（浏览器查看状态 / 改配置 / 手动执行）
uv run checkin schedule install  # 安装每日定时（默认 08:00）
```

## 命令

| 命令 | 说明 |
|---|---|
| `checkin run [--dry-run] [--force] [--only trae\|workbuddy]` | 执行一次：签到 → 台账 → 到期汇总 → 通知 → 落盘 |
| `checkin status [--json]` | 本地状态：签到记录 / 凭据有效期 / 最近积分快照 |
| `checkin doctor [--offline]` | 自检（含只读网络连通检查） |
| `checkin login trae [--channel chrome\|msedge\|chromium]` | 打开浏览器登录 Trae，换取并保存会话 |
| `checkin login workbuddy --method browser\|sms [--phone] [--code]` | WorkBuddy 双登录路径 |
| `checkin config show\|get\|set\|path` | 配置读写（`show` 中密钥自动打码） |
| `checkin ui [--port 8765] [--no-open]` | 本地 Web 看板：状态卡片 / 积分曲线 / 配置表单 / 手动执行 / 日志 |
| `checkin schedule install\|uninstall\|status` | launchd 定时任务管理 |

## 本地 Web 看板（`checkin ui`）

```bash
uv run checkin ui                # 默认 http://127.0.0.1:8765/，自动打开浏览器
uv run checkin ui --no-open      # 不自动开浏览器（端口被占用时自动顺延）
```

- 页面内容：产品状态卡片（签到状态 / 余额 / 7·30 天到期 / 凭据剩余）、**积分曲线**
  （每次成功执行追加一个点，逐批快照口径）、**配置表单**（时间 / 阈值 / 钉钉 / 开关，逐项校验落盘）、
  **手动执行**（含干跑与强制选项，实时滚动日志）、**定时任务**安装/卸载、最近日志尾部；
- 安全边界：**只监听 `127.0.0.1`**，校验 `Host` 头（防 DNS rebinding），
  写接口要求自定义头 `X-Checkin: 1` 且不发送任何 CORS 头 → 外部网页无法借浏览器调用本机接口；
  **token 只显示状态不显示明文**；
- 服务为纯标准库 `http.server` 实现（无 FastAPI/uvicorn 依赖），Ctrl+C 停止。

## 配置（`~/.checkin/config.json`，0600）

| 点路径 | 默认 | 说明 |
|---|---|---|
| `schedule.time` | `08:00` | 每日执行时间（改完记得 `checkin schedule install` 重新加载） |
| `expiry_warning_days` | `7` | 到期预警阈值（天），`0` = 关闭预警 |
| `notify.dingtalk.enabled` | `true` | 钉钉通道开关 |
| `notify.dingtalk.webhook` | 空 | 钉钉自定义机器人 webhook（需开启「加签」） |
| `notify.dingtalk.secret` | 空 | 钉钉加签 secret |
| `notify.local.enabled` | `true` | macOS 系统通知开关 |
| `notify.daily_summary` | `false` | 每日成功简报（默认仅异常与预警推送） |
| `products.trae.enabled` / `products.workbuddy.enabled` | `true` | 产品开关 |

```bash
uv run checkin config set notify.dingtalk.webhook 'https://oapi.dingtalk.com/robot/send?access_token=...'
uv run checkin config set notify.dingtalk.secret 'SEC...'
uv run checkin config set schedule.time 07:30 && uv run checkin schedule install
```

未配置钉钉时 `doctor` 会提示 WARN，通知降级为**本地通知 + 日志**，不影响签到主流程。

## 数据与安全

- 所有数据统一在 `~/.checkin/`（可用 `CHECKIN_HOME` 覆盖），**绝不写回任何客户端目录**：
  - `config.json` / `state.json`（幂等用）/ `points.jsonl`（每日快照）/ `logs/`（轮转日志，始终写）
  - `credentials/`：Trae 会话、WorkBuddy 多账号凭据（每账号一个 `<uid>.json`），全部 0600
- token 不打印明文、不进日志、不进通知；`config show` 中 webhook/secret 打码
- 幂等：先查签到状态再决定是否领取，"今日已签到"记为成功，**绝不重复触发 claim**；
  定时点错过（睡眠/关机）后 launchd 唤醒补跑也不会重复签到

## 风险声明

- 两侧接口均为社区逆向社会接口，**产品改版即可能失效**；风控码（如 Trae `9074/9095/12153` 一类）随时可能出现；
- WorkBuddy 官方规则将"利用脚本、接口调用等方式刷取活跃或奖励"列为违规，**脚本签到本身在规则字面上即构成违规**，
  后果可能包括取消参与资格、暂停或追回已发放奖励；多账号是第二个独立违规点。风险由账号持有者自行承担；
- 本工具**不做**批量签到、代签、倒卖、自动兑换；账号数量保持个位数，不多账号共用设备标识；
- 凭据失效时工具会明确告警并指引重新登录，不会静默失败。

## 实现要点（对应计划 §5.4 / §7.4）

- **Trae CN**：网页会话 cookie `X-Cloudide-Session` → `GetUserToken` 换 8 小时 JWT → `Cloud-IDE-JWT` 调
  `checkin_credits/status|claim`、`user_current_entitlement_list`（逐批 `credits_limit - usage` 与 `expire_time`）等接口；
- **WorkBuddy**：插件 OAuth（系统浏览器 + state 轮询）或 SMS 直登 → `Bearer` + `X-User-Id` 调
  `checkin-activity-status|daily-checkin`、`get-user-resource`（逐包 `CapacityRemain` 与 `DeductionEndTime`）；
  refresh token 实测约 60 天，剩余不足 6 小时自动续期；
- **浏览器零下载**：登录复用系统已装 Chrome/Edge（Playwright `channel`），仅当两者都不存在时才提示一次性安装自带 Chromium；
- 到期计算为**接口直读的批次精确口径**，不使用估算模型。

## 致谢

接口序列与错误码排障参考了以下开源项目（本项目为独立实现，未 fork）：

- [88lin/workbuddy-auto-signin](https://github.com/88lin/workbuddy-auto-signin)
- [L0NE-6/WorkBuddy-Daily](https://github.com/L0NE-6/WorkBuddy-Daily)（短信直登字段逐字对齐）
- [Cat-zaizai/ZaiZaiCat-Checkin](https://github.com/Cat-zaizai/ZaiZaiCat-Checkin)
- [ChenLun-dev/Trae-Checkin](https://github.com/ChenLun-dev/Trae-Checkin)
- [smart-open/TraeWorkAssistant](https://github.com/smart-open/TraeWorkAssistant)（插件 OAuth）
- [Sitoi/dailycheckin](https://github.com/Sitoi/dailycheckin)

## 开发

```bash
uv run pytest      # 单测：配置 / 到期计算 / 钉钉加签 / 路径
```

完整设计与实测结论见 `.trae/documents/trae-workbuddy-checkin-plan.md`（含 §5.4 Trae 端点、
§7.4 WorkBuddy 端点、§5.6 批次台账口径）。
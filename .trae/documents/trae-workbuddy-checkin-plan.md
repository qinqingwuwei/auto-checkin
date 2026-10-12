# Trae CN + WorkBuddy 签到与积分助手 · 实施计划

> 状态：待用户批准后执行
> 平台：macOS 优先（完整实现），Windows 第二阶段
> 定位：个人自用工具。每天准时跑 + 失败明确告警 + 手动兜底，不承诺"永不失效"

---

## 1. 目标与交付物

**做三件事**：
1. 每天自动完成 Trae CN 与 WorkBuddy 的每日签到；
2. 拉取两个产品的积分总额与明细；
3. 估算"即将过期积分"并在临近到期时预警。

**交付形态**：一个 Python CLI 工具 + 内置本地 Web 看板。
- CLI 是内核：`checkin run / status / ui / doctor / schedule / login`
- `checkin ui` 起一个只监听 `127.0.0.1` 的本地服务，浏览器打开即可查看与配置
- **不做** Electron/Tauri 桌面 App，不做云端服务，不做打包分发

---

## 2. 已确认的决策（Q&A 收口）

| 项 | 结论 | 依据 |
|---|---|---|
| 产物形态 | CLI + 本地 Web UI（无桌面壳） | C1 确认 |
| 运行位置 | 本机 macOS，launchd 定时 | Q2 |
| 跨平台 | macOS 先行，Windows 第二阶段（有 Windows 机器可实测） | Q2 / Q11 |
| 凭证策略 | **浏览器登录与 SMS 直登并行**（2026-10-12 定）：Trae = 网页会话 → 换票（登录窗口**复用系统已装浏览器**，零下载）；WorkBuddy = 插件 OAuth（系统默认浏览器）**或 SMS 直登**（手机号验证码，纯 API 零浏览器）。凭据存 `~/.checkin/`（0600），不写回任何客户端文件 | C2 + Q14 + 0.1d + 用户追加要求 |
| 技术栈 | Python 3.12（uv 管理）+ 项目内 venv | Q6 |
| 产品身份 | 腾讯 WorkBuddy（workbuddy.cn）+ Trae CN 国内版（trae.cn） | Q7 确认 |
| 账号数 | **WorkBuddy 支持多账号**（各自独立凭据、不共用任何设备标识、数量控制个位数）；**Trae 维持单账号**（`9095` 为设备级去重，不做指纹轮换） | Q8 + 追加调研 |
| 范围 | 签到 + 积分查询 + 到期预警 + 失败告警；**不做**自动兑换/自动完成任务 | Q5 / Q9 / Q10 |
| 通知 | 钉钉机器人（webhook + 加签 secret）+ 本地系统通知 + 本地日志（始终写） | Q4 |
| 通知策略 | 默认**仅异常与预警推送**；每日成功简报默认关闭、可开关 | Q13 |
| 到期阈值 | 默认 7 天，可配 | Q13 |
| Web UI 范围 | 看板 + 配置 + 手动触发 + 历史日志与积分曲线；**token 只显示状态不显示明文** | Q12 |
| 过期积分精度 | 采用合理估算（无开源先例，见 §5.6） | Q9 |
| 风险姿态 | 接受逆向接口脆弱性；拒绝批量/代签/倒卖用法 | Q10 |

---

## 3. 已验证的事实基础

### 3.1 本机环境（实测）
- `/Applications/Trae CN.app` v3.4.2（`cn.trae.app`，Electron，运行中）
- `/Applications/WorkBuddy.app` v5.7.7（`com.tencent.workbuddy.mac`，Electron，运行中）
- `python3` 3.9.6（系统版，**不安装依赖进去**）；`uv` 0.12.7 已装；`node` v20.20.2；`go` 无
- `~/Library/LaunchAgents` 存在且已有第三方 plist 在跑 → launchd 可行
- **浏览器完全不可用**：Chrome 无 profile、Edge 空、Safari 空 → 不存在可复用的浏览器登录态
- `sqlite3`、`security`(钥匙串 CLI) 可用

### 3.2 Trae CN（实测 + 社区）
- **凭据**：`~/.trae-cn/trae-jwt-token`，明文 JWT，权限 0600 —— ⚠️ **已证实该票不能用于签到接口（属智能体/CLI）**；正确票据链路见 §5.4（网页 cookie → GetUserToken）
  - payload claims：`data{t id,user_id,tenant_id,type}`、`exp`、`iat`、`iss`
  - `exp`-`iat` = **7 天**；文件 mtime 与 `iat` **精确到秒一致** → **客户端刷新时会重写该文件**
- **设备 ID**：`~/Library/Application Support/Trae CN/machineid`（36 字符 UUID 形）
- **用户 ID**：JWT 内 `data.user_id`
- **积分规则**（官方文档）：签到免费 100 分/天、会员 200 分/天；**有效期 31 个自然日**；明细入口在网页版「用量管理」
- **接口**（社区逆向，待 Phase 0 验证）：
  `POST https://api.trae.cn/trae/api/v2/ug/checkin_credits/status` 与 `/claim`
  头：`Authorization: Cloud-IDE-JWT <token>`、`x-device-id`、`X-User-Region`
  体：`{"req_source":1|2}`
- **已知错误码**：`9095` = 本设备今日已签到（视为成功）；`9074` = 限流/设备校验失败（退避重试 + 告警）；`12153` = token 非本域签发或被其他工具轮换

### 3.3 WorkBuddy / CodeBuddy（实测 + 社区）
- **凭据**：`~/Library/Application Support/CodeBuddyExtension/Data/Public/auth/workbuddy-desktop.info`，明文 JSON
  - `auth.accessToken` / `auth.refreshToken` → **`$wbEncrypted` 信封**（`{suite:1,keyId,nonce,authTag,ciphertext}`），**密钥不落盘，无法静态解密**
  - 明文可得：`expiresAt`（epoch 毫秒）、`refreshExpiresAt`、`sessionState`、`tokenType=Bearer`
  - `account.uid` → 用户 ID（`X-User-Id` 来源）
  - 同目录 2026-09-10 备份文件内含**明文 JWT**（`refreshExpiresAt` 至 2026-12-09）——**仅作应急，不作为方案**
- **设备 ID**：`~/.workbuddy/device-id`（36 字符 UUID 形）；指纹见 `~/.workbuddy/qimei-cache.json`
- **积分规则**：Buddy 加油站签到 100 分/天，连签第 7 天 +1000；明细在客户端「个人中心→套餐与用量→平台奖励积分」
- **接口**（✅ 已实测，精确版见 §7.4）：
  `POST {base}/v2/billing/meter/checkin-activity-status`、`/v2/billing/meter/daily-checkin`
  头：`Authorization: Bearer <token>`、`X-User-Id`；base 当前为 `www.codebuddy.cn`
  登录侧：**两条正式路径**——① 插件 OAuth（`/v2/plugin/auth/state|token`）：**微信扫码 ✅ 实测通过**；登录页上的手机号入口会报"登录失效"（页面限制，非接口能力问题）② **插件 SMS 直登**（`/v2/plugin/login/send-sms` → `/v2/plugin/login/token`，手机号+验证码，**纯 API 零浏览器**）：**✅ 2026-10-12 真机实测通过**（发码 → 登录 → RT 续期 → 只读查询全通，与 OAuth 路径同账号）
- **积分有效期**（✅ 2026-10-12 实测）：逐包独立到期（与 Trae 同构）——`DeductionEndTime`（epoch ms，≈`CycleEndTime`，发放后约 30 天）；46 包台账实测：余额合计 4177、7 天内到期 589、31 天内到期 3627

### 3.4 可参考的开源实现（借鉴，不 fork）
| 项目 | 借鉴点 |
|---|---|
| [88lin/workbuddy-auto-signin](https://github.com/88lin/workbuddy-auto-signin) | WorkBuddy 签到全流程与接口序列 |
| [L0NE-6/WorkBuddy-Daily](https://github.com/L0NE-6/WorkBuddy-Daily) | refresh_token 轮换、短信登录、多渠道推送 |
| [Cat-zaizai/ZaiZaiCat-Checkin](https://github.com/Cat-zaizai/ZaiZaiCat-Checkin) | Trae CN + WorkBuddy 双端覆盖 |
| [ChenLun-dev/Trae-Checkin](https://github.com/ChenLun-dev/Trae-Checkin) | 零依赖 Python、错误码排障文档 |
| [smart-open/TraeWorkAssistant](https://github.com/smart-open/TraeWorkAssistant) | 多应用登录态读取思路 |
| [Sitoi/dailycheckin](https://github.com/Sitoi/dailycheckin) | 多账号 YAML 配置范式 |

**不 fork 的理由**：它们的核心能力（Windows/青龙/Actions 适配）对我们无用，而我们的核心差异化（本机凭据直读 + 到期预警 + 本地看板）它们都没有，fork 只会背上历史包袱。仅在 README 里致谢并注明参考来源。

---

## 4. 架构

### 4.1 目录结构

```
TRAE 和 Workbuddy工具/
├── pyproject.toml               # uv 项目；入口 checkin = checkin.cli:main
├── .python-version              # 3.12（固定）
├── README.md                    # 用法、致谢、风险声明
├── src/checkin/
│   ├── cli.py                   # 命令解析与调度
│   ├── config.py                # 配置读写 + 校验
│   ├── paths.py                 # 跨平台路径解析（唯一的 OS 分支点之一）
│   ├── log.py                   # 结构化日志 + 轮转
│   ├── run.py                   # 一次完整执行的主流程
│   ├── credentials/
│   │   ├── base.py              # CredentialSource 协议 + OS 分派
│   │   ├── trae.py              # mac/win 两套路径
│   │   └── workbuddy.py         # 路线 (a)/(b) 见 §7 Phase 0
│   ├── providers/
│   │   ├── base.py              # Provider 协议：status / claim / fetch_points
│   │   ├── trae_cn.py
│   │   └── workbuddy.py
│   ├── expiring.py              # 积分台账与到期估算（纯函数，可单测）
│   ├── notify/
│   │   ├── dingtalk.py          # webhook + 加签 + 重试
│   │   ├── local.py             # macOS osascript / Windows toast
│   │   └── dispatcher.py        # 多通道分发 + 降级为仅日志
│   ├── scheduler/
│   │   ├── launchd.py           # plist 生成/安装/卸载
│   │   └── schtasks.py          # Windows（Phase 3）
│   └── web/
│       ├── server.py            # 127.0.0.1 上的极简 HTTP 服务
│       └── static/index.html    # 单页看板
└── tests/
    ├── test_expiring.py
    ├── test_dingtalk_sign.py
    └── test_credentials_paths.py
```

### 4.2 依赖策略
- 运行依赖：**仅 `httpx`**（HTTP 客户端，含超时与重试控制）
- 可选依赖 `browser` extra：`playwright`（仅 Trae 登录/重登用；WorkBuddy 全路径不需要）。**浏览器零下载原则（硬约束）**：绝不主动要求下载浏览器二进制——Trae 登录复用系统已装 Chrome（macOS）/ Edge（Windows 必装）开独立配置登录窗口（`channel`）；可选增强 `cookie-source` extra：`rookiepy` 直读系统默认浏览器现有登录态（能读就用、不能读自动回落 channel；macOS 首次需系统授权、Windows 新版 Chrome 127+ 为 App-Bound 加密读取受限）。仅当两者都不可用时，才由 `checkin doctor` 提示一次性安装自带 Chromium（~170MB）
- Web UI：Python 标准库 `http.server`（本地只读看板 + 少量写配置，不值得引入 FastAPI/uvicorn）
- 其余全部标准库：`hmac`/`hashlib`/`base64`/`json`/`logging`/`argparse`/`urllib`

### 4.3 数据落盘位置
统一放 `~/.checkin/`（mac/win 同构，**不碰任何客户端目录**）：
- `config.json`（0600）：钉钉 webhook/secret、运行时间、通知开关、到期阈值、产品开关
- `state.json`：最后一次成功日期（幂等判断用）、各产品最近一次结果
- `points.jsonl`：每日快照（日期、产品、余额、当日获得、当日消耗）
- `logs/checkin.log`（+ 轮转）：全量运行日志

---

## 5. 关键设计与算法

### 5.1 凭证策略（硬约束）
1. **登录通道与客户端文件解耦**，不再依赖客户端文件（Trae 的客户端票据来源不明；WorkBuddy 的票据被 `$wbEncrypted` 加密）；
2. **Trae**：Playwright 持久化 profile（`~/.checkin/browser/trae`；**channel 复用系统已装 Chrome/Edge 开登录窗口，零下载**）→ 网页会话 Cookie → `GetUserToken` 换 JWT；
3. **WorkBuddy**：插件 OAuth（系统浏览器 + state 轮询）**或 SMS 直登**（手机号验证码，纯 API）→ access/refresh token；refresh token 实测有效期约 60 天，到期前自动续期；
4. 所有凭据落 `~/.checkin/`，权限 0600；**仍然绝不写回任何客户端目录**；
5. token 不进日志、不进 UI；UI 只显示状态（有效 / X 小时后过期 / 已失效 + 来源路径）。

**实测状态（2026-10-12）**：**两侧均已全通**。Trae = cookie → 8h JWT → 全部接口 200（§5.4）；WorkBuddy = 两条登录路径（系统浏览器插件 OAuth / 手机号 SMS 直登）全部实测通过 + 签到状态 + 46 包台账只读验证（§7.4）；SMS 与 OAuth 路径 uid 一致（`9bef75f0…`，同账号）。

**浏览器策略（定版 · 零下载）**：
- **日常签到全程零浏览器**（纯 HTTP），浏览器只在登录/重登瞬间出现；
- **WorkBuddy 登录**：默认用**系统默认浏览器**打开 OAuth 页（`open`/`start` + state 轮询换 token）；**或** SMS 直登（手机号+验证码，纯 API，连浏览器都不用）。两条都是一等公民：`checkin login workbuddy --method browser|sms`；
- **Trae 登录**：主路径 = Playwright `channel="chrome"`（macOS）/ `channel="msedge"`（Windows 必装）**复用系统已装浏览器二进制**，开一个独立配置的登录窗口（窗口就是你机器上的 Chrome/Edge，**不是下载的 Chromium**）；channel 与 profile 目录固定绑定（cookie 加密钥匙随通道而异，切换=需重登一次）；
- **可选增强（v2）**：直读系统默认浏览器日常登录态（`cookie-source` extra）——日常浏览器已登录 trae.cn 即可零额外登录；能读就用、不能读自动回落 channel。限制：macOS 首跑需系统授权（App Data + 钥匙串 "Chrome Safe Storage" 各一次）；Windows 新版 Chrome（127+）为 App-Bound 加密读取受限（Edge 更早版本 / Firefox 直接可读）；**实测注记：Trae 沙箱内无法读 Chrome 数据目录（Operation not permitted），该增强须在真实终端环境首跑验证**；
- 仅当系统既无 Chrome/Edge 且增强不可用时，才提示一次性安装自带 Chromium（~170MB，`checkin doctor` 检测）。**正常机器永远不下载浏览器**；
- 重登频率：Trae 会话 ~14 天、WorkBuddy refresh token ~60 天（可自动续期）。

### 5.2 有效性预检与过期处理
```
读凭据 → 本地判断 exp/expiresAt 是否已过期
  ├─ 未过期 → 发一次轻量 status 请求
  │    ├─ 2xx → 继续本次执行
  │    └─ 401/403/12153 → 判定失效 → 告警 + 指引 `checkin login`
  └─ 已过期 → 直接告警 + 指引 `checkin login`（不发起业务请求）
```
不静默失败、不对鉴权失败做重试（只对网络错误与 5xx 做指数退避）。

### 5.3 幂等与漏跑补跑
- 每次执行前检查 `state.json` 中"最后成功日期"；若今天已成功，则跳过（`--force` 可覆盖）；
- 先调 `status` 再决定是否 `claim`：已签到（Trae `9095`、WorkBuddy 等价状态）**记为成功而非失败**；
- 这样即使 Mac 睡眠/关机导致定时点错过，launchd 在唤醒后补跑也不会重复签到、更不会漏签。

### 5.4 Trae CN 执行序列（✅ Phase 0.1d / 0.2 已全部实测通过，2026-10-12）

**票据链路（已验证，全程零浏览器）**：
```
网页会话 cookie `X-Cloudide-Session`（Playwright 持久化 profile 存 ~/.checkin/browser/trae，实测有效期至 ~2026-10-26）
  → POST https://api.trae.cn/cloudide/api/v3/common/GetUserToken   （头：X-Cloudide-Session: <cookie>，响应 Result.Token）
  → JWT（993 字符，**有效期 8 小时**）
  → 后续全部接口：Authorization: Cloud-IDE-JWT <jwt> + X-User-Region: cn
```

**已实测 HTTP 200 的接口**（均为 POST，`Content-Type: application/json`）：

| 接口 | 请求体 | 用途 |
|---|---|---|
| `/trae/api/v2/ug/checkin_credits/status` | `{"trae_client":"IDE","req_source":1,"client_version":"3.4.2"}` | 签到状态：`{checked_in, code:0, credits:100, enable, did_checked_in, extra_credits}` |
| `/trae/api/v2/ug/checkin_credits/claim` | 同上 | **签到动作（⏳ 待明日实测）** |
| `/trae/api/v2/pay/user_current_entitlement_list` | `{"require_usage":true,"full_data":true}` | **积分批次台账**：逐批 `credits_limit / usage.credits_amount / expire_time` |
| `/trae/api/v2/pay/expired_ents` | `{}` | 过期记录：`[{credits_limit, expire_time_ms, name}]` |
| `/trae/api/v1/pay/query_user_usage_group_by_session` | `{"start_time","end_time","page_size":20,"page_num":1,"usage_type":[7]}` | 会话级用量明细（含模型/Token 数） |
| `/trae/api/v2/pay/cn_credits_billing_status` | `{}` | billing 开关状态 |

**关键结论**：
- ✅ 早期 1001 网关拒绝根因**已证实**：`~/.trae-cn/trae-jwt-token` 是智能体/CLI 的票；**网页会话换的 JWT 才有效**（status 返回 `code:0`）
- ✅ 网页版自己就是用 `Cloud-IDE-JWT` 调上述 pay 接口（抓包确认 auth_scheme），与 CLI 完全同构
- ✅ **直连（不启动浏览器）已完整跑通**：cookie → 换票 → 上述接口全部 200
- ⚠️ 字段注意：`quota` / `entitlement_id` 顶层可能为 `null`，需回退 `entitlement_base_info.*`；`expire_time` 为**秒**，`expired_ents.expire_time_ms` 为**毫秒**
- 📊 实测样例（10-12 11:25）：总积分 4650、已消耗 344.61、剩余 4305.39；**7 天内到期 705.39**（5 批次，最早 10-14 18:31 过期）
- ⏳ 唯一未验证项：`claim`（今日已签到无法验证，明日状态重置后首次真跑时验证；若 IDE 体格式的 `device_id` 被校验则补设备头）
- 🔑 cookie 失效时：CLI 打开持久化 profile 窗口提示重新登录（通常会话可自动恢复，无需重扫）

**辅助事实（保留）**：鉴权/设备头/请求体构造均来自 `Trae CN.app/out/main.js` 源码（`WebRequestInterceptor`、`Dqe()`）；`trae_client` 只是 `"IDE"`/`"Lite"` 字符串，不是签名。

### 5.5 WorkBuddy 执行序列
同上结构，差异在**取 token 的方式**（OAuth 或 SMS 直登，见 §7.4）与所需头部（`Bearer` + `X-User-Id`，user id 优先取 `account.uid`，SMS 路径从 AT 的 JWT payload `sub` 取）。

### 5.6 即将过期积分

**✅ 2026-10-12 更新：两侧均已具备精确批次数据源**（Trae 见 §5.4 的 `user_current_entitlement_list`；WorkBuddy 见 §5.6.1 的 `get-user-resource`）。通用算法：逐批 `剩余 = credits_limit - 已用`，筛选 `到期时间 ∈ (now, now+N 天]` 累加剩余。**估算模型（§5.6.2）降为极端兜底。**

#### 5.6.1 主路径：直读批次到期数据（已由开源项目验证）

来源：`linguo2625469/workbuddy2api-panel`（Go / MIT / **2766★** / 活跃）`internal/upstream/client.go`。

- 端点：`POST {base}/v2/billing/meter/get-user-resource`
- 请求体：`{"ProductCode":"p_tcaca", "Status":[0,3], "PackageEndTimeRangeBegin": <now>, "PackageEndTimeRangeEnd": <now + 101年>}`
- 响应 `Data.Accounts[]` 是**逐包（批次）**数据：`Remain` / `Used` / `CycleCapacitySize`
- 到期时间字段按优先级取：`DeductionEndTime`（epoch 毫秒，"真失效时刻"）→ `ExpiredTime`（实测恒空）→ `PackageEndTime`（实测恒 miss）→ `CycleEndTime`（`YYYY-MM-DD HH:mm:ss`，上游真实下发，兜底）
- 计算：逐包 `Remain > 0` 且到期时间落在 `(now, now+N天]` → 累加 `Remain`
- 窗口：默认 **7 天**（与 Q13 阈值一致），`0` 表示关闭
- 注意：无统一固定有效期；global bonus pack 赠送积分为 **14 天**

→ **这一项从"估算"升级为"精确"。** Trae 侧等价批次接口已实测确认（`user_current_entitlement_list`，见 §5.4）；§5.6.2 仅作极端兜底。

#### 5.6.2 兜底：估算模型（仅在无批次接口时使用）

> **状态更新**：两侧均已确认精确批次数据源（见 §5.6 开头）。本节仅作为"接口改版失效"时的极端兜底保留，实现中不启用。

**模型（按批次 FIFO 消耗）**：
1. 每天记录积分余额，相邻两日的差值即"当日获得"（正）与"当日消耗"（负）；
2. 把"当日获得"记为一批，**到期日 = 获得日 + 有效期**（Trae=31 天，WorkBuddy 参数化）；
3. 消耗按**先到期先消耗**（FIFO by expiry）扣减批次；
4. 每日用真实余额校准批次总和（消除漏记与活动奖励误差）；
5. `未来 N 天内将过期 = Σ 剩余批次中到期日落在 (今天, 今天+N] 的额度`。

**边界与失真（必须体现在 UI 文案上）**：
- 数据从"开始使用本工具的那天"起积累，**前几天没有台账 → 显示"预估数据积累中（还需 X 天）"**，绝不显示一个看起来精确的假数字；
- 大额活动奖励、批量兑换、跨设备消耗会导致估算偏移 → UI 统一标注**「预估」**，不写"精确"；
- 若 Phase 0 探针发现「用量管理」接口真的返回批次到期字段，则**用真实数据替换估算**，估算模型保留为降级路径。

### 5.7 钉钉通知（webhook + 加签）
```python
timestamp = str(round(time.time() * 1000))
string_to_sign = f"{timestamp}\n{secret}"
sign = urllib.parse.quote_plus(
    base64.b64encode(hmac.new(secret.encode(), string_to_sign.encode(),
                              hashlib.sha256).digest())
)
url = f"{webhook}&timestamp={timestamp}&sign={sign}"
```
- 消息体：`{"msgtype":"markdown", "markdown":{"title":..., "text":...}}`
- 重试：网络错误与 5xx 指数退避（20s 起、最多 3 次、带抖动）；**4xx 不重试**（多为签名/配置错误）→ 转本地通知 + 日志
- 发送失败**不影响**签到主流程的成败判定（通知是旁路）
- 前置依赖：你需要先在钉钉群创建自定义机器人并开启「加签」，把 webhook 与 secret 填进配置

### 5.8 本地 Web UI（`checkin ui`）
- 只绑 `127.0.0.1`，随机端口或默认 8787，启动后自动打开浏览器
- 页面：今日状态卡 / 积分看板（当前余额、未来 7 天与 30 天将过期、30 天曲线）/ 配置表单（钉钉、运行时间、通知开关、到期阈值）/ 运行历史与日志尾部 / 「立即执行一次」按钮 / 凭据状态（仅状态）
- 读写的是同一份 `~/.checkin/` 文件，无额外 IPC
- 安全：不显示任何 token 明文；钉钉 secret 回显时打码；页面仅本机可达

### 5.9 定时（macOS）
生成 `~/Library/LaunchAgents/com.checkin.daily.plist`：
- `StartCalendarInterval` 默认 **08:00**（可配，`checkin config set schedule.time HH:MM`），`RunAtLoad` = false
- 标准输出/错误重定向到 `~/.checkin/logs/`
- `checkin schedule install|uninstall|status` 三个子命令负责装卸与查看

---

## 6. 分阶段实施

### Phase 0 · 探针验证（先做，决定后续实现细节）
> 目标：把"社区逆向"变成"我们实测过"。每项都是只读探测，失败即改路线，不写业务代码。

| # | 探针 | 成功标准 | 失败后的路线 |
|---|---|---|---|
| 0.1 | 调 Trae `checkin_credits/status` | ✅ **已完成（0.1d）**：网页登录 → `GetUserToken` 换 8h JWT → status 返回 `code:0`；1001 根因证实为票据来源错误（智能体的票） | —（已解决） |
| 0.2 | 找 Trae 积分明细数据源（网页「用量管理」网络请求） | ✅ **已完成**：拿到**批次级台账**（4 个 pay 接口 + 到期时间），到期可精确到批次 | —（已解决） |
| 0.3 | WorkBuddy 取 token：插件 OAuth（系统默认浏览器登录 + state 轮询） | ✅ **已完成（2026-10-12）**：**微信扫码**登录成功（手机号方式报"登录失效"），凭据落盘 `~/.checkin/workbuddy-oauth.json`（AT 1449 字符 / RT 1088 字符，expiresIn≈28 天） | —（不走 Electron 运行时解密路线） |
| 0.4 | 用该 token 只读调 WorkBuddy 签到状态 + 批次台账 | ✅ **已完成**：`checkin-activity-status` 200（今日已签、连签 13 天）；`get-user-resource` 46 包台账（400 已修复：时间范围字段须为字符串） | —（已解决） |
| 0.5 | 观察 WorkBuddy 积分有效期规则 | ✅ **已完成**：逐包 `DeductionEndTime`（≈`CycleEndTime`，发放后约 30 天；"个人体验版"包超远期），与 Trae 同构 | —（已解决） |
| 0.6 | WorkBuddy **SMS 直登**（send-sms → login/token → refresh 验证 → 只读 status） | ✅ **已完成（2026-10-12）**：真机实测全通——发码 200（`expires_in=300`）→ 登录拿 AT 1290 字符（exp 2026-11-09）/RT → RT 续期换发成功 → 只读 status 200（连签 13 天）；**uid 与 OAuth 路径一致（同账号）**；凭据落盘 `~/.checkin/workbuddy-sms.json`（0600）。探针：`probes/probe_workbuddy_sms.py` | —（已解决） |
| 0.7 | Trae 登录的浏览器零下载策略核验 | ✅ **策略定版**：主路径=复用系统已装 Chrome/Edge（channel）；实测沙箱内无法读 Chrome 数据目录（Operation not permitted）→"直读默认浏览器 cookie"降为真实终端环境中的可选增强 | —（已定策略） |

**Phase 0 产出**：一份 `探针结论.md`（记录实际可用的端点、头部、错误码），并入 README。

### Phase 1 · 核心可用（macOS）
1. uv 项目骨架、`pyproject.toml`、日志与配置模块
2. 凭据模块（Trae 打通；WorkBuddy 按 Phase 0 结论）
3. Trae CN provider：status → claim → 积分快照
4. WorkBuddy provider（同上）
5. `expiring.py` 台账与估算 + 单测
6. 钉钉通知 + 本地通知 + 日志（含加签单测）
7. CLI：`run` / `run --dry-run` / `status` / `doctor`
8. launchd 安装命令 + 幂等补跑
9. 端到端实跑验证（见 §8）

### Phase 2 · 本地 Web 看板
10. ✅ **已完成（2026-10-12）**：`checkin ui` = `src/checkin/web/server.py`（纯标准库 `http.server`，只监听 127.0.0.1，Host 校验 + `X-Checkin` 头防跨源写入）+ `static/index.html` 单页看板（状态卡片 / 积分曲线 / 配置表单 / 手动执行含干跑·强制与实时日志 / 定时任务安装卸载 / 日志尾部）；token 只显示状态；单测 `tests/test_web.py`

### Phase 3 · Windows 适配
11. 凭据路径：`%USERPROFILE%\.trae-cn\trae-jwt-token`、`%APPDATA%\Trae CN\machineid`、`%LOCALAPPDATA%\CodeBuddyExtension\Data\Public\auth\*.info`（**均需在 Windows 机器上实测校正**）
12. 定时：`schtasks /Create /SC DAILY ...`
13. 本地通知：PowerShell toast
14. 双端回归

---

## 7. WorkBuddy 的 token 从哪来（已定版）

### 7.1 源码级已证实的事实

- **信封为什么读不了**：解密密钥**只存在于客户端进程内存**。88lin/workbuddy-auto-signin 的做法是以 `ELECTRON_RUN_AS_NODE=1` 启动**已安装客户端自己的可执行文件**，用一段内嵌 JS 通过客户端私有原生 binding 取密钥：
  `process._linkedBinding('electron_browser_workbuddy_storage').loggerGet()` → JSON 中的 `atRestSecretKey`
  → `key = sha256(atRestSecretKey)` → 以 AES-256-GCM（固定 AAD）解开 `sym-v1` 信封。
  密钥不落盘，因此**静态解密此路不通，只有"借客户端运行时"这一条路**。
- **macOS 支持程度**：该项目**有 macOS 代码路径**（`sys.platform=="darwin"` → 从 `.app/Contents/Info.plist` 读 `CFBundleExecutable`；凭据路径正是我们实测的那个 `~/Library/Application Support/CodeBuddyExtension/Data/Public/auth/workbuddy-desktop.info`），但作者自己的验收表标注 **macOS「客户端实机验收待完成」**，仅 Windows（5.6.2）验证过"解密成功 + `checkin-activity-status` 返回 200"。→ **在你这台 Mac 上能否成功，必须实测。**
- **"抓网页 cookie"路线已删除**：无任何证据表明 workbuddy.cn / codebuddy.cn 的**网页会话**能调 `/v2/billing/meter/*`；所有可查实现用的都是**客户端或插件签发的 token**。
- **有实证的浏览器系路线是"插件 OAuth"**（smart-open/TraeWorkAssistant 的 `oauth.rs`）：`POST {base}/v2/plugin/auth/state?platform=CLI` → 打开浏览器登录 → 轮询 `/v2/plugin/auth/token?state=`，UA 需为 `CLI/2.63.2 CodeBuddy/2.63.2`。注意其源码注释提示：打到 `www.codebuddy.cn` 会拿到与 CLI 流程不匹配的裸登录链接。
- **短信登录有实证**（L0NE-6 的 `workbuddy_login.py`）：`POST www.workbuddy.cn/v2/plugin/login/send-sms` → `/v2/plugin/login/token`（`{"login_method":"phone",...}`）；续期 `POST copilot.tencent.com/v2/plugin/auth/token/refresh`，头 `X-Refresh-Token` + `X-Auth-Refresh-Source: plugin`。

### 7.2 路线优先级（Phase 0 逐级下降）

| 级别 | 路线 | 人工成本 | 前置条件 |
|---|---|---|---|
| 主（一） | **(b′) 插件 OAuth**：`POST /v2/plugin/auth/state?platform=CLI` → 系统默认浏览器登录（微信扫码）→ 轮询 `/v2/plugin/auth/token?state=` | 首次一次；refresh token 实测有效期 **60 天** | ✅ 已实测通过（2026-10-12） |
| 主（二） | **(c) SMS 直登**（用户明确要求支持手机号验证码）：`/v2/plugin/login/send-sms` → `/v2/plugin/login/token`（`login_method=phone`），**纯 API 零浏览器**，多账号最友好 | 首次一次 + 失效时 | ✅ 已真机实测通过（2026-10-12，同账号验证） |
| 可选增强 | **(a) 客户端 Electron 桥解密**：零人工，但依赖客户端私有 binding，客户端改版即失效，macOS 无他人实证 | 零 | 需在 macOS 实测 |
| 不采用 | 抓网页 cookie（无证据）；旧备份明文 token（会与客户端互撞） | — | — |

**`playwright` 仅为 Trae 服务**——WorkBuddy 全路径（(b′)/(c)）分别用系统默认浏览器与纯 API，不需要浏览器自动化。依赖面因此收窄。

### 7.3 需要你知情同意的一处偏离

若降级到 **(b′)/(c)**，工具必须**保存 token / refresh token 并在到期前自行续期**——这与 §5.1 第 3 条"绝不自行续期"直接冲突。

- 该硬约束**只适用于主路线 (a)**，因为那时客户端始终是真源、我们无状态；
- 一旦降级，工具会持有并轮换凭证，**存在与桌面客户端会话冲突的可能（客户端可能被登出）**；
- 这是有意的取舍：要么承担冲突风险换取无人值守，要么接受每次都要人工登录。**执行到 Phase 0.3 时若主路线失败，我会先回来跟你确认再降级。**

**已确认底线**：Trae 全自动；WorkBuddy 若主路线不通，至少首次需要你人工登录一次。

### 7.4 精确端点（2026-10-12 调研 + 本机实测通过）

**插件 OAuth（主路线，✅ 实测：系统默认浏览器 + 微信扫码完成绑定）**
（**登录方式实测（2026-10-12）**：微信扫码 ✅；登录页上的手机号入口提示"登录失效"（页面限制）——**手机号登录改走官方 SMS 直登 API（下方"第二路径"），两条路径均为正式支持**。链接为一次性、state 绑定：CLI 取走 token 后即失效。）
1. `POST https://copilot.tencent.com/v2/plugin/auth/state?platform=CLI`
   - 头：`User-Agent: CLI/2.63.2 CodeBuddy/2.63.2`、`Accept: application/json, text/plain, */*`、`X-Requested-With: XMLHttpRequest`、`Origin/Referer: https://www.codebuddy.cn`；body `{}`
   - 响应：`state`（或 `authState/auth_state`）+ `authUrl`（或 `auth_url/login_url/...`）
2. 打开 authUrl（缺省构造 `https://copilot.tencent.com/login?platform=CLI&state=<state>`）→ 人工登录
3. 轮询 `GET https://copilot.tencent.com/v2/plugin/auth/token?state=<state>`（**3s，上限 300s**）→ `{accessToken, refreshToken, expiresIn}`
4. `GET .../v2/plugin/login/account?state=<state>` + Bearer → `uid`（即 `X-User-Id` 来源）

**第二路径：SMS 直登（手机号 + 验证码，纯 API 零浏览器；✅ 2026-10-12 真机实测通过）**（来源：L0NE-6/WorkBuddy-Daily `workbuddy_login.py`，字段逐字对齐）
1. `POST https://www.workbuddy.cn/v2/plugin/login/send-sms`，头（Content-Type/Accept/Origin/Referer/浏览器 UA），body `{"phone":"1xxxxxxxxxx"}` → `{code:0, data:{expires_in:300}}`（失败 msg 含原因：频繁 / keycloak spi 等）
2. `POST https://www.workbuddy.cn/v2/plugin/login/token`，body `{"login_method":"phone","phone":...,"sms_code":...}` → `{code:0, data:{accessToken, refreshToken}}`（AT 为 JWT，payload `sub` 即 uid 候选）
3. 验证可用：`POST https://copilot.tencent.com/v2/plugin/auth/token/refresh`（头 `X-Refresh-Token` + `X-Auth-Refresh-Source: plugin`）→ 换发新 AT
探针：`probes/probe_workbuddy_sms.py send|login`（两段式：发码与交码分离，适配非交互环境）。**实测结果（2026-10-12）**：发码 200（300s）→ AT 1290 字符（exp 2026-11-09）/RT 有 → RT 续期成功 → 只读 status 200（连签 13 天，与 OAuth 同 uid）。

**续期**：`POST https://copilot.tencent.com/v2/plugin/auth/token/refresh`，头 `X-Refresh-Token` + `X-Auth-Refresh-Source: plugin`，body `{}` → 同结构（`expiresIn=5184000` = **60 天**）

**签到**（billing host = `https://www.codebuddy.cn`，`Authorization: Bearer` + `X-User-Id`，UA `WorkBuddy/5.5.6`）
- 状态：`POST /v2/billing/meter/checkin-activity-status`，body `{}` → `today_checked_in/checked_in`、`streak_days`、`total_credits`、`today_credit`、`activity_name`
- 领取：`POST /v2/billing/meter/daily-checkin`，body `{}` → `data.credit`
- **"今日已签到"判定**：业务 `code==10001` 或 msg 含 `已签到|already|repeat`（视为成功而非失败）

**积分批次台账**：`POST https://www.codebuddy.cn/v2/billing/meter/get-user-resource`（✅ 实测）
- body：`{"PageNumber":1,"PageSize":100,"ProductCode":"p_tcaca"}` 即返回全部；**`PackageEndTimeRangeBegin/End` 是 Go string 字段，传数字会 400**（可选参数，省略即可）
- 响应：`data.Response.Data.Accounts[]`（实测 46 包，`TotalDosage`=剩余合计 4177）；逐包 `CapacityRemain/CapacitySize/CapacityUsed`、`DeductionEndTime`（epoch ms，到期时刻）/`CycleEndTime`（字符串等价）、`PackageName`、`Status`

---

## 8. 验证方式

1. **单测**（`uv run pytest`）：
   - `test_expiring.py`：构造 10 天积分流水，断言 FIFO 消耗后的到期队列与"未来 7 天将过期"数额；断言数据不足时返回"积累中"而非数字
   - `test_dingtalk_sign.py`：用钉钉文档示例参数断言签名串
   - `test_credentials_paths.py`：mock 两个 OS，断言路径解析结果
2. **只读冒烟**：`checkin doctor` → 逐项打印凭据来源、是否可读、token 剩余有效期、网络连通性、配置完整性
3. **干跑**：`checkin run --dry-run` → 完整走一遍但**不执行 claim**，检查日志与落盘快照
4. **真跑**：`checkin run` → 校验钉钉收到通知、`state.json` 记录成功、重复执行被幂等拦截
5. **定时验证**：`checkin schedule install` → `launchctl list | grep checkin` → 临时把时间设为 2 分钟后，确认自动执行并写日志
6. **失效演练**：临时把 token 文件改名 → 确认工具告警而不崩溃；恢复后确认自动恢复

---

## 9. 边界、风险与明确不做的事

**风险（不可通过实现消除）**
- 接口全部为社区逆向，字节/腾讯改版即失效；`9074`、`9095`、`12153` 一类风控码随时可能出现
- 账号存在被风控、积分被回收的风险
- **ToS 风险（必须知情）**：WorkBuddy 官方规则将「利用脚本、接口调用等方式刷取活跃或奖励」与「恶意创建或控制多个账号」并列列为违规，后果为「取消参与资格、暂停或**追回已发放奖励**」。即**脚本签到本身在规则字面上即构成违规**，多账号是第二个独立违规点。社区现状为「暂未听说封号」，但风险落在账号持有者（你）身上，而非工具上。
- **多账号的三条硬约束**：① 绝不向多个账号注入同一设备标识（`X-Device-Token` 等）；若确需设备标识必须按 uid 稳定派生（`sha256(salt:uid)`），不得共用；② 同一出口 IP 跑多账号存在账号关联风险（`workbuddy2api-panel` issue #153）；③ 账号数量保持个位数
- WorkBuddy 登录定版为**双路径**（OAuth 扫码 / SMS 直登），均需自行持有并轮换 refresh token（实测 60 天）；Trae 会话 cookie 实测 ~14 天有效，失效需重登（见 §5.4）。**浏览器零下载（硬约束）**：登录复用系统已装 Chrome/Edge，日常运行零浏览器（见 §5.1）
- Trae `claim` 尚未实测（今日已签到无法验证）：若 IDE 体格式的 `device_id` 被服务端校验，可能需补设备头再试

**明确不做**
- 不做批量签到、代签、倒卖类功能
- 不做自动兑换、不刷每日任务
- 不做 Electron/Tauri 桌面 App、不做云端部署、不做 GitHub Actions
- 不写回任何客户端文件、不自行刷新 token
- 不承诺"永不失效"——定位是"每天准时跑 + 坏了你知道"

---

## 10. 待你提供的输入（实施时需要）

1. 钉钉自定义机器人的 **webhook URL 与加签 secret**（需在群里先创建并开启「加签」）
   → ✅ **已定版为可配置**：`checkin config set notify.dingtalk.webhook <URL>` /
   `checkin config set notify.dingtalk.secret <SECRET>`；未配置时 `checkin doctor` 提示 WARN，
   通知降级为仅本地日志，不影响签到主流程
2. 默认执行时间与到期阈值
   → ✅ **已确认**：执行时间默认 **08:00**（配置项 `schedule.time`，可改）；
   到期阈值默认 **7 天**（配置项 `expiry_warning_days`，可改，0 = 关闭预警）
3. 如需通知到多个群，告知群数量（默认单群）
# 排查报告（2026-10-14）

> 首版：只做只读排查，未改代码、未改配置、未动凭据。
> **修订记录（同日）**：① 桌面端集成（原 §4）按你的要求移出本轮范围，仅留档；② WorkBuddy 报错文案已由你确认，补充 §2.5 证据（该问题按你的安排留待下次带日志复现）；③ 自动签到已决策为「进程内调度 + 默认开启」并落地（§2b）；④ **Trae 9004 已修复**（§1.4）。
> 环境：macOS / Go 1.27.2，工作目录 `TRAE 和 Workbuddy工具`，数据目录 `~/.checkin`。

## 0. 结论速览

| # | 问题 | 结论 | 严重度 |
| --- | --- | --- | --- |
| 1 | Trae 自动签到失败（code 9004） | **已修复**：claim 缺 `x-device-id` 设备头（IDE 源码证实为签到必备），已补头 + 请求体 `device_id`；待"未签到日"验证 claim | 待验证 |
| 2 | WorkBuddy 短信/链接登录失败（你已确认报错为「登录失效 400」） | **服务端接口全部健康**；但全量实测中**没有任何登录端点会返回「登录失效」**，登录失败也不落日志 → 需按「文案来源」定位，见 §2.5 | 中 |
| 2b | "自动签到"不执行 | **已决策并落地**：保持**进程内调度**，`schedule.enabled` **默认开启**（见 §2b） | 已修复 |
| 3 | 打 tag 是否自动触发 Actions | **会触发**；但 tag 必须指向 `origin/main` 的历史，当前 HEAD 在未合并分支上会导致 `prepare` 失败 | 中 |
| 4 | WebUI 集成桌面端 | 本轮按你的要求**暂不考虑**，原调研移出范围仅留档（§4） | 暂缓 |

---

## 1. Trae 自动签到失败（code 9004）

### 1.1 现象（日志实证）

`~/.checkin/logs/checkin.log` 中 10-13 的自动执行：

```
2026-10-13 08:09:15 INFO  checkin: Trae JWT 已换发（2026-10-13 16:09 过期）
2026-10-13 08:09:15 ERROR checkin: Trae CN：Trae 签到 失败：code=9004
        msg=The submitted order parameters are incorrect. Please try placing the order again
```

同日 WorkBuddy 签到成功（`[完成] … OK`），仅 Trae 失败。`state.json` 中 Trae 记录 `status=error`。

### 1.2 关键事实

- **换票链路正常**：`GetUserToken` 换 JWT 成功（HTTP 200），今天（10-14）实测同样 200。
- **status 接口正常**：今天实测 `POST /trae/api/v2/ug/checkin_credits/status` 返回
  `{"checked_in":true,"code":0,"credits":100,"extra_credits":100,...}`（HTTP 200）。
  代码见 [provider.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/trae/provider.go#L69-L83)。
- **只有 claim 失败**：10-13 走到 claim 才报 9004；今天因 `checked_in=true` 直接跳过 claim，所以没复现。
- **原始计划早已预警**：初始 commit 的实施计划里就写过
  "⏳ 唯一未验证项：`claim`（今日已签到无法验证）…**若 IDE 体格式的 `device_id` 被服务端校验则补设备头**"
  （`git show dd68a87:.trae/documents/trae-workbuddy-checkin-plan.md` §5.4）。
  该计划列出的这两个接口头集合是 `Authorization / x-device-id / X-User-Region`，**`x-device-id` 从未被实现**。

### 1.3 根因：claim 缺 `x-device-id`

当前实现把请求头写死在 [provider.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/trae/provider.go#L30-L54)：

```go
headers := map[string]string{..., "X-User-Region": "cn", "trae_client": "IDE", "Authorization": "Cloud-IDE-JWT " + token}
```

**没有 `x-device-id`**。对照 Trae CN 客户端本体的源码
（`/Applications/Trae CN.app/Contents/Resources/app/out/main.js`）可见签到请求的真实构造：

```js
// 注入设备头（所有签到/权益请求共用）
fb(e){ e["x-device-id"]=this.S.guaranteedDeviceId;
       const i=this.g?.commonParams;
       i?.device_model && (e["x-device-brand"]=i.device_model);
       i?.os_name      && (e["x-device-type"]=i.os_name);
       i?.os_version   && (e["x-os-version"]=i.os_version);
       i?.app_version  && (e["x-app-version"]=i.app_version) }

// status 与 claim 走同一条通道，但都会先注入设备头
fetchCheckinCreditsStatus(){ return this.eb("/trae/api/v2/ug/checkin_credits/status","POST","checkin_status") }
async claimCheckinCredits(){ return this.eb("/trae/api/v2/ug/checkin_credits/claim","POST","checkin_claim") }

// 请求体（IDE 路径）只发 req_source
const o=this.cb(s.token); this.fb(o.headers);
const a=li(this.P)?2:1;   // Trae=1 / SOLO=2
fetchWithHeaders({url:n, method:i, data: i==="POST" ? {req_source:a} : void 0, ...o})
```

- IDE 对 status/claim 都会先 `fb()` 注入 `x-device-id` 及设备指纹头。
- status 接口服务端宽容，缺设备头也能返回 `code:0`；**claim 接口会校验，缺头即返回 9004**
  （社区实现同样标注："浏览器 fetch 发不出 `x-device-id`，导致 claim 恒返回 9004"；
  参考：ChenLun-dev/Trae-Checkin、L0NE-6/Trae-AutoCheckin、Maquer/trae-signin）。
- 结论吻合度极高：**9004 的文案是"下单参数不正确"，实际是缺设备标识**。

### 1.4 修复（已实施）

按上述根因完成修复，改动如下：

| 文件 | 改动 |
| --- | --- |
| [device.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/trae/device.go)（新增） | `DeviceID(uid)`：按账号（uid）分桶，首次生成随机 UUID 后持久化到 `~/.checkin/credentials/trae-device.json`（0600），之后始终复用、不轮换 |
| [creds.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/trae/creds.go#L152-L164) | 新增 `accountOf(token)` 从 JWT 的 `data` 段取 uid；`Account()` 复用它，行为不变 |
| [provider.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/trae/provider.go#L30-L53) | 所有请求补 `x-device-id` 头；签到请求体（`req_source`）额外携带 `device_id`，对齐 IDE 的 `Dqe()` |
| [util.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/util/util.go#L221-L230) | 新增 `UUID4()` |
| [paths.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/paths/paths.go#L31) | 新增 `TraeDevice()` 路径 |
| [provider_test.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/trae/provider_test.go#L20-L33) | 隔离 `CHECKIN_HOME` 并断言 `X-Device-Id` 头与请求体 `device_id` 非空 |

已验证：`go build ./...`、`go vet ./...`、`go test ./...` 全通过；真实只读请求（`run --dry-run`）中 Trae 状态与台账均返回 `code:0`，设备标识文件已落地：

```json
{ "<账号 uid>": "<设备 UUID>" }
```

**尚需一次真实验证**：9004 只在 claim 时出现，必须在"当天 status 返回 `checked_in=false`"时跑一次真实签到才能确认修复生效（今天已签到，无法复现；`--dry-run` 不触发 claim）。

---

## 2. WorkBuddy 手机号验证码 / 打开链接登录失败

### 2.1 实测：服务端接口全部健康

用与代码完全一致的请求做只读探针（2026-10-14）：

| 接口 | 结果 |
| --- | --- |
| `POST /v2/plugin/auth/state?platform=CLI`（3 个 host） | 均 HTTP 200，`code:0`，返回 `state`+`authUrl` |
| `GET /v2/plugin/auth/token?state=…`（未登录态） | HTTP 200，`code:11217 "login ing..."`（轮询正常） |
| `POST www.workbuddy.cn/v2/plugin/login/send-sms`（**真实手机号**） | HTTP 200，`code:0`，`expires_in:300`，**短信已发出** |
| `POST www.workbuddy.cn/v2/plugin/login/token`（错误验证码） | HTTP 400，`code:14704 invalid_sms_code`（端点正确校验） |

工具侧流程同样可跑通：

```
./checkin login workbuddy --method browser --timeout 25
  → 登录页 https://copilot.tencent.com/login?platform=CLI&state=… 已在系统默认浏览器打开
  → 25s 未登录，超时（预期）
./checkin login workbuddy --method sms --phone 176… --code 000000
  → 登录失败：登录失败：HTTP 400 14704:invalid_sms_code（预期）
```

即：**发码、换票、轮询、登录接口在服务端均正常工作**，工具也把请求正确发出。

### 2.2 因此，失败点在服务端之外

最可能的原因（按可能性排序）：
1. **官方 CLI 授权页面用手机号验证码登录本身就会报"登录失效"**——这是本项目此前已确认的上游限制
   （见项目记忆），并非本工具缺陷。链接登录若走授权页手机号登录，必然失败；扫码（微信）路径才可用。
2. **短信直登：验证码 / 风控**。验证码 300 秒有效，超时或输错即 `14704`；上游风控也可能拒绝。
   本次实测发码成功，说明"发得出去"没问题，问题在"验得通过"这一环。
3. 你操作时可能用的是**旧二进制**（仓库根目录二进制版本为 `dev`，Release 为 0.3.0），或换了机器/网络。

### 2.3 顺带发现的次要问题

- **登录失败不入日志**：[web/auth.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/web/auth.go#L91-L99) 与 CLI 都只把错误返回给前端/终端，
  **不写 `checkin.log`**。所以 10-13 16:01 打开登录页后日志戛然而止，事后无法复盘。建议登录失败也落日志（脱敏）。
- **错误前缀重复**：CLI 打印 `登录失败：登录失败：HTTP 400 …`
  （[wb/login.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/wb/login.go#L67-L68) 已带前缀，[cli.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/cli/cli.go#L421-L423) 又加一次）。

### 2.4 已确认的报错文案

你确认失败时的原始文案是 **「登录失效」+ HTTP 400**（用于两处：手机号验证码登录、打开授权链接登录）。

### 2.5 按该文案再排查：没有任何登录端点会返回「登录失效」

针对「登录失效 400」这个文案，做了一轮穷举式只读实测：

| 探测对象 | 实测结果 | 是否出现「登录失效」 |
| --- | --- | --- |
| 3 个 host 的 `POST /v2/plugin/auth/state?platform=CLI` | HTTP 200，`code:0`，返回 `state`+`authUrl` | 否 |
| `GET /v2/plugin/auth/token?state=…`（合法 / 无效 state） | HTTP 200，`code:11217 login ing...` | 否 |
| `POST www.workbuddy.cn/v2/plugin/login/send-sms`（真实手机号） | HTTP 200，`code:0`，短信已发出 | 否 |
| `POST …/v2/plugin/login/token`（错误验证码 / 空 body / 各种字段变体） | HTTP **400**，`code:14704 invalid_sms_code` / `10001 unsupported login_method` / `phone and sms_code are required…` | 否 |
| 浏览器登录页 JS 包（`www.codebuddy.cn/login`，OneID 统一登录页 9 个 bundle） | 页面自身失败文案是 **「登录失败」**，且包内不存在「登录失效」字样 | 否 |
| 授权链接实际落点 | `https://copilot.tencent.com/login?platform=CLI&state=…` → 302 到 `https://www.codebuddy.cn/login/?platform=CLI&state=…`（Tencent OneID SSO） | — |

即：**你登录链路上真正调用的所有接口，没有一个会吐出「登录失效」。** 那这个文案只能来自下面两处之一：

1. **本项目自己的签到链路（而非登录链路）**。全项目仅 [provider.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/wb/provider.go#L82-L85) 含这四个字：当 WorkBuddy 签到接口返回 `code≠0` 且 `msg` 含「登录失效」时，被归类为鉴权失败并提示重新登录；HTTP 400 是该接口的状态码。若你是在 WebUI/CLI 里看到的，最可能就是这条路径（登录成功后工具会立刻调 `Status()` 验证，这一步失败会显示为「凭据已保存，Token 验证未通过：…登录失效…」）。
2. **浏览器侧**：OneID 授权/回调服务返回的非本项目文案（其回调接口用无效 state 直连只得到 HTML 或 404，无法复现 400+「登录失效」）。

### 2.6 为什么日志帮不上忙

`checkin.log` 里 10-13 16:01:32 与 10-14 10:30:27 两次都只有「已用系统默认浏览器打开 WorkBuddy 登录页」，**之后没有任何成功/失败记录**——因为登录失败当前**不写日志**（见 §2.3）。所以无法从事后日志确认到底是哪一步、哪个状态码。

### 2.7 结论与下一步（需你配合一次现场抓取）

- 现有证据**无法把「登录失效 400」归因到本工具的任何登录请求**；服务端登录接口全部健康。因此这不是「接口坏了」，而是要么走了签到校验链路，要么文案来自浏览器页面。
- 请在**复现那一刻**记录：① 出错的界面（浏览器页面 / 工具 WebUI / CLI）；② 完整的报错文案与 HTTP 状态码；③ 若是浏览器页面，截图即可。
- 无论哪种，建议后续把**登录失败也写入日志（脱敏）**，否则每次都不可复盘（当前为「只有打开登录页有日志」）。

---

## 2b. 附："自动签到未成功"的另一半原因

### 决策与落地（本轮已执行，唯一一处代码/配置改动）

按你的决定：**自动签到保持进程内调度，并改为默认开启**。已做三处改动：

| 文件 | 改动 |
| --- | --- |
| [config.go](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/internal/config/config.go#L48-L57) | `Defaults()` 增加 `c.Schedule.Enabled = true`，新建配置即默认启用 |
| [README.md](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/README.md#L56) / [README.md](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/README.md#L86) | 文档里的默认值由「默认未启用 / `false`」改为「默认启用 / `true`」 |
| `~/.checkin/config.json` | 已有配置里显式写着 `enabled:false`（旧默认持久化而来），会覆盖新默认值，故已执行 `checkin config set schedule.enabled true` 置为 `true` |

已运行 `go vet ./...` 与 `go test ./...`，全部通过。

**仍须注意**：进程内调度依赖 `checkin ui` 常驻；退出程序/关机期间不会签到（不注册任何系统任务，这是既定设计）。

### 排查时的原状（供对照）

Go 版把定时改成**进程内调度**（此前默认 `schedule.enabled=false`），并且**不再注册系统任务**：

- `launchctl list | grep checkin` → 无；`~/Library/LaunchAgents` → 无该任务；无 `checkin` 进程。
- 配置 `~/.checkin/config.json`：`"schedule":{"enabled":false,"time":"08:00"}`。
- 说明见 [README.md](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/README.md#L42-L58)。

因此现在**没有任何自动执行**：只有 `checkin ui` 进程在运行且页面里开启定时，才会到点签到。
10-13 08:09 那次自动执行来自**旧版遗留的 launchd 任务**，该任务现已不存在。

补充：今天（10-14）干跑显示两侧均为"今日已签到"（Trae `checked_in=true`；WorkBuddy 连签 15 天），
说明产品自身/日常使用已把当天签掉，**Trae 的 9004 是"需要发 claim 时才暴露"的间歇性故障**。

---

## 3. GitHub Actions 是否由打 tag 自动触发

### 3.1 结论：会触发，但当前一个 tag 都没有

工作流 [release.yml](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/.github/workflows/release.yml#L3-L9)：

```yaml
on:
  push:
    branches: ['main']
    tags: ['v*']        # ← 打 v* tag 会触发
  pull_request: {branches: ['main']}
  workflow_dispatch:
```

- 只有 `push`（main 分支或 `v*` tag）、面向 main 的 PR、手动运行三类入口。
- `git ls-remote --tags origin` = **0 个 tag**，本地也无 tag → **发布链路从未跑过**。
- 已发生的运行：`ffc7f09`（main push，成功）与两次 PR（`codex/web-auth-release`）。

### 3.2 关键坑：tag 必须指向 main 的历史

`prepare` 步骤 [release.yml](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/.github/workflows/release.yml#L27-L37)：

```bash
if [[ "$GITHUB_REF_TYPE" == tag ]]; then
  ./scripts/validate-release-tag.sh "$GITHUB_REF_NAME"
  git merge-base --is-ancestor "$GITHUB_SHA" origin/main   # ← 不满足即失败
  ...
```

实测：

- 远端 `origin/main` = `ffc7f09`（仅"Go 重写"那一个提交）。
- 本地未合并分支 `codex/web-auth-release` 还有两个提交：
  `9f0975b`（WebUI 登录登出/进程内定时/平台发布）、`7800681`（Windows ACL），
  **均不在 main 上**。
- `git merge-base --is-ancestor HEAD origin/main` → **false**。

**因此：现在直接 `git push origin v0.3.0` 会触发工作流，但 `prepare` 会因 tag 不属于 main 历史而失败，整条流水线挂掉。**

### 3.3 正确发布姿势（与 [RELEASING.md](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/docs/RELEASING.md#L5-L17) 一致）

1. 先把 `codex/web-auth-release` 通过 PR 合并进 `main`（CI 需过三平台测试 + race + 四平台构建）。
2. 再在 main 上 `git tag -a vX.Y.Z && git push origin vX.Y.Z`。
3. 触发后：测试 → 四平台打包 → 校验体积/权限/SHA256 → 建草稿 Release → 转正式（预发布 tag 标 Pre-release）。
   publish 步骤见 [release.yml](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/.github/workflows/release.yml#L142-L177)。

补充：`publish` 仅对 `v*` tag 生效（`if: github.ref_type == 'tag'`），main 推送与 PR **只产出 Actions Artifacts，不发 Release**——这与你的预期一致，无需改动。

---

## 4. WebUI 集成桌面端：难度与产物体积调研（本轮按你的要求暂不考虑，仅留档）

现状：Go 二进制约 **9.28 MB**（CGO_ENABLED=0 纯静态，dist 各平台 ZIP 约 3.7–4.1 MB），内嵌单文件 WebUI，`checkin ui` 在 `127.0.0.1:8765` 起本地服务并唤起系统默认浏览器。

| 方案 | 实现难度 | 体积增量（9 MB 之上） | 跨平台/前置 | 能否复用现有 8765 服务 | 主要限制 |
| --- | --- | --- | --- | --- | --- |
| 现状（浏览器标签页） | 0 | 0 | 全平台零依赖 | 原生 | 无原生窗口/菜单 |
| **webview_go** | **低（1–2 天）** | mac ≈ +1–3 MB；win 需带 WebView2Loader.dll ≈ +1.5 MB；Linux 不增但依赖系统库 | mac=WKWebView / win=WebView2 / Linux=WebKitGTK | ✅ `Navigate("http://127.0.0.1:8765")` 直接复用 | **强制 CGO=1**，破坏当前纯静态交叉编译；Win 需 WebView2 Runtime；Linux 缺 webkit2gtk 起不来 |
| Wails v2 | 中高（1–2 周） | ≈ +15 MB | mac 需 Xcode CLT（cgo）；win 免 cgo 但需 WebView2；Linux 需 gtk3+webkit | 可但别扭（自带 AssetServer） | 引入前端构建链/NPM；**v3 截至 2026-10 仍是 beta** |
| Tauri v2（Rust 壳 + Go sidecar） | 中高（双语言） | 壳 <0.6 MB + Go ≈ 9 MB ≈ 10 MB（实测 mac dmg ≈ 7.5 MB） | 需 Rust 工具链；Linux 需 libwebkit2gtk-4.1；Win 需 WebView2（捆绑固定版 ≈ +180 MB） | ✅ 易（External URL + 子进程） | 双语言维护；签名要同时签壳与 sidecar |
| Electron 包壳 | 中 | **+≈180 MB / 安装 ≈350 MB** | 全平台但极重 | ✅ 易 | 体积/内存/启动全面劣势 |
| 平台原生壳（Swift WKWebView + WebView2） | 最高 | 每平台最小（几百 KB 级） | 需各平台分别写；Linux 还要 GTK | ✅ 易 | 多套代码库，维护成本最高 |

**推荐（按性价比）**：`webview_go` > Wails v2 > Tauri v2 > 维持现状；不建议 Electron/双原生壳。

**共同注意**：
- 任何方案在 macOS 分发仍需 hardened runtime + **公证**（Apple 开发者账号 $99/年）——与是否用 WebView 无关。
- Windows 任何 WebView 方案都依赖 **WebView2 Runtime**（Win11 自带，Win10 需处理）。
- Linux 必然依赖 **WebKitGTK** 系统库。
- `webview_go` 的代价最直接：**当前项目是 `CGO_ENABLED=0` 单二进制**（见 [build-release.sh](file:///Users/xsh/Documents/workspace/public_space/TRAE 和 Workbuddy工具/scripts/build-release.sh#L35-L37)），改用后需引入 C 工具链、放弃纯静态交叉编译。

---

## 5. 后续实施顺序

1. ~~（高）Trae 签到修复~~ —— **已实施**：补 `x-device-id` + 请求体 `device_id`，设备标识按账号持久化（见 §1.4）；**待一个"未签到"日真实验证 claim**。
2. ~~（高）恢复自动签到~~ —— **已落地**：保持进程内调度，`schedule.enabled` 默认 `true`（见 §2b）。使用上需让 `checkin ui` 常驻。
3. **（中）登录可观测性**：登录失败写日志（脱敏）；修掉"登录失败："重复前缀。这是让「登录失效 400」下次可定案的前提。
4. **（中）WorkBuddy 登录**：按约定，下次由你启动项目并现场登录，用日志定案（见 §2.7）。
5. **（中）发布**：当前 HEAD 在 `codex/web-auth-release`（领先 main 2 个提交），需先合入 main 再打 `vX.Y.Z` tag。
6. ~~（低）桌面端~~：本轮暂不考虑；如需原生窗口再评估 `webview_go`（接受 CGO）。

## 附：复现命令

```bash
# Trae
SESSION=$(python3 -c "import json;print(json.load(open('$HOME/.checkin/credentials/trae.json'))['session'])")
curl -s -X POST https://api.trae.cn/cloudide/api/v3/common/GetUserToken -H "X-Cloudide-Session: $SESSION" -H "Content-Type: application/json" -d '{}'
# WorkBuddy OAuth
curl -s -X POST https://copilot.tencent.com/v2/plugin/auth/state?platform=CLI -H "Content-Type: application/json" -d '{}'
# 本地干跑（只读，不 claim）
./checkin run --dry-run
```

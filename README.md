# checkin · Trae CN + WorkBuddy 签到与积分助手

Go 单文件程序，内嵌本地 WebUI，支持账号登录、每日签到、逐批积分台账和到期预警。用户运行时无需安装 Go 或 Python；Trae 登录复用系统 Chrome / Edge / Chromium。

**定时签到随程序进程启停。** 默认启用；仅在程序运行期间执行，关闭 EXE 窗口、结束进程、Ctrl+C 或点击页面「退出程序」后停止。安装和运行均不会注册新的系统后台任务。

## 下载与启动

从 [GitHub Releases](https://github.com/qinqingwuwei/auto-checkin/releases) 下载当前版本对应平台的 ZIP。`<版本>` 为不带 `v` 的版本号，例如 `0.1.0`：

| 平台 | 压缩包 |
| --- | --- |
| macOS Apple Silicon | `checkin-<版本>-darwin-arm64.zip` |
| macOS Intel | `checkin-<版本>-darwin-amd64.zip` |
| Windows x64 | `checkin-<版本>-windows-amd64.zip` |
| Linux x64 | `checkin-<版本>-linux-amd64.zip` |

一个平台一份 ZIP，每份同时支持 Trae CN 与 WorkBuddy。ZIP 内直接包含二进制、README 和对应安装脚本，无需从源码安装运行时。正式 Release 另提供 `SHA256SUMS`，用于核对文件完整性；GitHub 自动附带的 Source code 是源码包。

解压后运行：

```bash
./checkin ui
# 不带命令也会打开看板：./checkin
# Windows：双击 checkin.exe，或运行 .\checkin.exe ui
```

在页面「账号登录」中登录，然后手动执行一次签到。在「配置」中设置每日时间并点击「启用定时签到」。页面显示运行状态和下次执行时间。macOS 如提示无法打开，可在系统设置「隐私与安全性」中允许打开。

需要安装到固定位置时，在解压目录运行 `./setup.sh`；Windows PowerShell 运行 `.\setup.ps1`。脚本仅安装二进制到 `~/.checkin/bin/` 并显示启动命令，不自动登录、领取积分或启用定时。直接从解压目录运行也可以。

## WebUI 账号与程序管理

- **Trae CN**：选择系统浏览器并点击「浏览器登录」。打开独立窗口，8 分钟内完成登录，捕获会话后验证换票。
- **WorkBuddy**：点击「浏览器登录 / 添加账号」，在授权页面完成登录；也可展开短信登录，输入手机号、发送验证码并登录。支持多个账号分别登出；发码冷却按手机号独立计算，换号不受影响，发送失败不占用冷却。
- **取消等待**：浏览器登录或短信请求未完成时，可取消。程序退出也会中断正在等待的操作。
- **登出**：清除本工具的凭据、对应遗留导入文件和 Trae 专用浏览器会话，保留积分历史。不会注销官方网站或其他产品客户端，也不承诺服务端撤销已签发的 token。
- **退出程序**：关闭本机服务并停止定时、取消正在执行的网络操作。**仅关闭浏览器标签页不会结束程序进程。**

页面资源嵌入二进制，不依赖源码目录。服务仅监听 `127.0.0.1`，校验 Host；写接口要求 `X-Checkin: 1`，不开放 CORS。登录凭据与验证码不返回给前端；前端只显示账号状态、操作结果及临时浏览器授权链接。

## 定时机制

所有平台均使用程序内调度，由 `checkin ui` 进程持有计时器：

| 场景 | 行为 |
| --- | --- |
| 程序启动，定时已启用 | 等待本机时区下的下一次设定时间 |
| 到达时间 | 通过共享执行队列签到；页面实时显示执行结果 |
| 页面手动执行或账号操作正忙 | 当前进程的定时触发等待该操作结束 |
| 修改时间或关闭定时 | 立即生效 |
| 关闭 EXE / 退出程序 / 结束进程 | 计时器和本机服务停止，不再签到 |
| 电脑关机，或程序未运行 | 不执行；重新启动后等待下一次设定时间 |
| 程序仍在运行，电脑睡眠后唤醒 | 恢复后补触发一次已到期的定时 |

启用状态保存到配置中，重新启动程序后继续使用。默认时间为 **08:00**，默认启用。无需安装 launchd、任务计划程序或 cron。

旧版曾注册系统任务的用户，启动新 WebUI 或执行 `schedule enable/disable` 时会识别并清理本工具为当前数据目录创建的 `com.checkin.daily` / `checkin-daily`。清理失败会明确报错；其他数据目录的任务不会被误删。手工配置的 cron 或第三方启动项需自行移除。

## 命令

| 命令 | 说明 |
| --- | --- |
| `ui [--port 8765] [--no-open]` | 启动本地 WebUI 和程序内调度；无参数运行也会启动 UI |
| `run [--dry-run] [--force] [--only trae\|workbuddy]` | 单次签到与台账；`--only` 可重复 |
| `status [--json]` | 本地记录、凭据状态和积分快照 |
| `doctor [--offline]` | 配置、通知、浏览器、凭据和只读网络检查 |
| `login trae [--channel chrome\|msedge\|chromium] [--timeout 480]` | 浏览器登录并验证换票 |
| `login workbuddy [--method browser\|sms] [--phone] [--code] [--timeout 300]` | 浏览器或短信登录；不带 `--code` 时先发送验证码 |
| `logout trae` | 清除工具中的 Trae 登录状态 |
| `logout workbuddy --uid <账号 UID>` | 清除指定 WorkBuddy 账号；UID 可通过 `status --json` 查看 |
| `schedule enable\|disable\|status` | 保存定时开关或查看配置，实际运行状态以 WebUI 为准 |
| `config show\|get\|set\|path` | 点路径配置，密钥打码 |
| `--version` / `--help` / `-v` | 版本 / 帮助 / 调试日志；`-v` 放在命令前 |

旧 `schedule install/uninstall` 保留为 `enable/disable` 的兼容别名，已不再安装系统任务。单独执行 `schedule enable` 后命令会结束；需保持 `checkin ui` 运行才能定时签到。

`--force` 忽略本地成功日期，仍先查询远端状态，今日已签时跳过 claim。`--dry-run` 不领取、不写 `state.json` 或 `points.jsonl`、不发通知；日志仍记录，凭据缓存可按需刷新。签到、登录和登出共享系统文件锁，避免定时任务在登出过程中重新写入凭据。

## 配置与数据

默认 `~/.checkin/config.json`，可用 `CHECKIN_HOME` 或全局参数 `--home <目录>` 覆盖。

| 点路径 | 默认 | 说明 |
| --- | --- | --- |
| `schedule.enabled` | `true` | 程序内定时开关 |
| `schedule.time` | `08:00` | 每日时间，保存后即时生效 |
| `notify.dingtalk.enabled` | `true` | 钉钉开关 |
| `notify.dingtalk.webhook` / `notify.dingtalk.secret` | 空 | webhook 与加签 secret |
| `notify.local.enabled` | `true` | 系统通知开关（macOS / Windows） |
| `notify.daily_summary` | `false` | 每日成功简报，默认仅异常或到期预警通知 |
| `products.trae.enabled` / `products.workbuddy.enabled` | `true` | 产品签到开关 |

```bash
./checkin config set schedule.time 07:30
./checkin schedule enable
./checkin ui
```

未配置钉钉仍可签到，结果会写入日志。macOS 使用系统通知中心，Windows 使用 PowerShell Toast（失败时回退托盘气泡）；Linux 使用日志与钉钉。

Go 版直接读写原有 `~/.checkin/`：`config.json`、`state.json`、`points.jsonl`、`logs/`、`credentials/trae.json`、`credentials/workbuddy/<uid>.json`。支持 `trae-web-state.json`、`workbuddy-sms.json`、`workbuddy-oauth.json` 的首次导入。凭据与配置原子写入；macOS / Linux 使用 0600 文件与 0700 目录权限，Windows 使用仅允许当前用户和 SYSTEM 的原生 ACL；日志每 2 MiB 轮转，保留 5 份。凭据当前是本地文件保存，尚未集成系统密钥库。工具不写入产品客户端目录。

Trae 会话经 POST `GetUserToken` 换 JWT，提前 10 分钟换票；请求返回 401/403 或票据失效（12153）时自动重新换票并重试一次。WorkBuddy 的 AT 不足 6 小时自动续期，业务接口遇到 401/403 或登录失效时会用 refresh token 自动续期并重试一次，业务接口与 OAuth 接口使用各自 host。

## 开发与发布

源码兼容 Go **1.23+**，CI 和发布固定使用 Go **1.27.2**：

```bash
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o checkin ./cmd/checkin
./scripts/build-release.sh 0.3.0
```

未注入发布版本的源码构建显示 `dev`；Tag `v0.1.0` 的二进制显示 `0.1.0`。构建脚本输出至 `dist/<版本>/`，使用 ZIP 最高压缩级别并固定文件时间、执行权限；每个平台原始二进制限制为 12 MiB。

GitHub Actions 只在推送 `vMAJOR.MINOR.PATCH` Tag 时运行：先做三平台测试和 Linux race 检查，再四平台打包。分支推送、PR 与手动运行都不触发构建。Artifacts 分开展示：

| Actions 下载项 | 内容 |
| --- | --- |
| `checkin-darwin-arm64` | Apple Silicon ZIP + 该 ZIP 的 SHA256 |
| `checkin-darwin-amd64` | Intel macOS ZIP + SHA256 |
| `checkin-windows-amd64` | Windows x64 ZIP + SHA256 |
| `checkin-linux-amd64` | Linux x64 ZIP + SHA256 |
| `checkin-checksums` | 全部四个 ZIP 的 `SHA256SUMS` |

GitHub Artifacts 下载会再套一层 ZIP，是 GitHub 的标准行为；各平台已独立。只有推送合法 `vMAJOR.MINOR.PATCH` Tag 才触发构建并发布 Release，包含四个独立平台 ZIP 和 `SHA256SUMS`。预发布 Tag 如 `v0.1.0-rc.1` 标记为 Pre-release。正式发布步骤见 [发布流程](docs/RELEASING.md)。

仓库仅提交源码、测试、文档和构建脚本，`.trae/`、二进制与 dist/ 不提交。

## 风险、限制与致谢

接口来自社区公开实现，产品改版或风控可能使其失效。WorkBuddy 规则限制通过脚本或接口领取活跃奖励，多账号也可能带来账号风险，请自行判断。真实扫码与短信验证码须由账号本人完成，自动化测试使用模拟接口。

当前二进制尚无 Windows Authenticode / macOS 签名与公证。项目采用 [MIT 许可证](LICENSE)。后续优化建议见 [改进清单](docs/ROADMAP.md)。

接口参考：[workbuddy-auto-signin](https://github.com/88lin/workbuddy-auto-signin)、[WorkBuddy-Daily](https://github.com/L0NE-6/WorkBuddy-Daily)、[Trae-Checkin](https://github.com/ChenLun-dev/Trae-Checkin)、[TraeWorkAssistant](https://github.com/smart-open/TraeWorkAssistant)。

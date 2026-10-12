# checkin · Trae CN + WorkBuddy 签到与积分助手

每天定时签到、读取逐批积分台账，并在积分临近到期或执行异常时通知。Go 单文件二进制内嵌本地 Web 看板，运行签到无需 Go、Python 或其他运行时。Trae 浏览器登录复用系统 Chrome / Edge / Chromium，不下载浏览器。

## 快速开始

从 [GitHub Releases](https://github.com/qinqingwuwei/auto-checkin/releases) 下载并解压对应包：

| 平台 | 压缩包 |
| --- | --- |
| macOS Apple Silicon | `checkin-darwin-arm64.zip` |
| macOS Intel | `checkin-darwin-amd64.zip` |
| Windows x64 | `checkin-windows-amd64.zip` |
| Linux x64 | `checkin-linux-amd64.zip` |

在解压目录执行（Windows 使用 `checkin.exe`）：

```bash
./checkin doctor
./checkin login trae
./checkin login workbuddy --method browser
# 或短信登录：不带 --code 时先发码并提示输入；提供 --code 时直接使用已有验证码
./checkin login workbuddy --method sms --phone 138xxxxxxxx
./checkin run --dry-run
./checkin run
./checkin ui
```

首次使用尚未登录时，`doctor` 会报告缺少凭据。登录后再次自检即可。macOS 如提示无法打开下载的二进制，可在系统设置“隐私与安全性”中选择允许打开。

安装至固定位置后再启用定时任务，不能删除或移动该二进制：

```bash
mkdir -p ~/.checkin/bin
cp ./checkin ~/.checkin/bin/checkin
~/.checkin/bin/checkin schedule install
~/.checkin/bin/checkin schedule status
```

Windows 可将 `checkin.exe` 放在固定目录，执行 `checkin.exe schedule install`。默认每天本地时间 **08:00** 执行；电脑需要开机，错过时间会在系统允许时补跑。Linux 可用 cron 调用固定路径的 `checkin run`。

### 从源码构建

需要 Go **1.23+**。第三方库仅用于系统浏览器控制的 chromedp 及其依赖，全部编译进二进制。

```bash
git clone https://github.com/qinqingwuwei/auto-checkin.git
cd auto-checkin
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o checkin ./cmd/checkin
./setup.sh
# Windows PowerShell：.\setup.ps1
```

setup 脚本优先使用仓库目录的现成二进制，也可通过 `CHECKIN_BINARY` 指定；否则调用本机 Go 构建，再安装到 `~/.checkin/bin/`。已有凭据会继续使用。

## 命令

| 命令 | 说明 |
| --- | --- |
| `run [--dry-run] [--force] [--only trae\|workbuddy]` | 签到 → 台账 → 到期汇总 → 通知 → 状态保存；`--only` 可重复 |
| `status [--json]` | 本地签到记录、凭据状态、最近积分快照 |
| `doctor [--offline]` | 配置、通知、定时、浏览器、凭据及只读网络检查 |
| `login trae [--channel chrome\|msedge\|chromium] [--timeout 480]` | 独立持久化浏览器窗口登录，捕获会话并验证换票 |
| `login workbuddy [--method browser\|sms] [--phone] [--code] [--timeout 300]` | OAuth 浏览器登录或短信登录，多账号独立保存 |
| `config show\|get\|set\|path` | 点路径配置，自动校验，密钥打码 |
| `schedule install\|uninstall\|status` | macOS launchd / Windows 任务计划程序 |
| `ui [--port 8765] [--no-open]` | 本地 Web 看板，端口占用时最多顺延 10 个 |
| `--version` / `-v` | 版本 / 调试日志；`-v` 放在命令前 |

`--force` 忽略本地成功日期，但仍先查询远端状态，今日已签时跳过 claim。`--dry-run` 不领取、不写 `state.json` 或 `points.jsonl`、不发通知；日志仍记录，凭据缓存可按需刷新。看板手动执行和定时任务通过系统文件锁互斥。

## 本地看板

```bash
~/.checkin/bin/checkin ui
```

看板包括产品卡片、积分曲线、配置表单、执行实时日志、定时任务和浅/深色主题。页面嵌入二进制，不依赖源码目录。仅监听 `127.0.0.1`，校验 Host；写接口要求 `X-Checkin: 1`，不开放 CORS。凭据只展示状态，密钥不以明文返回。

## 配置

默认 `~/.checkin/config.json`，可用 `CHECKIN_HOME`（或全局参数 `--home <目录>`）覆盖。

| 点路径 | 默认 | 说明 |
| --- | --- | --- |
| `schedule.time` | `08:00` | 每日时间，修改后重新 `schedule install` |
| `expiry_warning_days` | `7` | 到期预警天数，范围 0–365，0 关闭预警 |
| `notify.dingtalk.enabled` | `true` | 钉钉开关 |
| `notify.dingtalk.webhook` / `notify.dingtalk.secret` | 空 | 自定义机器人 webhook 与加签 secret |
| `notify.local.enabled` | `true` | macOS 系统通知开关 |
| `notify.daily_summary` | `false` | 每日成功简报，默认仅异常或到期预警通知 |
| `products.trae.enabled` / `products.workbuddy.enabled` | `true` | 产品开关 |

```bash
./checkin config show
./checkin config set schedule.time 07:30
./checkin schedule install
./checkin config set notify.dingtalk.webhook 'https://oapi.dingtalk.com/robot/send?access_token=...'
./checkin config set notify.dingtalk.secret 'SEC...'
```

未配置钉钉时 `doctor` 为 WARN，仍可签到，通知降级为本地通知和日志。Windows / Linux 当前使用日志与钉钉，无本地系统通知。

## 数据与兼容性

Go 版直接读写原有 `~/.checkin/`，无需迁移：`config.json`、`state.json`、`points.jsonl`、`logs/`、`credentials/trae.json`、`credentials/workbuddy/<uid>.json`。仍支持 `trae-web-state.json`、`workbuddy-sms.json`、`workbuddy-oauth.json` 遗留凭据的首次导入。凭据与配置为 0600，原子写入；日志每 2 MiB 轮转，保留 5 份。工具不写入任何产品客户端目录。

Trae 使用会话 `X-Cloudide-Session` 经 **POST** `GetUserToken` 换 JWT，业务请求为 `Authorization: Cloud-IDE-JWT <jwt>`，提前 10 分钟换票。WorkBuddy 使用 Bearer / X-User-Id，AT 不足 6 小时自动续期；业务 host 与插件 OAuth host 按原协议分别切换。

## 开发与发布

```bash
go test ./...
go test -race ./...
go vet ./...
./scripts/build-release.sh 0.2.0
```

GitHub Actions 在推送 main、创建面向 main 的 PR 或手动运行时，使用 Go 1.23.12 测试并交叉编译四个平台。构建完成后，在 Actions 对应运行的 Artifacts 下载 `checkin-binaries`；只有推送 `v*` tag 才会创建 GitHub Release。

发布脚本生成使用 ZIP 最高压缩级别的四平台 zip 和 `SHA256SUMS`，每个平台原始二进制须不超过 12 MiB。仓库仅提交源码、测试与构建脚本；本地 `.trae/` 工作资料、二进制、dist/ 和覆盖率输出均不提交。

## 风险与致谢

两侧接口来自社区公开实现，产品改版或风控可能使接口失效。WorkBuddy 规则限制通过脚本或接口领取活跃奖励，多账号也可能带来账号风险，请自行判断是否使用。凭据失效会明确提示重新登录。

接口参考：[workbuddy-auto-signin](https://github.com/88lin/workbuddy-auto-signin)、[WorkBuddy-Daily](https://github.com/L0NE-6/WorkBuddy-Daily)、[Trae-Checkin](https://github.com/ChenLun-dev/Trae-Checkin)、[TraeWorkAssistant](https://github.com/smart-open/TraeWorkAssistant)。

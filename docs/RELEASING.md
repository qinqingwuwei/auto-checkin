# 发布流程

发布产物为四个按版本、操作系统和 CPU 架构命名的独立 ZIP，以及 `SHA256SUMS`。每个 ZIP 直接包含二进制、README 和对应安装脚本；Go、Python、浏览器不随包分发。

## 1. 合并并验收

通过 PR 将改动合并到 main。工作流已不再对分支推送与 PR 自动构建，合并前请在本机跑通测试；Tag 推送后由发布工作流在 Windows / macOS / Linux 上执行测试、Linux race 检查与四平台构建。真实 Trae 登录、WorkBuddy 扫码 / 短信及退出程序行为需在正式发布前由账号本人验收。

```bash
git switch main
git pull --ff-only
go test ./...
go test -race ./...
go vet ./...
```

确认工作区干净，文档已更新，准备发布的提交已包含在远端 main。发布工作流会校验 Tag 版本格式及提交是否属于 main 的历史。

## 2. 本地检查产物

以下版本号仅为示例；每次发布使用新的版本号：

```bash
./scripts/validate-release-tag.sh v0.3.0
./scripts/build-release.sh 0.3.0
python3 scripts/verify-release.py dist/0.3.0 0.3.0
# Linux：cd dist/0.3.0 && sha256sum --check SHA256SUMS
# macOS：cd dist/0.3.0 && shasum -a 256 --check SHA256SUMS
```

预期产物：

```text
checkin-0.3.0-darwin-arm64.zip
checkin-0.3.0-darwin-amd64.zip
checkin-0.3.0-windows-amd64.zip
checkin-0.3.0-linux-amd64.zip
SHA256SUMS
```

在本机对应二进制上执行 `--version`，应显示 `checkin 0.3.0`。构建工具链固定为 Go 1.27.2；同一源码和工具链在同一构建环境下应生成相同 ZIP。脚本另外提供各包的 `.zip.sha256`，便于单独核验 Actions 下载项，正式 Release 使用汇总校验文件。

## 3. 创建新 Tag

```bash
git tag -a v0.3.0 -m "Release v0.3.0"
git push origin v0.3.0
```

Tag 推送后自动执行测试 → 四平台独立构建 → 校验包内容、体积、执行权限和 SHA256 → 创建草稿 Release 并上传全部文件 → 转为正式发布。`v0.3.0-rc.1` 等 Tag 走同一流程，最终标记为 Pre-release。

只有 Tag 推送会触发工作流，main 推送、PR 与手动运行都不再构建。Tag 对应二进制不带 `v` 前缀；本地源码构建显示 `dev`，便于辨认来源。

## 4. 下载并确认

在 GitHub Release 的 Assets 中应分别看到四个平台 ZIP 和 `SHA256SUMS`，另有 GitHub 自动生成的源码下载项。下载本机 ZIP，核对 SHA256，解压后确认可直接启动 WebUI、页面登录入口、定时开关与退出程序按钮。

Windows 默认只发布 x64；macOS 同时发布 Apple Silicon 和 Intel；Linux 默认发布 x64。不将不同平台的程序合并成一个用户安装包。

失败的发布不会把缺少产物的草稿公开；重跑可以续传未发布的同版本草稿。已经公开的版本不覆盖产物、不移动旧 Tag，修复后使用新版本号发布。

## 版本维护

行为新增或调整提升 minor，兼容修复提升 patch；不兼容接口改动按项目版本策略调整。发布版本由 Tag 注入，源码默认版本保持 `dev`。定期更新并固定受支持的 Go 工具链和 Actions 版本，升级后重新执行全部检查。

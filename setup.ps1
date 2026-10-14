# 使用 Release 二进制，或从源码构建；定时任务由 Go 内置实现。
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
$checkinRoot = if ($env:CHECKIN_HOME) { $env:CHECKIN_HOME } else { Join-Path $env:USERPROFILE ".checkin" }
if ($checkinRoot.StartsWith('~\') -or $checkinRoot.StartsWith('~/')) { $checkinRoot = Join-Path $env:USERPROFILE $checkinRoot.Substring(2) }
$checkinRoot = [IO.Path]::GetFullPath($checkinRoot)
$checkinBinDir = Join-Path $checkinRoot "bin"
New-Item -ItemType Directory -Force $checkinBinDir | Out-Null
$checkinBinary = Join-Path $checkinBinDir "checkin.exe"
$checkinSource = if ($env:CHECKIN_BINARY) { $env:CHECKIN_BINARY } else { Join-Path $PSScriptRoot "checkin.exe" }
if (Test-Path $checkinSource) {
  if ([IO.Path]::GetFullPath($checkinSource) -ne $checkinBinary) { Copy-Item -Force $checkinSource $checkinBinary }
} elseif (Get-Command go -ErrorAction SilentlyContinue) {
  $previousCGO = $env:CGO_ENABLED
  try {
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags '-s -w' -o $checkinBinary ./cmd/checkin
    if ($LASTEXITCODE -ne 0) { throw "Go 构建失败" }
  } finally { $env:CGO_ENABLED = $previousCGO }
} else {
  throw "请先从 https://github.com/qinqingwuwei/auto-checkin/releases 下载 checkin.exe，或安装 Go 1.23+。"
}
$env:CHECKIN_HOME = $checkinRoot
& $checkinBinary --version
Write-Host "安装完成：$checkinBinary"
Write-Host "看板：& '$checkinBinary' ui"
Write-Host "在看板中启用定时，退出程序即停止；不会安装系统后台任务。"

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
if (-not (Test-Path (Join-Path $checkinRoot "credentials\trae.json")) -and -not (Test-Path (Join-Path $checkinRoot "trae-web-state.json"))) {
  $checkinAnswer = Read-Host "Trae 未登录，按 Enter 打开浏览器登录（n 跳过）"
  if ($checkinAnswer -ne "n") { & $checkinBinary login trae }
}
$checkinAccounts = Get-ChildItem (Join-Path $checkinRoot "credentials\workbuddy") -Filter *.json -ErrorAction SilentlyContinue
if (-not $checkinAccounts -and -not (Test-Path (Join-Path $checkinRoot "workbuddy-sms.json")) -and -not (Test-Path (Join-Path $checkinRoot "workbuddy-oauth.json"))) {
  $checkinMethod = Read-Host "WorkBuddy 登录：1 浏览器扫码 / 2 短信 / 3 跳过 [3]"
  switch ($checkinMethod) {
    "1" { & $checkinBinary login workbuddy --method browser }
    "2" { & $checkinBinary login workbuddy --method sms }
  }
}
& $checkinBinary schedule install
if ($LASTEXITCODE -ne 0) { throw "定时任务安装失败" }
& $checkinBinary doctor
if ($LASTEXITCODE -ne 0) { throw "自检有异常，请处理后重试" }
& $checkinBinary run --dry-run
if ($LASTEXITCODE -ne 0) { throw "干跑验证失败" }
Write-Host "安装完成：$checkinBinary"
Write-Host "看板：& '$checkinBinary' ui"

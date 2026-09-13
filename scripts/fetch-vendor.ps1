# 获取并校验第三方引擎二进制（v1: 7-Zip linux-x64 静态版）
# 用法：pwsh -File scripts/fetch-vendor.ps1
$ErrorActionPreference = "Stop"

$root   = Split-Path -Parent $PSScriptRoot
$cache  = Join-Path $root "refs\vendor\_extract"           # 本地已解包缓存
$tarball = Join-Path $root "refs\vendor\7z2603-linux-x64.tar.xz"
$dest   = Join-Path $root "app\vendor\7zip\linux-x64"
$lock   = Join-Path $root "vendor.lock.json"

if (-not (Test-Path $cache)) {
  if (-not (Test-Path $tarball)) {
    throw "缺少 7-Zip 归档：$tarball（请先下载 7z2603-linux-x64.tar.xz 到 refs/vendor/）"
  }
  New-Item -ItemType Directory -Force -Path $cache | Out-Null
  tar -xJf $tarball -C $cache
}

New-Item -ItemType Directory -Force -Path $dest | Out-Null
foreach ($f in @("7zzs", "License.txt", "History.txt")) {
  $src = Join-Path $cache $f
  if (-not (Test-Path $src)) { throw "缓存中缺少 $f" }
  Copy-Item -LiteralPath $src -Destination $dest -Force
}

$sevenZip = Join-Path $dest "7zzs"
$sha = (Get-FileHash -LiteralPath $sevenZip -Algorithm SHA256).Hash.ToLower()
$size = (Get-Item -LiteralPath $sevenZip).Length

$lockObj = [ordered]@{
  "7zip" = [ordered]@{
    version = "26.03"
    arch = [ordered]@{
      "linux-x64" = [ordered]@{
        file    = "app/vendor/7zip/linux-x64/7zzs"
        sha256  = $sha
        bytes   = $size
        license = "app/vendor/7zip/linux-x64/License.txt"
      }
    }
  }
  generatedAt = (Get-Date).ToString("s")
}
$lockObj | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $lock -Encoding UTF8

Write-Host "vendor 就绪："
Write-Host ("  {0}  {1:N0} bytes  sha256={2}" -f "7zzs", $size, $sha)

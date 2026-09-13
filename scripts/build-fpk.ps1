# 构建 100zip 的 FPK（v1 仅 x86_64）
# 用法：pwsh -File scripts/build-fpk.ps1 [-Version 0.1.0] [-SkipVendor]
param(
  [string]$Version = "",
  [string]$Platform = "x86",
  [string]$Fnpack = "D:\codex\fnnas-fpk-research\bin\fnpack-1.2.1.exe",
  [string]$GoBin = "D:\codex\tools\go\bin\go.exe",
  [switch]$SkipVendor
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

# ---------- 0. 前置检查
if (-not (Test-Path $GoBin)) { throw "找不到 Go 工具链：$GoBin" }
if (-not (Test-Path $Fnpack)) { throw "找不到 fnpack：$Fnpack" }
if ($Platform -ne "x86") { throw "v1 仅支持 x86（arm64 预留）" }

$manifest = Join-Path $root "manifest"
$manifestText = Get-Content -Raw -LiteralPath $manifest
if (-not $Version) {
  $m = [regex]::Match($manifestText, '(?m)^version\s*=\s*(\S+)')
  if (-not $m.Success) { throw "manifest 缺少 version" }
  $Version = $m.Groups[1].Value
}

# ---------- 1. vendor
if (-not $SkipVendor) {
  & (Join-Path $PSScriptRoot "fetch-vendor.ps1")
}

# ---------- 2. 交叉编译 Linux 静态二进制
$binDir = Join-Path $root "app\bin"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$binOut = Join-Path $binDir "100zip"
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
Write-Host "编译 linux/amd64 ..."
& $GoBin build -trimpath -ldflags "-s -w -X main.version=$Version" -o $binOut (Join-Path $root "server")
if ($LASTEXITCODE -ne 0) { throw "Go 构建失败" }
Write-Host ("  二进制：{0:N1} MB" -f ((Get-Item $binOut).Length / 1MB))

# ---------- 3. staging
$stage = Join-Path $root "build\staging\100zip-$Platform"
if (Test-Path $stage) {
  $resolved = (Resolve-Path $stage).Path
  if (-not $resolved.StartsWith((Resolve-Path $root).Path)) { throw "拒绝清理仓库外的目录：$resolved" }
  Remove-Item -Recurse -Force -LiteralPath $stage
}
New-Item -ItemType Directory -Force -Path $stage | Out-Null

foreach ($item in @("manifest", "config", "cmd", "wizard", "app", "ICON.PNG", "ICON_256.PNG")) {
  $src = Join-Path $root $item
  if (-not (Test-Path $src)) { throw "缺少打包项：$item" }
  Copy-Item -LiteralPath $src -Destination $stage -Recurse -Force
}

# platform 重写（防御：manifest 若被改过）
$stagedManifest = Join-Path $stage "manifest"
(Get-Content -Raw -LiteralPath $stagedManifest) -replace '(?m)^platform\s*=.*$', "platform              = $Platform" |
  Set-Content -LiteralPath $stagedManifest -Encoding UTF8 -NoNewline

# ---------- 3.5 防回归：生命周期脚本必须 POSIX sh 兼容
# 平台以 sh(dash) 调用 cmd/*；bash 专有语法会导致脚本无法解析（真机事故：code 10500）
$bashisms = @(
  @{ Pattern = 'local\s+\w+\s*\('; Desc = 'bash 数组赋值（local x=(...)）' },
  @{ Pattern = '\[\[';             Desc = 'bash [[ ]] 条件' },
  @{ Pattern = 'declare\s+-';      Desc = 'bash declare' },
  @{ Pattern = '^\s*function\s+';  Desc = 'bash function 关键字' },
  @{ Pattern = '\$\{?\w+\[@\]\}?'; Desc = 'bash 数组展开（${arr[@]}）' }
)
foreach ($f in Get-ChildItem (Join-Path $stage "cmd") -File) {
  $text = Get-Content -Raw -LiteralPath $f.FullName
  $codeLines = ($text -split "`n") | Where-Object { $_ -notmatch '^\s*#' }
  foreach ($b in $bashisms) {
    foreach ($line in $codeLines) {
      if ($line -match $b.Pattern) {
        throw ("cmd/{0} 含 {1}，平台用 sh(dash) 调用会解析失败：{2}" -f $f.Name, $b.Desc, $line.Trim())
      }
    }
  }
  if ($text -match "`r") {
    throw ("cmd/{0} 含 CRLF 换行，Linux 下 shebang 会失效" -f $f.Name)
  }
}
Write-Host "  lifecycle 脚本检查通过（POSIX sh 兼容、LF 换行）"

# 权限位（fnpack 在 Windows 上不保留，由构建脚本在 Linux/CI 上补；此处仅确保可执行标记存在）
$exe = Join-Path $stage "app\bin\100zip"
Write-Host ("  staging 完成：{0}" -f $stage)

# ---------- 4. fnpack 打包（产物落在 cwd）
$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $dist
try {
  & $Fnpack build --directory $stage
  if ($LASTEXITCODE -ne 0) { throw "fnpack 打包失败" }
} finally {
  Pop-Location
}

$produced = Join-Path $dist "100zip.fpk"
if (-not (Test-Path $produced)) { throw "未找到产物：$produced" }

# ---------- 5. 修正 Unix 权限位（Windows 构建必需）
$env:GOOS = "windows"; $env:GOARCH = "amd64"
$fixer = Join-Path $root "tools\bin\fixmodes.exe"
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $fixer) | Out-Null
& $GoBin build -trimpath -o $fixer (Join-Path $root "tools\fixmodes")
if ($LASTEXITCODE -ne 0) { throw "fixmodes 构建失败" }
$fixed = Join-Path $dist "100zip-fixed.fpk"
& $fixer -in $produced -out $fixed
if ($LASTEXITCODE -ne 0) { throw "权限修正失败" }
Remove-Item -LiteralPath $produced -Force
Move-Item -LiteralPath $fixed -Destination $produced -Force

$finalName = "100zip_${Version}_x86_64.fpk"
$finalPath = Join-Path $dist $finalName
Move-Item -LiteralPath $produced -Destination $finalPath -Force
Write-Host ("打包完成：{0}  ({1:N0} KB)" -f $finalName, ((Get-Item $finalPath).Length / 1KB))

# 构建 Go 版的“控制台版”exe（前端 dist + 单文件 exe + 冒烟），供脚本 / CI 里运行并读取输出与退出码。
# 用户双击用的 GUI 版（无控制台窗口）请用 scripts/build-exe.ps1 -> dist/digital-employee.exe。
#
#   powershell -ExecutionPolicy Bypass -File scripts/build-go.ps1
#   powershell -ExecutionPolicy Bypass -File scripts/build-go.ps1 -SkipFrontend
#
# 前置：Go 1.27.3+、Bun 1.4+。本机若 GOSUMDB=off，需临时改为 sum.golang.org
# （否则 Go 拉不到 1.27.3 工具链）。
[CmdletBinding()]
param(
    [switch]$SkipFrontend,
    [string]$Out = "dist/digital-employee-console.exe"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

function Step($text) { Write-Host "`n== $text ==" -ForegroundColor Cyan }
function Fail($text) { Write-Host "[FAIL] $text" -ForegroundColor Red; exit 1 }

# Go 工具链：MyGo 依赖 go 1.27.3，本机默认 GOSUMDB=off 会挡住自动升级
$env:GOTOOLCHAIN = "auto"
if ($env:GOSUMDB -eq "off" -or -not $env:GOSUMDB) { $env:GOSUMDB = "sum.golang.org" }

$started = Get-Date

Step "1/3 检查前端构建产物"
$distIndex = Join-Path $root "packages/web/dist/index.html"
if ($SkipFrontend) {
    if (-not (Test-Path $distIndex)) { Fail "缺少 packages/web/dist（不能 -SkipFrontend）" }
    Write-Host "  [SKIP] 使用已有前端 dist"
} else {
    if (-not (Test-Path (Join-Path $root "node_modules"))) {
        Write-Host "  bun install ..."
        & bun install --frozen-lockfile
        if ($LASTEXITCODE -ne 0) { Fail "bun install 失败" }
    }
    Write-Host "  bun run --filter @de/web build ..."
    & bun run --filter '@de/web' build
    if ($LASTEXITCODE -ne 0) { Fail "前端构建失败" }
    if (-not (Test-Path $distIndex)) { Fail "前端构建后仍没有 dist/index.html" }
    Write-Host "  [PASS] 前端 dist 就绪"
}

Step "2/3 构建单文件 exe"
New-Item -ItemType Directory -Force -Path (Split-Path -Parent (Join-Path $root $Out)) | Out-Null
& go build -trimpath -ldflags "-s -w" -o $Out ./cmd/digital-employee
if ($LASTEXITCODE -ne 0) { Fail "go build 失败" }
$exe = Get-Item (Join-Path $root $Out)
Write-Host ("  [PASS] {0}（{1:N1} MB）" -f $exe.FullName, ($exe.Length / 1MB))

Step "3/3 冒烟（init + A-01 剧本模式）"
$ws = Join-Path ([System.IO.Path]::GetTempPath()) ("de-build-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
$env:DE_WORKSPACE = $ws
& $exe.FullName init | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "exe init 失败" }
& $exe.FullName run-case samples/cases/A-01.json --scripted samples/cases/scripts/A-01.json --out (Join-Path $ws "out")
if ($LASTEXITCODE -ne 0) { Fail "A-01 冒烟失败" }
$docx = Get-ChildItem -Path $ws -Recurse -Filter *.docx | Where-Object { $_.FullName -match 'deliverables' } | Select-Object -First 1
if (-not $docx) { Fail "没有产出 docx 交付物" }
Write-Host ("  [PASS] A-01 delivered，docx {0:N1} KB" -f ($docx.Length / 1KB))
Remove-Item -Recurse -Force $ws -ErrorAction SilentlyContinue

$elapsed = [math]::Round(((Get-Date) - $started).TotalSeconds, 1)
Write-Host ""
Write-Host "BUILD PASS  $Out（耗时 ${elapsed}s）" -ForegroundColor Green

# 一条命令打出用户双击的 exe：构建前端 -> go build（GUI 子系统，双击没有控制台窗口）-> 打印体积和 PE 子系统。
#
#   powershell -ExecutionPolicy Bypass -File scripts/build-exe.ps1
#   powershell -ExecutionPolicy Bypass -File scripts/build-exe.ps1 -SkipFrontend
#   powershell -ExecutionPolicy Bypass -File scripts/build-exe.ps1 -Version 0.2.0-go -Out dist/my.exe
#
# 产物：dist/digital-employee.exe（-H=windowsgui）。这个脚本不运行产出的 exe（运行会弹出窗口）。
# 需要在脚本里跑 exe 的冒烟 / CI 用控制台版：scripts/build-go.ps1 -> dist/digital-employee-console.exe。
#
# 图标和版本信息来自 cmd/digital-employee/resource_windows_amd64.syso（已入库，go build 自动链接）。
# 改了 cmd/digital-employee/winres/ 下的东西，要重新生成：
#   go run scripts/gen-icon.go                        # 只在换图标图案时
#   cd cmd/digital-employee; go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64 --out resource
[CmdletBinding()]
param(
    [switch]$SkipFrontend,
    [string]$Out = "dist/digital-employee.exe",
    # 版本号：缺省取 cmd/digital-employee/main.go 里 appVersion 的默认值
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

function Step($text) { Write-Host "`n== $text ==" -ForegroundColor Cyan }
function Fail($text) { Write-Host "[FAIL] $text" -ForegroundColor Red; exit 1 }

# 读 PE 头里的 Subsystem：2 = Windows GUI，3 = Windows 控制台。
function Get-PeSubsystem([string]$path) {
    $stream = [System.IO.File]::OpenRead($path)
    try {
        $reader = New-Object System.IO.BinaryReader($stream)
        $stream.Position = 0x3C
        $peOffset = $reader.ReadInt32()
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550) { throw "不是 PE 文件：$path" }
        # PE 签名(4) + COFF 头(20) = 可选头起点；Subsystem 在可选头偏移 68（PE32 与 PE32+ 相同）
        $stream.Position = $peOffset + 4 + 20 + 68
        return [int]$reader.ReadUInt16()
    } finally {
        $stream.Dispose()
    }
}

# Go 工具链：MyGo 依赖 go 1.27.3，本机默认 GOSUMDB=off 会挡住自动升级
$env:GOTOOLCHAIN = "auto"
if ($env:GOSUMDB -eq "off" -or -not $env:GOSUMDB) { $env:GOSUMDB = "sum.golang.org" }

$started = Get-Date

Step "1/3 前端构建产物"
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

Step "2/3 go build（-H=windowsgui）"
if ($Version -eq "") {
    $mainGo = Get-Content (Join-Path $root "cmd/digital-employee/main.go") -Raw -Encoding UTF8
    if ($mainGo -match 'var appVersion = "([^"]+)"') { $Version = $Matches[1] } else { Fail "读不到 main.go 里的 appVersion，请用 -Version 指定" }
}
$outPath = Join-Path $root $Out
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $outPath) | Out-Null
$ldflags = "-s -w -H=windowsgui -X main.appVersion=$Version -X digital-employee/internal/desktop.version=$Version"
Write-Host "  go build -trimpath -ldflags `"$ldflags`" -o $Out ./cmd/digital-employee"
& go build -trimpath -ldflags $ldflags -o $outPath ./cmd/digital-employee
if ($LASTEXITCODE -ne 0) { Fail "go build 失败" }

Step "3/3 检查产物"
$exe = Get-Item $outPath
$subsystem = Get-PeSubsystem $exe.FullName
$subsystemName = switch ($subsystem) { 2 { "Windows GUI" } 3 { "Windows 控制台" } default { "未知" } }
Write-Host ("  文件：{0}" -f $exe.FullName)
Write-Host ("  体积：{0:N1} MB（{1:N0} 字节）" -f ($exe.Length / 1MB), $exe.Length)
Write-Host ("  版本：{0}" -f $Version)
Write-Host ("  PE Subsystem：{0}（{1}）" -f $subsystem, $subsystemName)
if ($subsystem -ne 2) { Fail "PE Subsystem 不是 GUI(2)：双击会弹控制台窗口" }

$elapsed = [math]::Round(((Get-Date) - $started).TotalSeconds, 1)
Write-Host ""
Write-Host "BUILD PASS  $Out（耗时 ${elapsed}s）" -ForegroundColor Green
Write-Host "双击 $Out 直接打开窗口；命令行子命令的用法与限制见 README「启动软件」。" -ForegroundColor DarkGray

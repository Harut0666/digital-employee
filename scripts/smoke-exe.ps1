<#
  冒烟测试：在临时工作区里真的运行打包好的 exe。
    1) exe init         → 释放员工配置与公开样例、写默认 settings.json
    2) exe run-case     → 剧本模式把公开样例 A-01 跑到 delivered，断言 docx 交付物存在
    3) exe（服务端模式）→ 解析控制台打印的实际地址，确认接口、欢迎页样例与前端构建产物可用，然后关掉
  用法：powershell -ExecutionPolicy Bypass -File scripts/smoke-exe.ps1 [-Exe dist\digital-employee.exe]
  退出码：0 通过；非 0 失败（PowerShell 抛错即失败）。
#>
param(
  [string]$Exe = ""
)

$ErrorActionPreference = "Stop"
# 输出统一成 UTF-8，便于在 CI/终端里看中文（PowerShell 5.1 默认按控制台代码页输出）
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ($Exe -eq "") { $Exe = Join-Path $root "dist\digital-employee.exe" }
if (-not (Test-Path $Exe)) { throw "找不到 exe：$Exe（先跑 bun run build:exe）" }
$exe = (Resolve-Path $Exe).Path
$sizeMb = [math]::Round((Get-Item $exe).Length / 1MB, 1)

$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("de-smoke-" + [Guid]::NewGuid().ToString("N").Substring(0, 8))
$workspace = Join-Path $temp "workspace"
New-Item -ItemType Directory -Path $temp -Force | Out-Null

$env:DE_WORKSPACE = $workspace
$env:DE_NO_OPEN = "1"
Remove-Item Env:DE_SCRIPTED -ErrorAction SilentlyContinue

Write-Host "[1/3] exe $sizeMb MB，临时工作区 $temp"

# ---- 1. 初始化工作区 ----
$initOut = & $exe init
if ($LASTEXITCODE -ne 0) { throw "exe init 失败（退出码 $LASTEXITCODE）" }
Write-Host "      $initOut"

# ---- 2. 剧本模式跑 A-01 ----
$out = Join-Path $temp "out"
$case = Join-Path $workspace "samples\cases\A-01.json"
$script = Join-Path $workspace "samples\cases\scripts\A-01.json"
if (-not (Test-Path $case)) { throw "公开样例没有释放到工作区：$case" }
$sw = [System.Diagnostics.Stopwatch]::StartNew()
& $exe run-case $case --scripted $script --out $out | ForEach-Object { Write-Host "      $_" }
$runCode = $LASTEXITCODE
$sw.Stop()
if ($runCode -ne 0) { throw "exe run-case 失败（退出码 $runCode）" }

$result = Get-Content (Join-Path $out "result.json") -Raw -Encoding UTF8 | ConvertFrom-Json
if ($result.finalState -ne "delivered") { throw "A-01 终态应为 delivered，实际 $($result.finalState)" }
$docx = @($result.deliverables | Where-Object { $_.kind -eq "docx" }) | Select-Object -First 1
if (-not $docx) { throw "A-01 没有 docx 交付物" }
if (-not (Test-Path $docx.path)) { throw "交付物文件不存在：$($docx.path)" }
$docxKb = [math]::Round((Get-Item $docx.path).Length / 1KB, 1)
Write-Host "[2/3] A-01 跑到 delivered，用时 $([math]::Round($sw.Elapsed.TotalSeconds, 1))s，docx $docxKb KB"

# ---- 3. 服务端模式：接口 + 前端构建产物 ----
$log = Join-Path $temp "serve.log"
$errLog = Join-Path $temp "serve.err.log"
$env:DE_PORT = "18787"
$proc = Start-Process -FilePath $exe -PassThru -NoNewWindow -RedirectStandardOutput $log -RedirectStandardError $errLog
try {
  $url = $null
  for ($i = 0; $i -lt 60 -and -not $url; $i++) {
    Start-Sleep -Milliseconds 500
    if (Test-Path $log) {
      $m = Select-String -Path $log -Pattern "http://localhost:(\d+)" | Select-Object -First 1
      if ($m) { $url = "http://localhost:" + $m.Matches[0].Groups[1].Value }
    }
  }
  if (-not $url) { throw "服务端 30 秒内没有就绪（看 $log）" }

  $employees = @(Invoke-RestMethod -Uri "$url/api/employees" -TimeoutSec 15)
  if (-not ($employees | Where-Object { $_.id -eq "weekly-report" })) { throw "员工列表里没有 weekly-report" }
  $samples = Invoke-RestMethod -Uri "$url/api/samples" -TimeoutSec 15
  if (@($samples.samples).Count -lt 1) { throw "工作区里没有公开样例（欢迎页载入入口会不可用）" }
  $index = Invoke-WebRequest -Uri "$url/" -UseBasicParsing -TimeoutSec 15
  if ($index.StatusCode -ne 200) { throw "首页状态码 $($index.StatusCode)" }
  $asset = [regex]::Match($index.Content, 'assets/[^"]+\.js').Value
  if ($asset -eq "") { throw "首页里没有前端脚本引用（静态资源没有托管成功）" }
  $js = Invoke-WebRequest -Uri "$url/$asset" -UseBasicParsing -TimeoutSec 15
  if ($js.StatusCode -ne 200) { throw "前端脚本 $asset 状态码 $($js.StatusCode)" }
  Write-Host "[3/3] 服务端 $url 就绪：员工 $($employees.Count) 个、样例 $(@($samples.samples).Count) 个，首页与前端脚本可访问"
}
finally {
  if ($proc -and -not $proc.HasExited) { & taskkill /PID $proc.Id /T /F | Out-Null }
}

Remove-Item -Recurse -Force $temp
Write-Host "冒烟通过：exe 可初始化工作区、可跑通 A-01、可起服务端并提供界面"
exit 0

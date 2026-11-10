<#
  冒烟测试：在临时工作区里真的运行打包好的桌面版（Electrobun 窗口应用）。
    1) 启动 bin\launcher.exe（临时 DE_WORKSPACE + 固定 DE_PORT + 剧本模式 DE_SCRIPTED）
    2) 轮询 /api/employees 直到就绪（默认 60 秒，超时失败）
    3) 校验首页 200、前端脚本可访问、员工列表里有 weekly-report
    4) 通过 HTTP 载入公开样例 A-01 → submit → approve，轮询到 delivered，断言 docx 交付物存在且可下载
    5) 优雅关窗：PostMessage(WM_CLOSE) 关掉“迷你数字员工”窗口，轮询最多 15 秒断言
       端口不再监听、launcher/bun 子进程全部退出（验证 before-quit 收尾；超时则 taskkill 兜底并失败）
    6) 清理临时目录；失败时打印应用最后一段日志
  用法：
    powershell -ExecutionPolicy Bypass -File scripts/smoke-desktop.ps1
    powershell -ExecutionPolicy Bypass -File scripts/smoke-desktop.ps1 -AppPath "D:\somewhere\DigitalEmployee" -Port 8899
  退出码：0 通过；非 0 失败（PowerShell 抛错即失败）。
  说明：窗口是否真的显示、菜单/单实例/下载落盘属于人工目视项，不在本脚本的断言范围内。
#>
param(
  [string]$AppPath = "",
  [int]$Port = 8899,
  [int]$TimeoutSec = 60,
  [int]$TaskTimeoutSec = 120,
  [int]$CloseTimeoutSec = 15,
  [switch]$KeepTemp
)

$ErrorActionPreference = "Stop"
# 输出统一成 UTF-8，便于在 CI/终端里看中文（PowerShell 5.1 默认按控制台代码页输出）
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

# ---- 解析应用路径：接受绿色目录、dist\desktop、或 launcher.exe 本身 ----
function Resolve-Launcher([string]$path) {
  if ($path -eq "") { $path = Join-Path $root "dist\desktop" }
  if (-not (Test-Path $path)) {
    throw "找不到应用路径：$path（先跑 bun run build:desktop，或用 -AppPath 指定绿色目录）"
  }
  $item = Get-Item $path
  if ($item.PSIsContainer) {
    $candidates = @(
      (Join-Path $item.FullName "bin\launcher.exe"),
      (Join-Path $item.FullName "DigitalEmployee\bin\launcher.exe")
    )
    foreach ($candidate in $candidates) {
      if (Test-Path $candidate) { return (Resolve-Path $candidate).Path }
    }
    # dist\desktop 下如果只有便携 zip 而没有解压目录，给出明确提示
    $zip = Join-Path $item.FullName "win-x64-DigitalEmployee-portable.zip"
    if (Test-Path $zip) {
      throw "在 $($item.FullName) 下没有解压后的绿色目录，只有便携 zip：$zip。先解压它，或用 -AppPath 指向解压目录。"
    }
    throw "在 $($item.FullName) 下找不到 bin\launcher.exe（先跑 bun run build:desktop）"
  }
  if ($item.Name -ne "launcher.exe") {
    throw "-AppPath 应指向绿色目录、dist\desktop 或 bin\launcher.exe，实际是：$($item.FullName)"
  }
  return $item.FullName
}

# ---- 小工具：端口是否在监听；本脚本启动的、位于绿色目录下的进程 ----
function Test-PortListening([int]$targetPort) {
  $client = $null
  try {
    $client = New-Object System.Net.Sockets.TcpClient
    $client.Connect("127.0.0.1", $targetPort)
    return $true
  } catch {
    return $false
  } finally {
    if ($client) { try { $client.Close() } catch {} }
  }
}

function Get-AppProcesses {
  # 只算“本脚本启动之后、路径在这个绿色目录下”的进程，不误杀用户自己的实例
  try {
    return @(Get-Process | Where-Object {
        $_.Path -and $_.Path.StartsWith($appDir, [System.StringComparison]::OrdinalIgnoreCase) -and
        $_.StartTime -ge $started.AddSeconds(-5)
      })
  } catch {
    return @()
  }
}

$launcher = Resolve-Launcher $AppPath
$appDir = Split-Path (Split-Path $launcher -Parent) -Parent
$scripted = Join-Path $appDir "Resources\app\samples\cases\scripts\A-01.json"
if (-not (Test-Path $scripted)) {
  # 允许从仓库里直接拿剧本，便于只解压了安装包、没有带样例的调试场景
  $repoScripted = Join-Path $root "samples\cases\scripts\A-01.json"
  if (Test-Path $repoScripted) { $scripted = $repoScripted }
  else { throw "找不到 A-01 剧本文件：$scripted" }
}

# ---- 端口必须空闲：桌面版遇到占用会往后换端口，固定端口轮询会假失败 ----
if (Test-PortListening $Port) { throw "端口 $Port 已被占用：先关掉占用它的程序，或用 -Port 换一个端口" }

# ---- 临时工作区（绝不碰真实的 %APPDATA%\digital-employee）----
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("de-smoke-desktop-" + [Guid]::NewGuid().ToString("N").Substring(0, 8))
$workspace = Join-Path $temp "workspace"
New-Item -ItemType Directory -Path $temp -Force | Out-Null
$log = Join-Path $temp "app.out.log"
$errLog = Join-Path $temp "app.err.log"

# ---- 设置环境变量（结束时恢复原值，不污染当前 shell）----
$saved = @{}
foreach ($name in @("DE_WORKSPACE", "DE_PORT", "DE_SCRIPTED", "DE_NO_OPEN")) {
  $saved[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
$env:DE_WORKSPACE = $workspace
$env:DE_PORT = "$Port"
$env:DE_SCRIPTED = $scripted
$env:DE_NO_OPEN = "1"

$base = "http://127.0.0.1:$Port"
$proc = $null
$started = Get-Date
$step = "启动"

function Show-AppLog {
  Write-Host "      ---- 应用日志（$errLog）----"
  if (Test-Path $errLog) {
    Get-Content -Path $errLog -Tail 40 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "      $_" }
  }
  if (Test-Path $log) {
    Get-Content -Path $log -Tail 20 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "      $_" }
  }
}

function Wait-Ready {
  $deadline = (Get-Date).AddSeconds($TimeoutSec)
  while ((Get-Date) -lt $deadline) {
    if ($proc -and $proc.HasExited) {
      return $false
    }
    try {
      $resp = Invoke-WebRequest -Uri "$base/api/employees" -UseBasicParsing -TimeoutSec 2
      if ($resp.StatusCode -eq 200) { return $true }
    } catch {}
    Start-Sleep -Milliseconds 300
  }
  return $false
}

function Get-Snapshot([string]$id) {
  return Invoke-RestMethod -Uri "$base/api/tasks/$id" -TimeoutSec 10
}

function Wait-State([string]$id, [string[]]$states) {
  $deadline = (Get-Date).AddSeconds($TaskTimeoutSec)
  while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 400
    $snap = Get-Snapshot $id
    if ($states -contains $snap.state) { return $snap }
    if ($snap.pause -and ($states -notcontains $snap.state)) {
      # 暂停态不是期望的终态时直接失败，避免白等到超时
      if ($snap.state -ne "awaiting_review") {
        throw "任务 $id 停在 $($snap.state)（暂停原因 $($snap.pause.code)），期望 $($states -join ' / ')"
      }
      return $snap
    }
  }
  $last = Get-Snapshot $id
  throw "任务 $id 在 $TaskTimeoutSec 秒内没有到达 $($states -join ' / ')，最后状态 $($last.state)"
}

try {
  $step = "启动桌面应用"
  Write-Host "[1/6] 启动 $launcher"
  Write-Host "      临时工作区 $workspace，端口 $Port"
  $proc = Start-Process -FilePath $launcher -WorkingDirectory (Split-Path $launcher -Parent) -PassThru -NoNewWindow `
    -RedirectStandardOutput $log -RedirectStandardError $errLog
  $started = Get-Date

  $step = "等待接口就绪"
  if (-not (Wait-Ready)) {
    if ($proc.HasExited) { throw "应用启动后退出（退出码 $($proc.ExitCode)），$TimeoutSec 秒内没有就绪" }
    throw "应用 $TimeoutSec 秒内没有就绪（$base/api/employees 不可用）"
  }
  $readySeconds = [math]::Round(((Get-Date) - $started).TotalSeconds, 2)
  Write-Host "[2/6] 接口就绪，用时 ${readySeconds}s"

  $step = "校验界面与员工"
  Write-Host "[3/6] 校验首页、前端脚本与员工列表"
  $employees = @(Invoke-RestMethod -Uri "$base/api/employees" -TimeoutSec 15)
  if (-not ($employees | Where-Object { $_.id -eq "weekly-report" })) {
    throw "员工列表里没有 weekly-report（实际 $($employees.Count) 个）"
  }
  $index = Invoke-WebRequest -Uri "$base/" -UseBasicParsing -TimeoutSec 15
  if ($index.StatusCode -ne 200) { throw "首页状态码 $($index.StatusCode)" }
  $asset = [regex]::Match($index.Content, 'assets/[^"]+\.js').Value
  if ($asset -eq "") { throw "首页里没有前端脚本引用（静态资源没有托管成功）" }
  $js = Invoke-WebRequest -Uri "$base/$asset" -UseBasicParsing -TimeoutSec 15
  if ($js.StatusCode -ne 200) { throw "前端脚本 $asset 状态码 $($js.StatusCode)" }
  Write-Host "      首页 200、前端脚本 $asset 200、员工 $($employees.Count) 个"

  $step = "载入 A-01 并跑通"
  $taskWatch = [System.Diagnostics.Stopwatch]::StartNew()
  $headers = @{ "x-role" = "initiator" }
  $created = Invoke-RestMethod -Uri "$base/api/samples/scenario-a/tasks" -Method Post -Headers $headers -TimeoutSec 30
  if (-not $created.id) { throw "载入公开样例没有返回任务 id" }
  Write-Host "      任务 $($created.id) 已创建（草稿）"

  $submit = Invoke-RestMethod -Uri "$base/api/tasks/$($created.id)/commands" -Method Post -Headers $headers `
    -ContentType "application/json" -Body (ConvertTo-Json -Compress @{ type = "submit" }) -TimeoutSec 30
  if (-not $submit.ok) { throw "submit 被拒绝：$($submit.error) $($submit.message)" }

  $snap = Wait-State $created.id @("awaiting_review")
  if ($snap.state -ne "awaiting_review") { throw "提交后应停在 awaiting_review，实际 $($snap.state)" }
  Write-Host "      提交后停在待审核（质检已通过）"

  $reviewHeaders = @{ "x-role" = "reviewer" }
  $approve = Invoke-RestMethod -Uri "$base/api/tasks/$($created.id)/commands" -Method Post -Headers $reviewHeaders `
    -ContentType "application/json" -Body (ConvertTo-Json -Compress @{ type = "approve" }) -TimeoutSec 30
  if (-not $approve.ok) { throw "approve 被拒绝：$($approve.error) $($approve.message)" }

  $final = Wait-State $created.id @("delivered", "terminated")
  if ($final.state -ne "delivered") { throw "A-01 终态应为 delivered，实际 $($final.state)" }

  $docx = @($final.deliverables | Where-Object { $_.kind -eq "docx" }) | Select-Object -First 1
  if (-not $docx) { throw "A-01 没有 docx 交付物" }
  if (-not (Test-Path $docx.path)) { throw "交付物文件不存在：$($docx.path)" }
  $docxSize = (Get-Item $docx.path).Length
  if ($docxSize -le 0) { throw "交付物文件是空的：$($docx.path)" }

  # 再走一次 HTTP 下载，确认桌面窗口点“下载”时走的那条路可用
  $downloaded = Join-Path $temp "downloaded.docx"
  Invoke-WebRequest -Uri "$base/api/tasks/$($created.id)/deliverables/docx" -UseBasicParsing -OutFile $downloaded -TimeoutSec 30
  $downloadSize = (Get-Item $downloaded).Length
  if ($downloadSize -ne $docxSize) { throw "HTTP 下载的 docx 大小与交付物不一致（$downloadSize / $docxSize）" }
  $taskWatch.Stop()
  $taskSeconds = [math]::Round($taskWatch.Elapsed.TotalSeconds, 1)
  Write-Host "[4/6] A-01 跑到 delivered，用时 ${taskSeconds}s：docx $([math]::Round($docxSize / 1KB, 1))KB，HTTP 下载一致"

  # ---- 优雅关窗：验证 before-quit 收尾。taskkill 只能证明进程被杀，发现不了收尾回归 ----
  $step = "优雅关窗"
  if (-not ("DeDesktopWindow" -as [type])) {
    Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class DeDesktopWindow {
    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern IntPtr FindWindow(string lpClassName, string lpWindowName);
    [DllImport("user32.dll", SetLastError = true)]
    public static extern bool PostMessage(IntPtr hWnd, uint Msg, IntPtr wParam, IntPtr lParam);
}
"@
  }
  $windowTitle = "迷你数字员工"
  $hwnd = [IntPtr]::Zero
  $findDeadline = (Get-Date).AddSeconds($CloseTimeoutSec)
  while ((Get-Date) -lt $findDeadline -and $hwnd -eq [IntPtr]::Zero) {
    # 第一个参数必须是真正的 NULL：PowerShell 会把 $null 转成空字符串（空类名永远匹配不到），
    # 所以显式传 [NullString]::Value，等价于 C# 里的 FindWindow(null, "迷你数字员工")。
    $hwnd = [DeDesktopWindow]::FindWindow([NullString]::Value, $windowTitle)
    if ($hwnd -eq [IntPtr]::Zero) { Start-Sleep -Milliseconds 250 }
  }
  if ($hwnd -eq [IntPtr]::Zero) {
    throw "找不到标题为【$windowTitle】的窗口，无法验证优雅关窗（窗口是否已提前退出？看应用日志）"
  }
  Write-Host "[5/6] 关窗：向【$windowTitle】发送 WM_CLOSE，轮询端口 $Port 释放与子进程退出（最多 ${CloseTimeoutSec}s）"
  $WM_CLOSE = 0x0010
  [void][DeDesktopWindow]::PostMessage($hwnd, $WM_CLOSE, [IntPtr]::Zero, [IntPtr]::Zero)

  $closeWatch = [System.Diagnostics.Stopwatch]::StartNew()
  $closeDeadline = (Get-Date).AddSeconds($CloseTimeoutSec)
  $portFree = $false
  $launcherExited = $false
  $leftover = @()
  while ((Get-Date) -lt $closeDeadline) {
    $proc.Refresh()
    $portFree = -not (Test-PortListening $Port)
    $launcherExited = $proc.HasExited
    $leftover = @(Get-AppProcesses)
    if ($portFree -and $launcherExited -and $leftover.Count -eq 0) { break }
    Start-Sleep -Milliseconds 250
  }
  if (-not ($portFree -and $launcherExited -and $leftover.Count -eq 0)) {
    $reason = @()
    if (-not $portFree) { $reason += "端口 $Port 仍在监听" }
    if (-not $launcherExited) { $reason += "launcher.exe 没有退出" }
    if ($leftover.Count -gt 0) { $reason += "残留进程 $((($leftover | ForEach-Object { $_.ProcessName }) -join ', '))" }
    Show-AppLog
    throw "关窗后 ${CloseTimeoutSec} 秒内没有完成收尾（$($reason -join '；')）；已用 taskkill 兜底清理"
  }
  $closeSeconds = [math]::Round($closeWatch.Elapsed.TotalSeconds, 2)
  Write-Host "[5/6] 关窗后 ${closeSeconds}s 收尾完成：端口 $Port 释放、launcher 与 bun 子进程全部退出"
}
catch {
  Write-Host "[失败] $step：$($_.Exception.Message)" -ForegroundColor Red
  if ($proc -and -not $proc.HasExited) { Show-AppLog }
  throw
}
finally {
  # ---- 关进程树：launcher.exe 会拉子进程 bun.exe，必须整棵树结束 ----
  if ($proc -and -not $proc.HasExited) {
    & taskkill /PID $proc.Id /T /F 2>&1 | Out-Null
    try { Wait-Process -Id $proc.Id -Timeout 10 -ErrorAction SilentlyContinue } catch {}
  }
  # 兜底：只清理“本脚本启动之后、路径在这个绿色目录下”的残留进程，不误杀用户自己的实例
  try {
    Get-Process | Where-Object {
      $_.Path -and $_.Path.StartsWith($appDir, [System.StringComparison]::OrdinalIgnoreCase) -and
      $_.StartTime -ge $started.AddSeconds(-5)
    } | ForEach-Object { & taskkill /PID $_.Id /T /F 2>&1 | Out-Null }
  } catch {}

  foreach ($name in $saved.Keys) {
    if ($null -eq $saved[$name]) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
    else { Set-Item "Env:$name" $saved[$name] }
  }

  if ($KeepTemp) {
    Write-Host "      临时目录保留：$temp"
  } else {
    for ($i = 0; $i -lt 5; $i++) {
      try { Remove-Item -Recurse -Force $temp -ErrorAction Stop; break }
      catch { Start-Sleep -Milliseconds 500 }
    }
  }
}

Write-Host "[6/6] 冒烟通过：桌面版可启动、接口与界面可用、A-01 剧本跑到 delivered 且 docx 可下载、关窗后端口与子进程释放"
exit 0

<#
  冒烟测试（Go 版）：在临时工作区里跑单 exe，对标 scripts/smoke-exe.ps1。
    1) go build -o dist/digital-employee.exe ./cmd/digital-employee
    2) exe init → 释放员工配置与公开样例、写默认 settings.json
    3) exe run-case A-01 --scripted → 断言 finalState=delivered 且 docx 交付物存在
    4) exe --serve-only → 解析控制台里的实际端口，校验 /api/employees、/api/tasks、
       /、/assets/*.js，并用 SSE 收到至少 1 条事件，然后关进程
  用法：powershell -ExecutionPolicy Bypass -File scripts/smoke-go.ps1 [-Exe dist\digital-employee.exe] [-SkipBuild]
  退出码 = 失败项数（0 表示通过）
#>
param(
  [string]$Exe = "",
  [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
# 输出统一成 UTF-8，便于在 CI/终端里看中文（PowerShell 5.1 默认按控制台代码页输出）
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}
$env:GOSUMDB = "sum.golang.org"
$env:GOTOOLCHAIN = "auto"

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ($Exe -eq "") { $Exe = Join-Path $root "dist\digital-employee.exe" }
$Exe = [System.IO.Path]::GetFullPath($Exe)

$script:failures = 0
$script:proc = $null
$started = Get-Date

function Say([string]$text) { Write-Host "`n== $text ==" -ForegroundColor Cyan }
function Pass([string]$text) { Write-Host "  [PASS] $text" -ForegroundColor Green }
function Fail([string]$text) {
  $script:failures++
  Write-Host "  [FAIL] $text" -ForegroundColor Red
}

$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("de-go-smoke-" + [Guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Path $temp -Force | Out-Null
$workspace = Join-Path $temp "workspace"
$env:DE_WORKSPACE = $workspace
$env:DE_NO_OPEN = "1"
$env:DE_PORT = "18788"
Remove-Item Env:DE_SCRIPTED -ErrorAction SilentlyContinue

# 读 SSE 的第一帧：用原始 TCP 发 HTTP/1.1 请求，读到 "event: " 行为止。
# （PowerShell 5.1 里 HttpClient 流式读取容易踩 TPL 绑定的坑，裸 TCP 最稳）
function Read-SseFirstFrame([string]$sseUrl) {
  $client = $null
  $stream = $null
  try {
    $uri = [System.Uri]$sseUrl
    $client = New-Object System.Net.Sockets.TcpClient
    $client.Connect($uri.Host, $uri.Port)
    $stream = $client.GetStream()
    $stream.ReadTimeout = 10000
    $pathAndQuery = $uri.PathAndQuery
    $request = "GET $pathAndQuery HTTP/1.1`r`nHost: $($uri.Host):$($uri.Port)`r`nAccept: text/event-stream`r`nCache-Control: no-cache`r`nConnection: keep-alive`r`n`r`n"
    $bytes = [System.Text.Encoding]::ASCII.GetBytes($request)
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Flush()

    $buffer = New-Object byte[] 8192
    $builder = New-Object System.Text.StringBuilder
    $deadline = (Get-Date).AddSeconds(10)
    while ((Get-Date) -lt $deadline) {
      $read = 0
      try {
        $read = $stream.Read($buffer, 0, $buffer.Length)
      } catch {
        break
      }
      if ($read -le 0) { break }
      [void]$builder.Append([System.Text.Encoding]::UTF8.GetString($buffer, 0, $read))
      if ($builder.ToString() -match "event: ") { break }
    }
    $text = $builder.ToString()
    if ($text -match "HTTP/1\.[01] 200") { return $text }
    return $null
  } catch {
    return $null
  } finally {
    if ($stream) { $stream.Dispose() }
    if ($client) { $client.Close() }
  }
}

try {
  # ---- 1. 构建 ----
  Say "1/4 构建"
  if ($SkipBuild) {
    Pass "跳过构建（-SkipBuild）"
  } elseif (-not (Test-Path (Join-Path $root "go.mod"))) {
    Fail "找不到 go.mod（$root 不是 Go 工作树）"
  } else {
    Push-Location $root
    try {
      $buildOutput = & go build -o $Exe ./cmd/digital-employee 2>&1
      $buildCode = $LASTEXITCODE
    } finally {
      Pop-Location
    }
    if ($buildCode -ne 0) {
      Fail ("go build 失败（退出码 $buildCode）：`n" + ($buildOutput -join "`n"))
    } else {
      $sizeMb = [math]::Round((Get-Item $Exe).Length / 1MB, 1)
      Pass "go build → $Exe（$sizeMb MB）"
    }
  }

  if (-not (Test-Path $Exe)) {
    Fail "找不到 exe：$Exe（先 go build 或传 -Exe）"
  } else {
    # ---- 2. 初始化工作区 ----
    Say "2/4 exe init（释放员工配置/样例、写默认 settings.json）"
    $initOutput = & $Exe init 2>&1
    $initCode = $LASTEXITCODE
    if ($initCode -ne 0) {
      Fail ("exe init 失败（退出码 $initCode）：" + ($initOutput -join " "))
    } else {
      Pass ("exe init 输出：" + ($initOutput -join " "))
    }
    $caseFile = Join-Path $workspace "samples\cases\A-01.json"
    $scriptFile = Join-Path $workspace "samples\cases\scripts\A-01.json"
    $employeesDir = Join-Path $workspace "employees\weekly-report"
    if (-not (Test-Path $caseFile)) { Fail "公开样例没有释放到工作区：$caseFile" }
    if (-not (Test-Path $employeesDir)) { Fail "员工配置没有释放到工作区：$employeesDir" }
    $settingsFile = Join-Path $workspace "settings.json"
    if (-not (Test-Path $settingsFile)) { Fail "默认 settings.json 没有写入：$settingsFile" }

    # ---- 3. 剧本模式跑 A-01 ----
    Say "3/4 exe run-case A-01（剧本模式）"
    $out = Join-Path $temp "out"
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    if ((Test-Path $caseFile) -and (Test-Path $scriptFile)) {
      & $Exe run-case $caseFile --scripted $scriptFile --out $out | ForEach-Object { Write-Host "      $_" }
      $runCode = $LASTEXITCODE
      $sw.Stop()
      if ($runCode -ne 0) {
        Fail "exe run-case 失败（退出码 $runCode）"
      } else {
        $resultPath = Join-Path $out "result.json"
        if (-not (Test-Path $resultPath)) {
          Fail "缺少 $resultPath"
        } else {
          $result = Get-Content $resultPath -Raw -Encoding UTF8 | ConvertFrom-Json
          if ($result.finalState -ne "delivered") {
            Fail "A-01 终态应为 delivered，实际 $($result.finalState)"
          } else {
            Pass "A-01 delivered（用时 $([math]::Round($sw.Elapsed.TotalSeconds, 1))s，任务 $($result.taskId)）"
          }
          $docx = @($result.deliverables | Where-Object { $_.kind -eq "docx" }) | Select-Object -First 1
          if (-not $docx) {
            Fail "A-01 没有 docx 交付物"
          } elseif (-not (Test-Path $docx.path)) {
            Fail "交付物文件不存在：$($docx.path)"
          } else {
            $docxKb = [math]::Round((Get-Item $docx.path).Length / 1KB, 1)
            Pass "docx 交付物存在：$docxKb KB"
          }
        }
      }
    } else {
      Fail "缺少用例或剧本文件，跳过 run-case"
    }

    # ---- 4. 服务端模式 ----
    Say "4/4 exe --serve-only（接口 + 静态资源 + SSE）"
    $env:DE_SCRIPTED = $scriptFile
    $log = Join-Path $temp "serve.log"
    $errLog = Join-Path $temp "serve.err.log"
    $script:proc = Start-Process -FilePath $Exe -ArgumentList "--serve-only" -PassThru -NoNewWindow `
      -RedirectStandardOutput $log -RedirectStandardError $errLog
    $url = $null
    for ($i = 0; $i -lt 60 -and -not $url; $i++) {
      Start-Sleep -Milliseconds 500
      if (Test-Path $log) {
        $match = Select-String -Path $log -Pattern "http://localhost:(\d+)" | Select-Object -First 1
        if ($match) { $url = "http://localhost:" + $match.Matches[0].Groups[1].Value }
      }
    }
    if (-not $url) {
      Fail "服务端 30 秒内没有就绪（看 $log）"
    } else {
      Pass "服务端就绪：$url"

      # /api/employees
      try {
        $employees = @(Invoke-RestMethod -Uri "$url/api/employees" -TimeoutSec 15)
        if (-not ($employees | Where-Object { $_.id -eq "weekly-report" })) {
          Fail "员工列表里没有 weekly-report"
        } else {
          Pass "GET /api/employees（$($employees.Count) 个员工）"
        }
      } catch {
        Fail "GET /api/employees 失败：$($_.Exception.Message)"
      }

      # /api/tasks
      try {
        $tasks = Invoke-RestMethod -Uri "$url/api/tasks" -TimeoutSec 15
        Pass "GET /api/tasks（$(@($tasks).Count) 个任务）"
      } catch {
        Fail "GET /api/tasks 失败：$($_.Exception.Message)"
      }

      # / 与 /assets/*.js
      try {
        $index = Invoke-WebRequest -Uri "$url/" -UseBasicParsing -TimeoutSec 15
        if ($index.StatusCode -ne 200) {
          Fail "首页状态码 $($index.StatusCode)"
        } else {
          $asset = [regex]::Match($index.Content, 'assets/[^"]+\.js').Value
          if ($asset -eq "") {
            Fail "首页里没有前端脚本引用（静态资源没有托管成功）"
          } else {
            $js = Invoke-WebRequest -Uri "$url/$asset" -UseBasicParsing -TimeoutSec 15
            if ($js.StatusCode -ne 200) {
              Fail "前端脚本 $asset 状态码 $($js.StatusCode)"
            } else {
              Pass "GET / 与 $asset 可访问（脚本 $([math]::Round($js.RawContentLength / 1KB)) KB）"
            }
          }
        }
      } catch {
        Fail "首页/静态资源校验失败：$($_.Exception.Message)"
      }

      # SSE：建一个草稿任务，连 /stream 读第一帧（task_created）
      try {
        $created = Invoke-RestMethod -Uri "$url/api/tasks" -Method Post -TimeoutSec 15 `
          -Headers @{ "x-role" = "initiator" } -ContentType "application/json" `
          -Body '{"employee":"weekly-report","form":{"period":{"year":2026,"week":44}}}'
        if (-not $created.id) {
          Fail "建草稿任务没有返回 id"
        } else {
          $frame = Read-SseFirstFrame "$url/api/tasks/$($created.id)/stream?after=0"
          if (-not $frame) {
            Fail "SSE 没有收到任何事件"
          } elseif ($frame -notmatch "id: \d+") {
            Fail "SSE 帧缺少 id: 行：$frame"
          } elseif ($frame -notmatch "event: ") {
            Fail "SSE 帧缺少 event: 行：$frame"
          } else {
            $eventName = ([regex]::Match($frame, "event: (\S+)")).Groups[1].Value
            Pass "SSE 收到事件：$eventName"
          }
        }
      } catch {
        Fail "SSE 校验失败：$($_.Exception.Message)"
      }
    }
  }
} catch {
  Fail ("未捕获异常：" + $_.Exception.Message)
} finally {
  if ($script:proc -and -not $script:proc.HasExited) {
    & taskkill /PID $script:proc.Id /T /F | Out-Null
  }
  Remove-Item -Recurse -Force $temp -ErrorAction SilentlyContinue
}

$elapsed = [math]::Round(((Get-Date) - $started).TotalSeconds, 1)
Write-Host ""
if ($script:failures -eq 0) {
  Write-Host "SMOKE PASS  Go 版单 exe 可构建、可初始化工作区、可跑 A-01、可起服务端并提供界面与 SSE（耗时 ${elapsed}s）" -ForegroundColor Green
  exit 0
}
Write-Host "SMOKE FAIL  $($script:failures) 项失败（耗时 ${elapsed}s）" -ForegroundColor Red
exit $script:failures

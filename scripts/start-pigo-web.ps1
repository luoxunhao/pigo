param(
  [switch]$Stop
)

$ErrorActionPreference = "Stop"
$PiWeb = "E:\project\pi-web"
$PigoExe = "E:\project\pigo\pigo-accept.exe"
$PidDir = Join-Path $env:TEMP "pi-web-pigo"
$PidFile = Join-Path $PidDir "pids.txt"

if ($Stop) {
  if (-not (Test-Path $PidFile)) {
    Write-Host "No running record found. Start pi-web first."
    exit 0
  }
  $pids = Get-Content $PidFile | ForEach-Object { [int]$_ }
  foreach ($pidValue in $pids) {
    if (Get-Process -Id $pidValue -ErrorAction SilentlyContinue) {
      taskkill.exe /PID $pidValue /T /F 2>$null | Out-Null
    }
  }
  Remove-Item -LiteralPath $PidFile -ErrorAction SilentlyContinue
  Write-Host "Stopped pi-web (sessiond/web/vite)."
  exit 0
}

New-Item -ItemType Directory -Force -Path $PidDir | Out-Null

if (-not (Test-Path $PiWeb)) {
  Write-Error "pi-web directory missing: $PiWeb"
}
if (-not (Test-Path $PigoExe)) {
  Write-Error "pigo exe missing. Build first: cd E:\project\pigo; go build -o pigo-accept.exe ./cmd/pigo"
}

$ConfigFile = Join-Path $PidDir "pi-web-config.json"
$configJson = @{
  agentBackend = "pigo"
  pigo = @{
    command = $PigoExe
    args    = @("--acp")
  }
} | ConvertTo-Json -Depth 5
[System.IO.File]::WriteAllText($ConfigFile, $configJson, (New-Object System.Text.UTF8Encoding($false)))

$env:PI_WEB_CONFIG = $ConfigFile
$env:PI_WEB_SESSIOND_PORT = "8599"
$env:PI_WEB_SESSIOND_HOST = "127.0.0.1"
$env:PI_WEB_SESSIOND_URL = "http://127.0.0.1:8599"
$env:PI_WEB_PORT = "8504"

$sessiondOut = Join-Path $PidDir "sessiond.out.log"
$sessiondErr = Join-Path $PidDir "sessiond.err.log"
$webOut = Join-Path $PidDir "web.out.log"
$webErr = Join-Path $PidDir "web.err.log"
$viteOut = Join-Path $PidDir "vite.out.log"
$viteErr = Join-Path $PidDir "vite.err.log"

function Start-HiddenCmd {
  param(
    [string]$CommandLine,
    [string]$OutFile,
    [string]$ErrFile
  )
  $psi = New-Object System.Diagnostics.ProcessStartInfo
  $psi.FileName = "cmd.exe"
  $psi.Arguments = "/c `"$CommandLine > `"$OutFile`" 2> `"$ErrFile`"`""
  $psi.WorkingDirectory = $PiWeb
  $psi.UseShellExecute = $true
  $psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
  $process = [System.Diagnostics.Process]::Start($psi)
  return $process.Id
}

Write-Host "Starting sessiond (8599) ..."
$sessiondId = Start-HiddenCmd "npx tsx src/server/sessiond.ts" $sessiondOut $sessiondErr

Start-Sleep -Seconds 3

Write-Host "Starting web gateway (8504) ..."
$webId = Start-HiddenCmd "npx tsx src/server/index.ts" $webOut $webErr

Write-Host "Starting Vite frontend (8505) ..."
$viteId = Start-HiddenCmd "npm run dev:client" $viteOut $viteErr

@($sessiondId, $webId, $viteId) | Set-Content -Encoding UTF8 -Path $PidFile

$sessiondReady = $false
$webReady = $false
for ($i = 0; $i -lt 60; $i++) {
  if (-not $sessiondReady) {
    try {
      $null = Invoke-RestMethod -Uri "http://127.0.0.1:8599/health" -TimeoutSec 2
      $sessiondReady = $true
    } catch {}
  }
  if (-not $webReady) {
    try {
      $null = Invoke-RestMethod -Uri "http://127.0.0.1:8504/api/machines/local/pigo/config" -TimeoutSec 2
      $webReady = $true
    } catch {}
  }
  if ($sessiondReady -and $webReady) { break }
  Start-Sleep -Seconds 1
}

Write-Host ""
Write-Host "sessiond: $sessiondReady  web: $webReady"
Write-Host "Frontend: http://localhost:8505"
Write-Host "Logs: $PidDir"
Write-Host "Stop: powershell -ExecutionPolicy Bypass -File E:\project\pigo\scripts\start-pigo-web.ps1 -Stop"

Start-Process "http://localhost:8505"

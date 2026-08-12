param(
    [string]$PigoHome = $env:PIGO_HOME
)

if (-not $PigoHome) {
    $PigoHome = Join-Path $HOME ".pigo"
}
$PigoHome = [System.IO.Path]::GetFullPath($PigoHome)
$legacyRoot = Join-Path $PigoHome "legacy-sessions"
New-Item -ItemType Directory -Force -Path $legacyRoot | Out-Null

$targets = @()
$rootSessions = Join-Path $PigoHome "sessions"
if (Test-Path -LiteralPath $rootSessions) {
    $targets += [pscustomobject]@{ Path = $rootSessions; Name = "sessions" }
}
$projects = Join-Path $PigoHome "projects"
if (Test-Path -LiteralPath $projects) {
    Get-ChildItem -LiteralPath $projects -Directory | ForEach-Object {
        $sessions = Join-Path $_.FullName "sessions"
        if (Test-Path -LiteralPath $sessions) {
            $targets += [pscustomobject]@{ Path = $sessions; Name = "projects-$($_.Name)-sessions" }
        }
    }
}

foreach ($target in $targets) {
    $dest = Join-Path $legacyRoot $target.Name
    if (Test-Path -LiteralPath $dest) {
        Write-Host "skip $($target.Path) -> $dest (already exists)"
        continue
    }
    Move-Item -LiteralPath $target.Path -Destination $dest
    Write-Host "moved $($target.Path) -> $dest"
}

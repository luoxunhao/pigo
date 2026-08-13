$ErrorActionPreference = 'Stop'

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..\..')
Push-Location $repoRoot
try {
    go test ./internal/contextbuild -run TestParity -update
    if ($LASTEXITCODE -ne 0) {
        throw "parity fixture refresh failed (exit $LASTEXITCODE)"
    }
    go test ./internal/contextbuild -run TestParity
    if ($LASTEXITCODE -ne 0) {
        throw "parity fixture verification failed (exit $LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

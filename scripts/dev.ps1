$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    & go run ./tools/dev @args
    if ($LASTEXITCODE -ne 0) { throw "Workcopy task failed with exit code $LASTEXITCODE" }
}
finally { Pop-Location }

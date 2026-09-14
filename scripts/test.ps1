$ProjectPath = Split-Path -Parent $PSScriptRoot

Write-Output "Project path (PowerShell): $ProjectPath"
Write-Output "Ejecutando tests en Docker..."

docker run --rm `
    -v "${ProjectPath}:/app" `
    -w /app `
    golang:1.24-bookworm `
    go test -v ./...

if ($LASTEXITCODE -ne 0) {
    Write-Error "tests fallaron"
    exit 1
} else {
    Write-Output "tests completados"
}

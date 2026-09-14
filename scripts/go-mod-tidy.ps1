$ProjectPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory"

Write-Output "Ejecutando go mod tidy en $ProjectPath"

docker run --rm `
    -v "${ProjectPath}:/app" `
    -w /app `
    golang:1.24-bookworm `
    go mod tidy

if ($LASTEXITCODE -ne 0) {
    Write-Error "go mod tidy falló"
    exit 1
}

Write-Output "go mod tidy completado"

# verificar que se generó go.sum
if (Test-Path "$ProjectPath\go.sum") {
    Write-Output "go.sum generado:"
    Get-Item "$ProjectPath\go.sum" | Select-Object Name, Length, LastWriteTime
} else {
    Write-Error "go.sum no se generó"
    exit 1
}

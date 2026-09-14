$ProjectPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory"
$OutputPath = "$ProjectPath\tmp\clipfactory-linux-amd64"

Write-Output "Compilando ClipFactory para Linux amd64..."
Write-Output "Directorio de salida: $OutputPath"

# crear directorio de salida si no existe
if (-not (Test-Path $OutputPath)) {
    New-Item -ItemType Directory -Path $OutputPath -Force | Out-Null
}

docker run --rm `
    -v "${ProjectPath}:/app" `
    -w /app `
    golang:1.24-bookworm `
    bash -c "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/tmp/clipfactory-linux-amd64 ./cmd/clipfactory"

if ($LASTEXITCODE -ne 0) {
    Write-Error "compilación falló"
    exit 1
}

Write-Output "compilación completada"

# verificar que el binario existe
# el binario se guarda en $OutputPath\clipfactory (dentro del directorio)
$BinaryPath = "$OutputPath\clipfactory"
if (Test-Path $BinaryPath) {
    $bin = Get-Item $BinaryPath
    Write-Output "binario generado:"
    Write-Output "  nombre: $($bin.Name)"
    Write-Output "  tamaño: $($bin.Length) bytes"
    Write-Output "  fecha: $($bin.LastWriteTime)"
} else {
    Write-Error "binario no se generó en $BinaryPath"
    exit 1
}

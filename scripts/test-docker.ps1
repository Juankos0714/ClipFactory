$ProjectPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory"

Write-Output "Project path (PowerShell): $ProjectPath"
Write-Output "Test docker run..."

docker run --rm `
    -v "${ProjectPath}:/app" `
    -w /app `
    golang:1.24-bookworm `
    bash -c "echo 'Hola desde Docker'; ls -la /app"

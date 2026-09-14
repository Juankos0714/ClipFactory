# scripts/debug-args.ps1
# Script de depuración para verificar cómo se pasan los argumentos al script run-cli.ps1

param(
    [string[]]$Args = @()
)

Write-Output "=== DEBUG ARGS ==="
Write-Output "Args recibidos: $($Args -join ', ')"
Write-Output "Tipo de Args: $($Args.GetType().Name)"
Write-Output "Cantidad de elementos: $($Args.Count)"

# ejecutar el script run-cli.ps1 con los mismos argumentos
$ScriptPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory\scripts\run-cli.ps1"

Write-Output "Ejecutando: $ScriptPath -Args $($Args -join ', ')"
& $ScriptPath -Args $Args

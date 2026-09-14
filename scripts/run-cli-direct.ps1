# scripts/run-cli-direct.ps1
# Script de PowerShell que ejecuta el comando de forma correcta

param(
    [string[]]$CliArgs = @()
)

$ProjectPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory"
$ScriptPath = "$ProjectPath\scripts\run-cli.ps1"

Write-Output "ejecutando script run-cli.ps1 con argumentos: $($CliArgs -join ' ')"

# ejecutar el script con los argumentos
# usar la sintaxis correcta para pasar los argumentos al script

$psArgs = @()
$psArgs += "-File"
$psArgs += $ScriptPath
$psArgs += "-Args"

# agregar los argumentos de CLI como parámetros separados
$psArgs += $CliArgs

Write-Output "powershell command: powershell -NoProfile $($psArgs -join ' ')"

powershell -NoProfile $psArgs
exit $LASTEXITCODE

# scripts/debug-run.ps1
# Script de PowerShell que ejecuta el script de depuración debug-args.ps1 correctamente

# ejecutar el script debug-args.ps1 con el argumento 'help'
$ScriptPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory\scripts\debug-args.ps1"

Write-Output "Ejecutando script de depuración..."

# usar la sintaxis correcta para ejecutar el script con argumentos
# desde un script de PowerShell, podemos usar & directamente

& $ScriptPath -Args @('help')

Write-Output "Fin de la depuración"

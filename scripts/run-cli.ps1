param(
    [string[]]$CliArgs = @()
)

$ProjectPath = "C:\Users\jucar\OneDrive\Documentos\SoftwareDevelopment\ClipFactory"

# ruta del binario dentro del contenedor (ahora en /opt/clipfactory/bin/clipfactory)
$UnixBinaryPath = "/opt/clipfactory/bin/clipfactory"

Write-Output "ejecutando ClipFactory con argumentos: $($CliArgs -join ' ')"

# usar docker compose run para ejecutar el binario dentro del contenedor de desarrollo
# la sintaxis es: docker compose run --rm SERVICE COMMAND ARG1 ARG2 ...
# donde SERVICE es el nombre del servicio, COMMAND es el comando a ejecutar, y ARG1 ARG2 son los argumentos al comando

$dockerComposeArgs = @(
    "run",
    "--rm",
    "clipfactory-dev",
    $UnixBinaryPath
)

# agregar los argumentos de CLI como argumentos adicionales al comando
$dockerComposeArgs += $CliArgs

Write-Output "docker compose command: $($dockerComposeArgs -join ' ')"

docker compose @dockerComposeArgs
exit $LASTEXITCODE

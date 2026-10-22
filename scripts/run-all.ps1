# Levantar ClipFactory con Docker (solo Kick) — un comando
#
# Uso:
#   .\scripts\run-all.ps1
#
# Este script:
#  1. Construye y levanta el contenedor de desarrollo (clipfactory-dev)
#  2. Construye y levanta el worker (clipfactory) que procesa la cola de jobs
#  3. Construye y levanta la API REST (clipfactory-server) en el puerto 8080
#
# Requisitos: Docker y Docker Compose instalados.
# Credenciales: Kick no requiere credenciales (endpoints públicos).
# Los archivos de config y credenciales se montan por volumen desde ./config y ./credentials.

$ErrorActionPreference = "Stop"

$ProjectPath = Split-Path -Parent $PSScriptRoot

Write-Output "========================================"
Write-Output "ClipFactory — Levantando con Docker"
Write-Output "========================================"
Write-Output "Ruta del proyecto: $ProjectPath"
Write-Output ""

# Verificar que docker compose esté disponible
if (!(Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Error "Docker no está instalado o no está en el PATH."
    exit 1
}

if (!(docker compose version 2>$null)) {
    Write-Error "Docker Compose no está disponible."
    exit 1
}

# Levantar todos los servicios (dev + worker + server)
Write-Output "[1/3] Construyendo y levantando servicios..."
docker compose up -d --build

if ($LASTEXITCODE -ne 0) {
    Write-Error "Error al levantar los contenedores."
    exit 1
}

Write-Output ""
Write-Output "[2/3] Esperando a que la API esté lista..."
Start-Sleep -Seconds 5

# Verificar salud de la API
$health = Invoke-RestMethod -Uri "http://localhost:8080/api/health" -Method Get -ErrorAction SilentlyContinue
if ($health) {
    Write-Output "API REST responde en http://localhost:8080/api/health"
} else {
    Write-Output "Aviso: la API aún no responde (puede tardar en compilar la primera vez)."
}

Write-Output ""
Write-Output "[3/3] Estado de los contenedores:"
docker compose ps

Write-Output ""
Write-Output "========================================"
Write-Output "Listo. Comandos útiles:"
Write-Output "  .\scripts\run-cli.ps1 status    # ver estado del sistema"
Write-Output "  .\scripts\run-cli.ps1 discovery # forzar pasada de discovery (Kick)"
Write-Output "  docker compose logs -f clipfactory  # ver logs del worker"
Write-Output "  docker compose down             # parar todo"
Write-Output "========================================"

# Levanta todo el stack (Postgres, Redis, runtime, backend, frontend) tras reiniciar la PC.
# Uso: doble clic en start.bat, o  .\start.ps1
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

function Test-Docker { docker info *> $null; return ($LASTEXITCODE -eq 0) }

if (-not (Test-Docker)) {
    Write-Host 'Docker no responde. Iniciando Docker Desktop...'
    $exe = "$env:ProgramFiles\Docker\Docker\Docker Desktop.exe"
    if (-not (Test-Path $exe)) { Write-Error 'No encuentro Docker Desktop. Instalalo o abrelo manualmente.'; exit 1 }
    Start-Process $exe
    $deadline = (Get-Date).AddMinutes(4)
    while (-not (Test-Docker)) {
        if ((Get-Date) -gt $deadline) { Write-Error 'Docker no arranco en 4 minutos.'; exit 1 }
        Start-Sleep 5
    }
}
Write-Host 'Docker listo. Construyendo y levantando servicios...'

if ((Test-Path .env.example) -and -not (Test-Path .env)) { Copy-Item .env.example .env }

docker compose up -d --build
if ($LASTEXITCODE -ne 0) { Write-Error 'docker compose fallo.'; exit 1 }

Write-Host 'Esperando al backend...'
$ok = $false
for ($i = 0; $i -lt 60; $i++) {
    try { Invoke-RestMethod http://localhost:8080/api/v1/healthz -TimeoutSec 2 | Out-Null; $ok = $true; break } catch { Start-Sleep 2 }
}
if (-not $ok) { Write-Warning 'El backend no respondio. Revisa: docker compose logs backend'; exit 1 }

Write-Host 'Todo arriba:  http://localhost:3000   (API: http://localhost:8080)'
Start-Process 'http://localhost:3000'

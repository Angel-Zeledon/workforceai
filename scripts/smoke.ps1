# Smoke test del escenario de $50,000 (PowerShell 5.1+ / 7+).
# Uso: .\scripts\smoke.ps1   (parametros: -ApiUrl -RuntimeUrl -TimeoutSec)
param(
  [string]$ApiUrl = $(if ($env:API_URL) { $env:API_URL } else { "http://localhost:8080/api/v1" }),
  [string]$RuntimeUrl = $(if ($env:RUNTIME_URL) { $env:RUNTIME_URL } else { "http://localhost:8000" }),
  [int]$TimeoutSec = 180
)
$ErrorActionPreference = "Stop"
$Text = 'Un cliente pidió una propuesta de $50,000. Prepara la propuesta, revisa el contrato, el margen, la rentabilidad y la capacidad operativa.'
$script:Pass = 0; $script:Fail = 0

function Ok($m)  { Write-Host "  [PASS] $m" -ForegroundColor Green; $script:Pass++ }
function Bad($m) { Write-Host "  [FAIL] $m" -ForegroundColor Red; $script:Fail++ }
function Finish {
  Write-Host ""
  if ($script:Fail -eq 0) { Write-Host "SMOKE: PASS ($($script:Pass) checks)" -ForegroundColor Green; exit 0 }
  Write-Host "SMOKE: FAIL ($($script:Fail) fallos, $($script:Pass) ok)" -ForegroundColor Red; exit 1
}
function Die($m) { Bad $m; Finish }

function Wait-Health($name, $url) {
  for ($i = 0; $i -lt 60; $i++) {
    try { Invoke-RestMethod -Uri $url -TimeoutSec 3 | Out-Null; Ok "$name healthz ($url)"; return $true }
    catch { Start-Sleep -Seconds 2 }
  }
  return $false
}

Write-Host "== Smoke AI Workforce OS =="
if (-not (Wait-Health "backend" "$ApiUrl/healthz")) { Die "backend no responde en $ApiUrl/healthz" }
if (-not (Wait-Health "agent-runtime" "$RuntimeUrl/healthz")) { Die "runtime no responde en $RuntimeUrl/healthz" }

try { Invoke-RestMethod -Method Post -Uri "$ApiUrl/demo/reset" | Out-Null; Ok "demo/reset" }
catch { Write-Host "  [warn] demo/reset no disponible" }

try {
  $body = @{ text = $Text } | ConvertTo-Json
  $resp = Invoke-RestMethod -Method Post -Uri "$ApiUrl/requests" -ContentType "application/json; charset=utf-8" -Body ([Text.Encoding]::UTF8.GetBytes($body))
} catch { Die "POST /requests fallo: $_" }
$reqId = $resp.request_id
if (-not $reqId) { Die "POST /requests sin request_id" }
Ok "request creada: $reqId"

$approved = 0
$deadline = (Get-Date).AddSeconds($TimeoutSec)
$status = ""
while ((Get-Date) -lt $deadline) {
  $pending = @(Invoke-RestMethod -Uri "$ApiUrl/approvals?status=pending")
  foreach ($a in $pending) {
    if ($null -eq $a) { continue }
    try {
      Invoke-RestMethod -Method Post -Uri "$ApiUrl/approvals/$($a.id)/decision" -ContentType "application/json" -Body '{"decision":"approve","note":"smoke"}' | Out-Null
      $approved++; Write-Host "  .. aprobada $($a.id)"
    } catch { }
  }
  $status = (Invoke-RestMethod -Uri "$ApiUrl/requests/$reqId").status
  if ($status -eq "done") { break }
  if ($status -eq "failed") { Die "request termino en failed" }
  Start-Sleep -Seconds 1
}

if ($approved -ge 1) { Ok "aprobaciones resueltas: $approved" } else { Bad "nunca aparecio una aprobacion" }
if ($status -eq "done") { Ok "request done" } else { Die "timeout (${TimeoutSec}s) esperando request done (estado: $status)" }

$req = Invoke-RestMethod -Uri "$ApiUrl/requests/$reqId"
$ok = $false
if ($req.report_id) {
  try { Invoke-RestMethod -Uri "$ApiUrl/reports/$($req.report_id)" | Out-Null; $ok = $true } catch { }
}
if ($ok) { Ok "report existe: $($req.report_id)" } else { Bad "no existe report para la request" }

$activity = Invoke-RestMethod -Uri "$ApiUrl/activity?limit=500"
$n = @($activity | ForEach-Object { $_ } | ForEach-Object { $_.agent_id } | Where-Object { $_ } | Sort-Object -Unique).Count
if ($n -ge 5) { Ok "activity con $n agentes distintos (>=5)" } else { Bad "activity con solo $n agentes distintos (se requieren 5)" }

Finish

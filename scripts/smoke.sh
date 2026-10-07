#!/usr/bin/env bash
# Smoke test del escenario de $50,000. Requiere: curl y (jq o python3).
# Uso: scripts/smoke.sh   (variables: API_URL, RUNTIME_URL, TIMEOUT)
set -u

API_URL="${API_URL:-http://localhost:8080/api/v1}"
BACKEND_HEALTH="${BACKEND_HEALTH:-${API_URL}/healthz}"
RUNTIME_URL="${RUNTIME_URL:-http://localhost:8000}"
TIMEOUT="${TIMEOUT:-180}"
TEXT='Un cliente pidió una propuesta de $50,000. Prepara la propuesta, revisa el contrato, el margen, la rentabilidad y la capacidad operativa.'

PASS=0; FAIL=0
ok()   { echo "  [PASS] $1"; PASS=$((PASS+1)); }
bad()  { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }
finish() {
  echo
  if [ "$FAIL" -eq 0 ]; then echo "SMOKE: PASS ($PASS checks)"; exit 0; fi
  echo "SMOKE: FAIL ($FAIL fallos, $PASS ok)"; exit 1
}
die() { bad "$1"; finish; }

if command -v jq >/dev/null 2>&1; then
  jget() { jq -r "$1" 2>/dev/null; }
else
  # Fallback mínimo con python3: soporta .a.b, .[]?.x y length en estas consultas.
  jget() { python3 -c "
import sys, json
q = sys.argv[1]
try: d = json.load(sys.stdin)
except Exception: print(''); sys.exit()
if q == 'len': print(len(d or []))
elif q == 'ids': print('\n'.join(x['id'] for x in (d or [])))
elif q == 'agents': print(len(set(x.get('agent_id') for x in (d or []) if x.get('agent_id'))))
else:
    for k in q.split('.'):
        if k: d = (d or {}).get(k)
    print('' if d is None else d)
" "$1"; }
  JQ_MODE=py
fi

# Helpers que abstraen jq/python
field()   { if [ -z "${JQ_MODE:-}" ]; then jq -r "$1 // empty" 2>/dev/null; else jget "$2"; fi; }
count()   { if [ -z "${JQ_MODE:-}" ]; then jq 'length' 2>/dev/null; else jget len; fi; }
ids()     { if [ -z "${JQ_MODE:-}" ]; then jq -r '.[].id' 2>/dev/null; else jget ids; fi; }
agents()  { if [ -z "${JQ_MODE:-}" ]; then jq '[.[].agent_id | select(. != null)] | unique | length' 2>/dev/null; else jget agents; fi; }

wait_health() { # nombre url
  local name="$1" url="$2" i
  for i in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then ok "$name healthz ($url)"; return 0; fi
    sleep 2
  done
  return 1
}

echo "== Smoke AI Workforce OS =="
wait_health "backend" "$BACKEND_HEALTH" || die "backend no responde en $BACKEND_HEALTH"
wait_health "agent-runtime" "${RUNTIME_URL}/healthz" || die "runtime no responde en ${RUNTIME_URL}/healthz"

curl -fsS -X POST "${API_URL}/demo/reset" >/dev/null 2>&1 && ok "demo/reset" || echo "  [warn] demo/reset no disponible"

BODY=$(printf '{"text":"%s"}' "$TEXT")
RESP=$(curl -fsS -X POST "${API_URL}/requests" -H 'content-type: application/json' -d "$BODY") || die "POST /requests falló"
REQ_ID=$(echo "$RESP" | field '.request_id' 'request_id')
[ -n "$REQ_ID" ] && ok "request creada: $REQ_ID" || die "POST /requests sin request_id: $RESP"

APPROVED=0
DEADLINE=$(( $(date +%s) + TIMEOUT ))
STATUS=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  PENDING=$(curl -fsS "${API_URL}/approvals?status=pending" 2>/dev/null)
  for AID in $(echo "$PENDING" | ids); do
    if curl -fsS -X POST "${API_URL}/approvals/${AID}/decision" -H 'content-type: application/json' \
         -d '{"decision":"approve","note":"smoke"}' >/dev/null 2>&1; then
      APPROVED=$((APPROVED+1)); echo "  .. aprobada $AID"
    fi
  done
  STATUS=$(curl -fsS "${API_URL}/requests/${REQ_ID}" 2>/dev/null | field '.status' 'status')
  [ "$STATUS" = "done" ] && break
  [ "$STATUS" = "failed" ] && die "request terminó en failed"
  sleep 1
done

[ "$APPROVED" -ge 1 ] && ok "aprobaciones resueltas: $APPROVED" || bad "nunca apareció una aprobación"
[ "$STATUS" = "done" ] && ok "request done" || die "timeout (${TIMEOUT}s) esperando request done (estado: ${STATUS:-?})"

REQ_JSON=$(curl -fsS "${API_URL}/requests/${REQ_ID}")
REPORT_ID=$(echo "$REQ_JSON" | field '.report_id' 'report_id')
if [ -n "$REPORT_ID" ] && curl -fsS "${API_URL}/reports/${REPORT_ID}" >/dev/null 2>&1; then
  ok "report existe: $REPORT_ID"
else
  bad "no existe report para la request"
fi

N=$(curl -fsS "${API_URL}/activity?limit=500" | agents)
[ "${N:-0}" -ge 5 ] && ok "activity con $N agentes distintos (>=5)" || bad "activity con solo ${N:-0} agentes distintos (se requieren 5)"

finish

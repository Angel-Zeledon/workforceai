#!/usr/bin/env sh
# Publish the workforceai site blocks into the shared Caddy (owned by the stockai project) and reload it
# gracefully (no restart, no downtime). Safe to re-run.
# Refuses to run until DNS points at this VPS, to avoid failing Let's Encrypt attempts.
set -eu
cd "$(dirname "$0")"

STOCKAI_DEPLOY="${STOCKAI_DEPLOY:-/opt/stockai/deploy}"
CADDY_CONTAINER="${CADDY_CONTAINER:-faro-caddy-1}"
EXTRA_DIR=/opt/workforceai/caddy
SNIPPET=caddy/workforceai.caddy

MY_IP="$(curl -4 -fsS --max-time 5 https://ifconfig.me || true)"
for host in workforceai.es www.workforceai.es app.workforceai.es; do
    got="$(getent ahostsv4 "$host" | awk 'NR==1{print $1}')"
    if [ -z "$got" ] || { [ -n "$MY_IP" ] && [ "$got" != "$MY_IP" ]; }; then
        echo "DNS not ready: $host -> '${got:-none}' (this VPS is ${MY_IP:-unknown}). Aborting." >&2
        exit 1
    fi
done

# Validate the live Caddyfile plus the new snippet in a throwaway container before touching anything.
mkdir -p "$EXTRA_DIR"
STAGE="$(mktemp -d)"; cp "$SNIPPET" "$STAGE/workforceai.caddy"
docker run --rm --env-file "$STOCKAI_DEPLOY/.env" \
    -v "$STOCKAI_DEPLOY/Caddyfile.split:/etc/caddy/Caddyfile:ro" -v "$STAGE:/etc/caddy/extra:ro" \
    caddy:2-alpine caddy validate --config /etc/caddy/Caddyfile >/dev/null
rm -rf "$STAGE"

cp "$SNIPPET" "$EXTRA_DIR/workforceai.caddy"
docker exec "$CADDY_CONTAINER" caddy reload --config /etc/caddy/Caddyfile
echo "Activated. Check: https://workforceai.es and https://app.workforceai.es"

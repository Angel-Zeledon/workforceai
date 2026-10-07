#!/usr/bin/env sh
# Deploy/update the demo on the VPS. Run from /opt/workforceai/src/deploy/vps.
#   ./deploy.sh          # pull, build (low priority so stockai is not starved), start
set -eu
cd "$(dirname "$0")"
git pull --ff-only
# Builds run niced: the VPS is shared with a live site.
nice -n 19 ionice -c3 docker compose build
docker compose up -d --wait
# The Caddy snippet is only STAGED here; ./activate-caddy.sh publishes it once DNS is ready.
mkdir -p /opt/workforceai/caddy-staged
cp caddy/workforceai.caddy /opt/workforceai/caddy-staged/workforceai.caddy
echo "Deployed. Status:"
docker compose ps

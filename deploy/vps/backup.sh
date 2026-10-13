#!/bin/sh
# Nightly Postgres backup of AI Workforce OS (accounts, organizations, everything the app stores).
# Keeps 14 days in /var/backups/workforceai and fails loudly (non-zero exit + message) if the dump looks wrong.
# Installed by cron on the VPS:  30 3 * * * /opt/workforceai/src/deploy/vps/backup.sh >> /var/log/workforceai-backup.log 2>&1
set -eu
D=/var/backups/workforceai
T=$(date +%F)
C=${WF_PG_CONTAINER:-workforceai-wf-postgres-1}
mkdir -p "$D"
umask 077
docker exec "$C" pg_dump -U workforce -d workforce | gzip > "$D/db-$T.sql.gz.tmp"
[ "$(stat -c%s "$D/db-$T.sql.gz.tmp")" -gt 2000 ] || { echo "$(date -Is) DB BACKUP TOO SMALL" >&2; rm -f "$D/db-$T.sql.gz.tmp"; exit 1; }
mv "$D/db-$T.sql.gz.tmp" "$D/db-$T.sql.gz"
find "$D" -type f -name 'db-*.sql.gz' -mtime +14 -delete
echo "$(date -Is) backup ok: db-$T.sql.gz $(stat -c%s "$D/db-$T.sql.gz") bytes"

#!/bin/bash
# predeploy.sh: run before `cerberus resource deploy nanite-api-service --ack` on the Mac (CW-20261024-0113).
# Refuses while an agent runtime is active (override: --force) and takes sqlite .backup copies of the
# auth and workspace databases with an integrity check. Prints the backup directory; never prints secrets.
# bash 3.2 compatible. Env: NANITE_MAC_WORKSPACE (default mesh-chat).
set -euo pipefail
WORKSPACE=${NANITE_MAC_WORKSPACE:-mesh-chat}
DATA=$HOME/.local/share/nanite
BACKUPS=$DATA/backups
force=0; [ "${1:-}" = "--force" ] && force=1
die() { echo "predeploy: $*" >&2; exit 1; }
command -v sqlite3 >/dev/null || die "sqlite3 not found"

db=$DATA/workspaces/$WORKSPACE/main.db
if [ -f "$db" ]; then
  n=$(sqlite3 -readonly "$db" "select count(*) from agent_runtime where state in ('launching','running')" 2>/dev/null || echo 0)
  if [ "$n" != 0 ] && [ $force = 0 ]; then die "$n agent runtime(s) active; wait or pass --force"; fi
  v=$(sqlite3 -readonly "$db" "select max(version_id) from goose_db_version" 2>/dev/null || echo unknown)
  echo "workspace $WORKSPACE schema version: $v"
fi

bk=$BACKUPS/$(date +%Y%m%d-%H%M%S)
mkdir -p "$bk"; chmod 700 "$BACKUPS" "$bk"
for pair in "auth/auth.db:auth.db" "workspaces/$WORKSPACE/main.db:main.db"; do
  src=$DATA/${pair%%:*}; dst=$bk/${pair##*:}
  [ -f "$src" ] || continue
  sqlite3 "$src" ".backup $dst"
  [ "$(sqlite3 "$dst" 'pragma integrity_check')" = ok ] || die "backup of $src failed integrity_check"
  chmod 600 "$dst"
done
echo "backups: $bk"
echo "to undo a migration: cerberus resource stop nanite-api-service --ack, copy the two files back over the live databases, delete -wal/-shm, deploy the previous build"

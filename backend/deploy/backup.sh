#!/bin/sh
set -eu
umask 077
backup() {
  stamp=$(date -u +%Y%m%dT%H%M%SZ)
  mkdir -p /backups
  for database in catalog booking payment identity; do
    pg_dump -h postgres -U postgres -Fc "$database" > "/backups/.banya-${database}-${stamp}.tmp"
    mv "/backups/.banya-${database}-${stamp}.tmp" "/backups/banya-${database}-${stamp}.dump"
  done
  find /backups -maxdepth 1 -type f -name 'banya-*.dump' -mtime +29 -delete
  printf 'Backup completed at %s\n' "$stamp"
}
if [ "${1:-}" = once ]; then backup; exit 0; fi
trap 'exit 0' TERM INT
while true; do backup; sleep 86400 & wait "$!"; done

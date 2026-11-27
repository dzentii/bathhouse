#!/usr/bin/env bash
# Called by GitHub Actions after uploading a release to /opt/banya/releases/<sha>.
set -Eeuo pipefail
umask 077
sha=${1:?expected full commit SHA}
prefix=${2:?expected ghcr.io/owner/repository}
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo 'Invalid SHA' >&2; exit 1; }
[[ $prefix =~ ^ghcr\.io/[a-z0-9._/-]+$ ]] || { echo 'Invalid registry prefix' >&2; exit 1; }
root=/opt/banya
release="$root/releases/$sha"
cd "$release/backend"
exec 9>"$root/deploy.lock"
flock -n 9 || { echo 'Another deployment is running' >&2; exit 1; }
[[ -f "$root/shared/.env" ]] || { echo 'Create shared/.env first' >&2; exit 1; }
chmod 600 "$root/shared/.env"
python3 - "$root/shared/.env" <<'PY'
import sys
from urllib.parse import urlsplit
values = dict(line.split('=', 1) for line in open(sys.argv[1]).read().splitlines() if '=' in line and not line.startswith('#'))
domain = values.get('DOMAIN', '')
origin = 'https://' + domain
if not domain or urlsplit(origin).hostname != domain or values.get('APP_ENV') != 'production' or values.get('COOKIE_SECURE') != 'true' or values.get('PUBLIC_URL') != origin or values.get('ALLOWED_ORIGIN') != origin:
    raise SystemExit('Require DOMAIN, APP_ENV=production, COOKIE_SECURE=true and matching HTTPS origins')
PY
printf 'IMAGE_PREFIX=%s\nIMAGE_TAG=%s\nBACKUP_DIR=%s/backups\nDEPLOY_UID=%s\nDEPLOY_GID=%s\n' "$prefix" "$sha" "$root" "$(id -u)" "$(id -g)" > "$release/release.env"
dc=(docker compose --project-name banya-staging --env-file "$root/shared/.env" --env-file "$release/release.env" -f docker-compose.yml -f compose.production.yaml -f compose.staging.yaml)
"${dc[@]}" config --quiet
# Pull before downtime, never build on the server. No changes if the registry is unavailable.
"${dc[@]}" pull
available=$(df -Pk "$root" | awk 'NR==2 {print $4}')
(( available >= 5 * 1024 * 1024 )) || { echo 'Need at least 5 GiB free after pulling images' >&2; exit 1; }
previous=''
if [[ -L $root/current ]]; then
    previous=$(readlink -e "$root/current")
    [[ -f $previous/release.env ]] || { echo 'Invalid current release' >&2; exit 1; }
elif [[ -e $root/current ]]; then
    echo 'current must be a release symlink' >&2
    exit 1
fi
phase=prepare
recover() {
    code=$?
    trap - ERR
    if [[ $phase == checkpoint && -n $previous && -f $previous/release.env ]]; then
        # No migration has started: it is safe to restore the previously running binaries.
        (cd "$previous/backend" && docker compose --project-name banya-staging --env-file "$root/shared/.env" --env-file "$previous/release.env" -f docker-compose.yml -f compose.production.yaml -f compose.staging.yaml up -d --no-build --no-deps --wait identity catalog booking payment gateway frontend caddy) || true
    elif [[ $phase == migration || $phase == start ]]; then
        "${dc[@]}" stop caddy gateway catalog booking payment identity || true
        echo 'Deployment failed after migration began. Public traffic stopped. Inspect migrations and backups; no automatic schema rollback.' >&2
    fi
    exit "$code"
}
trap recover ERR
if [[ -n $previous && -f $previous/release.env ]]; then
    phase=checkpoint
    # Freeze all writers, including outbox consumers, for a consistent four-database checkpoint.
    "${dc[@]}" stop caddy gateway catalog booking payment identity
    "${dc[@]}" --profile ops run --rm --no-deps backup once
fi
phase=migration
"${dc[@]}" up -d --no-build --wait --wait-timeout 240 postgres redis kafka
"${dc[@]}" run --rm --no-deps kafka-init
for service in catalog booking payment identity; do
    "${dc[@]}" run --rm --no-deps "migrate-$service"
done
phase=start
# Dependencies have already been checked/migrated explicitly, do not rerun one-shot jobs.
"${dc[@]}" up -d --no-build --no-deps --wait --wait-timeout 180 identity catalog booking payment frontend swagger gateway caddy
bash deploy/smoke.sh "$root/shared/.env" "$release/release.env"
if [[ -n $previous && $previous != "$release" ]]; then
    ln -sfn "$previous" "$root/previous"
fi
ln -sfn "$release" "$root/current"
phase=complete
echo "Deployment healthy: $sha"

#!/usr/bin/env bash
# Daily consistent checkpoint. A short planned outage is acceptable on this test server.
set -Eeuo pipefail
umask 077
root=/opt/banya
exec 9>"$root/deploy.lock"
flock -w 1800 9
release=$(readlink -f "$root/current")
[[ -f $release/release.env ]]
cd "$release/backend"
dc=(docker compose --project-name banya-staging --env-file "$root/shared/.env" --env-file "$release/release.env" -f docker-compose.yml -f compose.production.yaml -f compose.staging.yaml)
running=()
running_services=$("${dc[@]}" ps --status running --services)
while IFS= read -r service; do
    case "$service" in
        caddy|gateway|catalog|booking|payment|identity) running+=("$service");;
    esac
done <<< "$running_services"
finish() {
    code=$?
    trap - EXIT
    if (( ${#running[@]} )); then
        if ! "${dc[@]}" up -d --no-build --no-deps --wait "${running[@]}"; then code=1; fi
    fi
    exit "$code"
}
trap finish EXIT
if (( ${#running[@]} )); then "${dc[@]}" stop "${running[@]}"; fi
"${dc[@]}" --profile ops run --rm --no-deps backup once

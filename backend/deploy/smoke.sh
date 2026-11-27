#!/usr/bin/env bash
# Read-only checks. Run from the release's backend directory.
set -Eeuo pipefail
env_file=${1:?shared .env required}
release_file=${2:?release.env required}
dc=(docker compose --project-name banya-staging --env-file "$env_file" --env-file "$release_file" -f docker-compose.yml -f compose.production.yaml -f compose.staging.yaml)
for path in /healthz /api/v1/baths /openapi.yaml /swagger/; do
    "${dc[@]}" exec -T gateway wget -q -O /dev/null "http://127.0.0.1:8080$path"
done
"${dc[@]}" exec -T frontend wget -q -O /dev/null http://127.0.0.1:8080/admin/login
"${dc[@]}" exec -T gateway wget -q -O - http://127.0.0.1:8080/api/v1/baths | python3 -c 'import json,sys; assert json.load(sys.stdin), "Empty catalog response"'
domain=$(python3 - "$env_file" <<'PY'
import sys
for line in open(sys.argv[1]):
    if line.startswith('DOMAIN='):
        print(line.strip().split('=', 1)[1])
        break
PY
)
# Verify real HTTPS and Caddy routing; DNS must already point to this server.
for path in / /api/v1/baths /swagger/ /openapi.yaml; do
    curl --fail --silent --show-error --retry 12 --retry-all-errors --retry-delay 5 --retry-max-time 120 --max-time 10 "https://$domain$path" -o /dev/null
done
echo 'HTTP API, frontend, Swagger and HTTPS checks passed'

#!/usr/bin/env python3
"""Generate server-only secrets once; never read or overwrite the local .env."""
import argparse
import os
import re
import secrets
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("domain", help="DNS name without scheme or port")
parser.add_argument("output", type=Path, help="For example /opt/banya/shared/.env")
args = parser.parse_args()
labels = args.domain.split(".")
if len(args.domain) > 253 or len(labels) < 2 or not all(
    re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label) for label in labels
):
    parser.error("Use a lowercase DNS name, for example test.example.ru")

values = {
    "APP_ENV": "production", "COOKIE_SECURE": "true", "DOMAIN": args.domain,
    "PUBLIC_URL": f"https://{args.domain}", "ALLOWED_ORIGIN": f"https://{args.domain}",
    "BACKUP_DIR": "/opt/banya/backups", "APP_GOMAXPROCS": "1",
    "PDF_MAX_CONCURRENT": "1", "PDF_CACHE_MIB": "8",
}
for key in (
    "POSTGRES_PASSWORD", "CATALOG_DB_PASSWORD", "BOOKING_DB_PASSWORD",
    "PAYMENT_DB_PASSWORD", "IDENTITY_DB_PASSWORD", "REDIS_PASSWORD",
    "INTERNAL_API_KEY", "ORDER_TOKEN_KEY", "TICKET_TOKEN_KEY", "ADMIN_PASSWORD",
):
    values[key] = secrets.token_hex(32)
template = Path(__file__).resolve().parents[1] / ".env.example"
lines = []
for line in template.read_text().splitlines():
    key, sep, value = line.partition("=")
    lines.append(f"{key}={values[key]}" if sep and key in values else line)
try:
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    raise SystemExit(f"{args.output} already exists; no changes made") from None
with os.fdopen(fd, "w") as output:
    output.write("\n".join(lines) + "\n")
print(f"Created {args.output} with new secrets (0600); existing files are never replaced.")

#!/usr/bin/env python3
"""Create local secrets once. Existing files are never overwritten."""
import os
import secrets
from pathlib import Path

root = Path(__file__).resolve().parents[1]
target = root / '.env'
if target.exists():
    raise SystemExit('.env already exists; no changes made')
secret_keys = {'POSTGRES_PASSWORD','CATALOG_DB_PASSWORD','BOOKING_DB_PASSWORD','PAYMENT_DB_PASSWORD','IDENTITY_DB_PASSWORD','REDIS_PASSWORD','INTERNAL_API_KEY','ORDER_TOKEN_KEY','TICKET_TOKEN_KEY','ADMIN_PASSWORD'}
lines=[]
for line in (root/'.env.example').read_text().splitlines():
    key,sep,value=line.partition('=')
    if key in secret_keys:line=f'{key}={secrets.token_hex(32)}'
    lines.append(line)
fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'w') as f:f.write('\n'.join(lines)+'\n')
print('Created backend/.env with random secrets (mode 0600). Credentials are not printed.')

#!/usr/bin/env python3
"""Verify up/down/up using four fresh disposable databases, never the application DBs."""
import os
import subprocess
import uuid
from pathlib import Path

root=Path(__file__).resolve().parents[1]
values=dict(line.split('=',1) for line in (root/'.env').read_text().splitlines() if '=' in line)
if values.get('APP_ENV')!='development':raise SystemExit('Only the local development environment is allowed')
binary=root/'bin/migrate'
if not binary.is_file():raise SystemExit('First run: go build -o bin/migrate ./cmd/migrate')

def psql(sql):
    result=subprocess.run(['docker','compose','exec','-T','postgres','psql','-U','postgres','-v','ON_ERROR_STOP=1','-tAc',sql],cwd=root,text=True,capture_output=True)
    if result.returncode:raise RuntimeError('PostgreSQL migration test setup failed')
    return result.stdout.strip()

for service in ['catalog','booking','payment','identity']:
    name=f'banya_migration_{service}_{uuid.uuid4().hex}'
    psql(f'CREATE DATABASE "{name}" OWNER {service}')
    try:
        env={**os.environ,'DATABASE_URL':f'postgres://{service}:{values[service.upper()+"_DB_PASSWORD"]}@localhost:55432/{name}?sslmode=disable','MIGRATIONS_PATH':str(root/'services'/service/'migrations')}
        migration_count=len(list((root/'services'/service/'migrations').glob('*.up.sql')))
        directions=['up']+['down']*migration_count+['up']
        for direction in directions:
            result=subprocess.run([str(binary),direction],cwd=root,env=env,text=True,capture_output=True)
            if result.returncode:raise RuntimeError(f'{service}: {direction} failed: {result.stderr}')
        print(f'{service}: up/down/up passed')
    finally:
        psql(f'DROP DATABASE "{name}" WITH (FORCE)')

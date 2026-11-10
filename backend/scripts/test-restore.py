#!/usr/bin/env python3
"""Restore the latest backup of each service into a fresh disposable database."""
import subprocess
import uuid
from pathlib import Path

root=Path(__file__).resolve().parents[1]
values=dict(line.split('=',1) for line in (root/'.env').read_text().splitlines() if '=' in line)
if values.get('APP_ENV')!='development':raise SystemExit('Only local development restoration tests are allowed')
backup_dir=Path(values.get('BACKUP_DIR','./backups'))
if not backup_dir.is_absolute():backup_dir=root/backup_dir

def psql(sql,database='postgres'):
    result=subprocess.run(['docker','compose','exec','-T','postgres','psql','-U','postgres','-d',database,'-v','ON_ERROR_STOP=1','-tAc',sql],cwd=root,text=True,capture_output=True)
    if result.returncode:raise RuntimeError('Restore test query failed')
    return result.stdout.strip()

for service in ['catalog','booking','payment','identity']:
    files=sorted(backup_dir.glob(f'banya-{service}-*.dump'))
    if not files:raise SystemExit(f'No {service} backup found')
    name=f'banya_restore_{service}_{uuid.uuid4().hex}'
    psql(f'CREATE DATABASE "{name}" OWNER {service}')
    try:
        command=f'pg_restore -h postgres -U postgres --no-owner --role={service} --exit-on-error -d {name} /backups/{files[-1].name}'
        restored=subprocess.run(['docker','compose','--profile','ops','run','--rm','--no-deps','--entrypoint','/bin/sh','backup','-ec',command],cwd=root,text=True,capture_output=True)
        if restored.returncode:raise RuntimeError(f'{service}: pg_restore failed')
        assert psql('SELECT dirty FROM schema_migrations',name)=='f'
        table={'catalog':'departments','booking':'resources','payment':'attempts','identity':'admins'}[service]
        assert int(psql(f'SELECT count(*) FROM {table}',name)) >= {'catalog':16,'booking':149,'payment':0,'identity':1}[service]
        if service=='booking':
            assert psql("SELECT NOT EXISTS(SELECT 1 FROM orders WHERE status='confirmed' AND ticket_hash IS NULL)",name)=='t'
        if service in {'catalog','booking','payment'}:
            before=int(psql('SELECT count(*) FROM outbox',name))
            psql('UPDATE outbox SET delivered_at=NULL,next_attempt_at=clock_timestamp()',name)
            assert int(psql('SELECT count(*) FROM outbox WHERE delivered_at IS NULL',name))==before
        print(f'{service}: backup restored and data verified')
    finally:
        psql(f'DROP DATABASE "{name}" WITH (FORCE)')

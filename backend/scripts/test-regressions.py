#!/usr/bin/env python3
"""Run security/memory regressions in a disposable booking database, with allocation profiles."""
import os,subprocess,uuid
from pathlib import Path
root=Path(__file__).resolve().parents[1];values=dict(line.split('=',1) for line in (root/'.env').read_text().splitlines() if '=' in line)
assert values['APP_ENV']=='development'
name='banya_regression_'+uuid.uuid4().hex
def sql(q):
 r=subprocess.run(['docker','compose','exec','-T','postgres','psql','-U','postgres','-v','ON_ERROR_STOP=1','-tAc',q],capture_output=True,text=True,cwd=root)
 if r.returncode:raise RuntimeError('Regression database setup failed')
 return r.stdout
base={**os.environ,'GOMAXPROCS':'2','GOFLAGS':'-p=1'}
url=f'postgres://booking:{values["BOOKING_DB_PASSWORD"]}@localhost:55432/{name}?sslmode=disable'
sql(f'CREATE DATABASE "{name}" OWNER booking')
try:
 r=subprocess.run([str(root/'bin/migrate'),'up'],env={**base,'DATABASE_URL':url,'MIGRATIONS_PATH':str(root/'services/booking/migrations')},capture_output=True,text=True,cwd=root)
 if r.returncode:raise RuntimeError('Regression migrations failed')
 (root/'tmp/regression').mkdir(parents=True,exist_ok=True)
 with (root/'tmp/regression/probes.log').open('w') as log:
  r=subprocess.run(['go','test','-race','-count=1','-v','-timeout=3m','-run','^TestRegression','./services/booking/internal'],env={**base,'BANYA_PDF_QA_DIR':str(root/'tmp/pdfs'),'BANYA_REGRESSION_DATABASE_URL':url,'BANYA_REGRESSION_REDIS_URL':f'redis://:{values["REDIS_PASSWORD"]}@localhost:56379/0'},stdout=log,stderr=subprocess.STDOUT)
 print('Regression probes exit code:',r.returncode)
 if r.returncode:raise SystemExit(r.returncode)
 with (root/'tmp/regression/benchmarks.log').open('w') as log:
  r=subprocess.run(['go','test','-run','^$','-bench','^BenchmarkRegression','-benchtime=5x','-benchmem','-memprofile=tmp/regression/heap.pprof','-o','tmp/regression/booking-regression.test','./services/booking/internal'],cwd=root,env=base,stdout=log,stderr=subprocess.STDOUT)
 print('Regression benchmarks exit code:',r.returncode)
 if r.returncode:raise SystemExit(r.returncode)
finally:
 sql(f'DROP DATABASE "{name}" WITH (FORCE)')
 print('Isolated regression database removed')

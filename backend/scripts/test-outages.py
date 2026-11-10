#!/usr/bin/env python3
"""Exercise a local dependency outage and always restore its container."""
import argparse
import json
import subprocess
import sys
import time
import uuid
import urllib.error
import urllib.request
from pathlib import Path

root=Path(__file__).resolve().parents[1]
values=dict(line.split('=',1) for line in (root/'.env').read_text().splitlines() if '=' in line)
if values.get('APP_ENV')!='development':raise SystemExit('Dependency outages are only allowed in the local test environment')
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('dependency',choices=['redis','kafka'])
args=parser.parse_args()
base='http://localhost:'+values.get('HTTP_PORT','8080')
def compose(*arguments):
    result=subprocess.run(['docker','compose',*arguments],cwd=root,text=True,capture_output=True)
    if result.returncode:raise RuntimeError('Docker dependency test action failed')
    return result.stdout
def get(path,token=None):
    headers={'Authorization':'Bearer '+token} if token else {}
    with urllib.request.urlopen(urllib.request.Request(base+path,headers=headers),timeout=20) as r:return json.load(r)

try:
    compose('stop',args.dependency)
    print('Stopped local test dependency: '+args.dependency,flush=True)
    if args.dependency=='redis':
        get('/api/v1/baths');get('/api/v1/departments/b1-male-general/booking-config')
        request=urllib.request.Request(base+'/api/v1/admin/sessions',data=json.dumps({'username':values['ADMIN_USERNAME'],'password':values['ADMIN_PASSWORD']}).encode(),headers={'Content-Type':'application/json','X-Idempotency-Key':str(uuid.uuid4())},method='POST')
        try:
            urllib.request.urlopen(request,timeout=20)
            raise AssertionError('Login succeeded without its rate limiter')
        except urllib.error.HTTPError as error:assert error.code==503
        for request in [urllib.request.Request(base+'/api/v1/quotes',data=b'{}',headers={'Content-Type':'application/json','X-Idempotency-Key':str(uuid.uuid4())},method='POST'),urllib.request.Request(base+'/api/v1/departments/b1-male-general/availability-calendar?from=2026-11-11&to=2026-11-11')]:
            try:
                urllib.request.urlopen(request,timeout=20)
                raise AssertionError('Protected operation succeeded without Redis')
            except urllib.error.HTTPError as error:assert error.code==503
        print('Redis outage: PostgreSQL read fallback works; login/write/heavy routes return 503',flush=True)
    else:
        subprocess.run([sys.executable,str(root/'scripts/demo.py'),'--url',base,'--no-wait'],cwd=root,check=True,capture_output=True)
        result=json.loads((root/'tmp/demo-result.json').read_text());oid=result['order']['id'];pid=result['attempt']['id'];token=result['order_token']
        assert get(f'/api/v1/orders/{oid}/payments/{pid}',token)['status']=='pending'
        login=urllib.request.Request(base+'/api/v1/admin/sessions',data=json.dumps({'username':values['ADMIN_USERNAME'],'password':values['ADMIN_PASSWORD']}).encode(),headers={'Content-Type':'application/json','X-Idempotency-Key':str(uuid.uuid4())},method='POST')
        with urllib.request.urlopen(login,timeout=20) as response:admin=json.load(response)['access_token']
        view=get(f'/api/v1/admin/orders/{oid}',admin)
        assert view['payment']['status']=='pending' and view['payment']['paid_kopecks']==0
        logout=urllib.request.Request(base+'/api/v1/admin/sessions/current',headers={'Authorization':'Bearer '+admin},method='DELETE')
        with urllib.request.urlopen(logout,timeout=20) as response:assert response.status==204

        query=f"SELECT count(*),coalesce(sum(attempts),0) FROM outbox WHERE delivered_at IS NULL AND payload->'data'->>'payment_id'='{pid}'"
        deadline=time.monotonic()+30
        while True:
            count,retries=map(int,compose('exec','-T','postgres','psql','-U','postgres','-d','payment','-tAc',query).strip().split('|'))
            if count==1 and retries>0:break
            if time.monotonic()>deadline:raise AssertionError('Producer retry not recorded')
            time.sleep(.5)
        print('Kafka outage: pending preserved, durable outbox retry observed',flush=True)
finally:
    compose('start','--wait',args.dependency)
    print('Restored local test dependency: '+args.dependency,flush=True)

if args.dependency=='kafka':
    subprocess.run([sys.executable,str(root/'scripts/demo.py'),'--url',base,'--resume'],cwd=root,check=True,capture_output=True)
    print('Kafka recovery: the same pending attempt became accepted and its order confirmed',flush=True)

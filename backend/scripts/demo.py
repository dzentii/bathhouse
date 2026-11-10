#!/usr/bin/env python3
"""Run a real HTTP/Kafka test checkout. Secrets are saved locally, never printed."""
import argparse
import datetime as dt
import json
import os
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from zoneinfo import ZoneInfo

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url',default='http://localhost:8080')
parser.add_argument('--no-wait',action='store_true',help='save pending attempt immediately')
parser.add_argument('--resume',action='store_true',help='poll the attempt from the saved result')
args=parser.parse_args()
base=args.url.rstrip('/')
target=Path(__file__).resolve().parents[1]/'tmp/demo-result.json'

def call(method,path,body=None,token=None,key=None):
    headers={'Content-Type':'application/json'}
    if token:headers['Authorization']='Bearer '+token
    if method in ('POST','PUT','PATCH'):headers['X-Idempotency-Key']=key or str(uuid.uuid4())
    data=None if body is None else json.dumps(body).encode()
    req=urllib.request.Request(base+path,data=data,headers=headers,method=method)
    try:
        with urllib.request.urlopen(req,timeout=20) as response:return json.load(response)
    except urllib.error.HTTPError as e:
        try:code=json.load(e).get('error',{}).get('code','http_error')
        except (ValueError,AttributeError):code='http_error'
        raise SystemExit(f'HTTP {e.code}: {code}') from None

def save(result):
    target.parent.mkdir(parents=True,exist_ok=True)
    fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
    os.fchmod(fd,0o600)
    with os.fdopen(fd,'w') as f:json.dump(result,f,ensure_ascii=False,indent=2)

if args.resume:
    result=json.loads(target.read_text())
else:
    now=dt.datetime.now(ZoneInfo('Asia/Yekaterinburg'))
    selected=None
    for offset in range(8):
        date=(now+dt.timedelta(days=offset)).date().isoformat()
        availability=call('GET','/api/v1/departments/b1-male-general/availability?date='+date)
        for slot in availability['items']:
            free=[r for r in slot['resources'] if r['state']=='free']
            if free:
                selected={'department_id':'b1-male-general','resource_ids':[free[0]['id']],'starts_at':slot['starts_at'],'ends_at':slot['ends_at'],'guests':[{'category':'adult','count':1}]}
                break
        if selected:break
    if not selected:raise SystemExit('No available future demo session')
    key=str(uuid.uuid4())
    created=call('POST','/api/v1/orders',{'selection':selected,'buyer':{'name':'Демонстрационный заказ','phone':'+79000000000'}},key=key)
    result={'create_key':key,**created}
    token=result['order_token'];oid=result['order']['id']
    held=call('POST',f'/api/v1/orders/{oid}/hold',{},token)
    result['order']=held['order'];quote=result['order']['quote']
    result['payment_key']=str(uuid.uuid4())
    result['attempt']=call('POST',f'/api/v1/orders/{oid}/payments',{'amount_kopecks':quote['amount_kopecks'],'currency':'RUB','price_hash':quote['price_hash'],'outcome':'success'},token,result['payment_key'])
    save(result)

if not args.no_wait:
    deadline=time.monotonic()+90
    oid=result['order']['id'];pid=result['attempt']['id'];token=result['order_token']
    while time.monotonic()<deadline:
        result['attempt']=call('GET',f'/api/v1/orders/{oid}/payments/{pid}',token=token)
        result['order']=call('GET',f'/api/v1/orders/{oid}',token=token)
        if result['attempt']['status']!='pending':break
        time.sleep(.5)
    save(result)
    if result['attempt']['status']!='accepted' or result['order']['status']!='confirmed':raise SystemExit('Test payment is not confirmed; saved result contains its current status')
    print('Test payment confirmed; private result saved to backend/tmp/demo-result.json')
else:
    print('Attempt saved; private result is in backend/tmp/demo-result.json')

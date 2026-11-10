#!/usr/bin/env python3
"""Check local proxy attribution, protected metrics and bounded PDF concurrency."""
import concurrent.futures
import hashlib
import json
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

root=Path(__file__).resolve().parents[1]
values=dict(line.split('=',1) for line in (root/'.env').read_text().splitlines() if '=' in line)
if values.get('APP_ENV')!='development':raise SystemExit('Only local development checks are allowed')
base='http://localhost:'+values.get('HTTP_PORT','8080')
def compose(*args,check=True):
    result=subprocess.run(['docker','compose',*args],cwd=root,text=True,capture_output=True)
    if check and result.returncode:raise RuntimeError('Local governance Docker action failed')
    return result.stdout.strip()
def get(path,headers=None):
    try:response=urllib.request.urlopen(urllib.request.Request(base+path,headers=headers or {}),timeout=20)
    except urllib.error.HTTPError as error:response=error
    with response:return response.status,response.read(),dict(response.headers)
def metrics(service):
    raw=compose('exec','-T',service,'sh','-c','wget -qO- --header "X-Internal-Key: $INTERNAL_API_KEY" http://127.0.0.1:8080/internal/metrics')
    data={line.split()[0]:float(line.split()[1]) for line in raw.splitlines() if line and not line.startswith('#')}
    assert 'banya_heap_bytes' in data and 'banya_http_duration_seconds_bucket{le="+Inf"}' in data
    return data

def login_count(ip):
    key='identity:login-ip:'+hashlib.sha256(ip.encode()).hexdigest()
    return int(compose('exec','-T','redis','redis-cli','GET',key) or 0)

assert get('/internal/metrics')[0]==404
for service in ('catalog','booking','payment','identity'):metrics(service)
print('Protected metrics: all four services respond; gateway does not expose them',flush=True)

addresses=[]
for service in ('catalog','booking'):
    cid=compose('ps','-q',service)
    ip=subprocess.run(['docker','inspect','--format','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',cid],check=True,text=True,capture_output=True).stdout.strip()
    before=login_count(ip)
    assert before<18,'Wait one minute before rerunning proxy checks'
    for _ in range(2):
        body=json.dumps({'username':'governance-'+uuid.uuid4().hex[:12],'password':'synthetic-invalid-password'})
        compose('exec','-T',service,'wget','-qO-','--header','Content-Type: application/json','--header','X-Idempotency-Key: '+str(uuid.uuid4()),'--header','X-Forwarded-For: 198.51.100.99','--post-data',body,'http://gateway:8080/api/v1/admin/sessions',check=False)
    assert login_count(ip)-before==2,'Gateway must attribute requests to their real source'
    addresses.append(ip)
assert addresses[0]!=addresses[1]
print('Proxy chain: two source containers have separate login counters; forged XFF does not replace either address',flush=True)

subprocess.run(['python3',str(root/'scripts/demo.py'),'--url',base],cwd=root,check=True,capture_output=True)
result=json.loads((root/'tmp/demo-result.json').read_text());path='/api/v1/tickets/'+result['order']['ticket_url'].rstrip('/').split('/')[-1]+'/pdf'
status,data,headers=get(path);assert status==200 and data.startswith(b'%PDF-')
status,part,partial_headers=get(path,{'Range':'bytes=0-15'});assert status==206 and part==data[:16] and 'Content-Range' in partial_headers
assert get(path,{'Range':f'bytes={len(data)+1}-'})[0]==416
assert get(path,{'If-Modified-Since':headers['Last-Modified']})[0]==304
before=metrics('booking')
with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
    statuses=list(pool.map(lambda _:get(path)[0],range(16)))
assert all(status in (200,429,503) for status in statuses)
assert any(status==200 for status in statuses)
time.sleep(1)
after=metrics('booking')
assert after['banya_heap_bytes']<160*1024*1024
report={'pdf_bytes':len(data),'pdf_concurrent_responses':{str(status):statuses.count(status) for status in set(statuses)},'heap_before_bytes':int(before['banya_heap_bytes']),'heap_after_bytes':int(after['banya_heap_bytes']),'goroutines_before':int(before['banya_goroutines']),'goroutines_after':int(after['banya_goroutines'])}
(root/'tmp/governance-verification.json').write_text(json.dumps(report,indent=2))
print('PDF: 200/206/304/416 verified; bounded concurrency:',report['pdf_concurrent_responses'],flush=True)
print('Booking heap and goroutine samples saved without tokens to tmp/governance-verification.json',flush=True)

#!/usr/bin/env python3
"""Exercise a local development backend and sample memory without logging tokens."""
import argparse
import json
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections import Counter
from pathlib import Path

root = Path(__file__).resolve().parents[1]
env = dict(line.split('=', 1) for line in (root / '.env').read_text().splitlines() if '=' in line)
if env.get('APP_ENV') != 'development':
    raise SystemExit('Stability checks create synthetic data and require development')
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--duration', type=int, default=180)
args = parser.parse_args()
if not 60 <= args.duration <= 900:
    raise SystemExit('duration: 60..900 seconds')
base = 'http://localhost:' + env.get('HTTP_PORT', '8080')
services = ('catalog', 'booking', 'payment', 'identity')


def compose(*arguments):
    result = subprocess.run(['docker', 'compose', *arguments], cwd=root,
                            capture_output=True, text=True, timeout=20)
    if result.returncode:
        raise RuntimeError('Local stability Docker action failed')
    return result.stdout.strip()


def request(method, path, token=None, body=None):
    headers = {'Content-Type': 'application/json', 'X-Request-ID': 'synthetic-private-request-marker'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if method == 'POST':
        headers['X-Idempotency-Key'] = str(uuid.uuid4())
    req = urllib.request.Request(base + path, method=method, headers=headers,
                                 data=None if body is None else json.dumps(body).encode())
    try:
        response = urllib.request.urlopen(req, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        status, data = response.status, response.read()
        retry = min(60, max(1, int(response.headers.get('Retry-After', '1'))))
        if status == 200:
            assert response.headers.get('X-Request-ID') != headers['X-Request-ID']
    return status, data, retry


def metrics():
    snapshot = {'at_seconds': round(time.monotonic() - started, 1), 'services': {}}
    for service in services:
        raw = compose('exec', '-T', service, 'sh', '-c',
                      'wget -qO- --header "X-Internal-Key: $INTERNAL_API_KEY" http://127.0.0.1:8080/internal/metrics')
        values = {line.split()[0]: float(line.split()[1]) for line in raw.splitlines()
                  if line and not line.startswith('#')}
        snapshot['services'][service] = {key: int(values[key]) for key in
                                        ('banya_heap_bytes', 'banya_goroutines', 'banya_db_acquired', 'banya_db_max')}
    return snapshot


def container_starts():
    identifiers = [compose('ps', '-q', service) for service in services]
    result = subprocess.run(['docker', 'inspect', '--format', '{{.State.StartedAt}}', *identifiers],
                            cwd=root, capture_output=True, text=True, timeout=20, check=True)
    return result.stdout.splitlines()


subprocess.run(['python3', str(root / 'scripts/demo.py'), '--url', base], cwd=root,
               check=True, capture_output=True, timeout=100)
demo = json.loads((root / 'tmp/demo-result.json').read_text())
status, body, _ = request('POST', '/api/v1/admin/sessions', body={
    'username': env['ADMIN_USERNAME'], 'password': env['ADMIN_PASSWORD']})
if status != 201:
    raise SystemExit('Stability administrator login failed')
admin = json.loads(body)['access_token']
try:
    status, body, _ = request('GET', '/api/v1/admin/me', admin)
    assert status == 200
    employee = json.loads(body)['employee']['id']
    order = demo['order']
    selection = order['selection']
    department = selection['department_id']
    ticket = urllib.parse.urlsplit(order['ticket_url']).path
    period = urllib.parse.urlencode({'from': selection['starts_at'], 'until': selection['ends_at'], 'limit': 5})
    routes = [
        ('GET', '/api/v1/baths', None, None),
        ('GET', f'/api/v1/departments/{department}/booking-config', None, None),
        ('GET', f'/api/v1/departments/{department}/resources', None, None),
        ('GET', ticket, None, None),
        ('GET', ticket + '/pdf', None, None),
        ('GET', f'/api/v1/departments/{department}/availability?date=' + selection['starts_at'][:10], None, None),
        ('GET', '/api/v1/orders/' + order['id'], demo['order_token'], None),
        ('GET', '/api/v1/admin/me', admin, None),
        ('POST', '/api/v1/admin/orders/search', admin, {'order_id': order['id'], 'limit': 1}),
        ('GET', '/api/v1/admin/security-events?limit=5', admin, None),
        ('GET', '/api/v1/admin/employees/' + employee, admin, None),
        ('GET', f'/api/v1/admin/departments/{department}/occupancy?' + period, admin, None),
    ]
    starts = container_starts()
    started = time.monotonic()
    samples = [metrics()]
    counts = Counter()
    index = 0
    blocked_until = started
    while time.monotonic() - started < args.duration:
        if time.monotonic() >= blocked_until:
            method, path, token, body = routes[index % len(routes)]
            status, _, retry = request(method, path, token, body)
            counts[status] += 1
            if status == 429:
                blocked_until = time.monotonic() + retry
            else:
                assert status == 200, f'Stability operation {index % len(routes)}: HTTP {status}'
            index += 1
        if time.monotonic() - started - samples[-1]['at_seconds'] >= 15:
            samples.append(metrics())
        time.sleep(1)
    samples.append(metrics())
    assert starts == container_starts(), 'Services restarted during observation'
    assert counts[200] >= args.duration // 4, 'Too few successful observations'
    summary = {}
    for service in services:
        values = [sample['services'][service] for sample in samples]
        warm = next(i for i, sample in enumerate(samples) if sample['at_seconds'] >= 30)
        growth = values[-1]['banya_heap_bytes'] - values[warm]['banya_heap_bytes']
        drift = values[-1]['banya_goroutines'] - values[warm]['banya_goroutines']
        assert max(v['banya_heap_bytes'] for v in values) < 160 << 20, service + ': heap budget exceeded'
        assert growth < 32 << 20, service + ': retained growth requires investigation'
        assert drift <= 16, service + ': goroutine growth requires investigation'
        summary[service] = {'heap_growth_after_warmup_bytes': growth, 'goroutine_drift': drift,
                            'max_heap_bytes': max(v['banya_heap_bytes'] for v in values)}
    report = {'duration_seconds': round(time.monotonic() - started, 1), 'responses': dict(counts),
              'no_service_restarts': True, 'summary': summary, 'samples': samples,
              'limitation': 'Short observation with one sequential client; not proof of absence of leaks or a capacity test.'}
    target = root / 'tmp/final-audit/stability.json'
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(report, indent=2))
    print(json.dumps({key: report[key] for key in ('duration_seconds', 'responses', 'no_service_restarts', 'summary')}, indent=2))
finally:
    req = urllib.request.Request(base + '/api/v1/admin/sessions/current', method='DELETE',
                                 headers={'Authorization': 'Bearer ' + admin})
    with urllib.request.urlopen(req, timeout=20) as response:
        assert response.status == 204

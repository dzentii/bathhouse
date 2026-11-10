#!/usr/bin/env python3
"""Verify a new department stays pending during Kafka outage and publishes after recovery."""
import json
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

root = Path(__file__).resolve().parents[1]
env = dict(line.split('=', 1) for line in (root / '.env').read_text().splitlines() if '=' in line)
if env.get('APP_ENV') != 'development':
    raise SystemExit('Only the local development environment is allowed')
base = 'http://localhost:' + env.get('HTTP_PORT', '8080')


def compose(*args):
    result = subprocess.run(['docker', 'compose', *args], cwd=root, capture_output=True)
    if result.returncode:
        raise RuntimeError('Docker dependency action failed')


def call(method, path, body=None, token=None, key=None, expected=200):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if method in ('POST', 'PATCH'):
        headers['X-Idempotency-Key'] = key or str(uuid.uuid4())
    request = urllib.request.Request(base + path, method=method, headers=headers,
                                    data=None if body is None else json.dumps(body).encode())
    try:
        response = urllib.request.urlopen(request, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        if response.status != expected:
            raise AssertionError(f'{method}: unexpected HTTP {response.status}')
        return json.load(response) if expected != 204 else None


admin = call('POST', '/api/v1/admin/sessions', {'username': env['ADMIN_USERNAME'], 'password': env['ADMIN_PASSWORD']}, expected=201)['access_token']
bath = call('POST', '/api/v1/admin/baths', {'name': 'Kafka recovery check', 'address': 'Local synthetic data', 'active': True}, admin, expected=201)
bath_path = '/api/v1/admin/baths/' + str(bath['id'])
source = call('GET', '/api/v1/departments/b1-male-general/booking-config')
setup = {k: source[k] for k in ('base_guests', 'max_guests', 'min_duration_minutes', 'duration_step_minutes', 'start_step_minutes', 'buffer_minutes', 'schedule', 'tariffs')}
setup['resources'] = [{'label': '1', 'x': 100, 'y': 100}]
body = {'name': 'Recovery department', 'mode': 'session', 'active': True, 'booking_setup': setup}
try:
    try:
        compose('stop', 'kafka')
        key = str(uuid.uuid4())
        first = call('POST', bath_path + '/departments', body, admin, key, 202)
        did = first['id']
        assert first['sync_status'] == 'pending'
        assert call('GET', '/api/v1/admin/departments/' + did, token=admin)['sync_status'] == 'pending'
        call('GET', '/api/v1/departments/' + did, expected=404)
        retry_key = str(uuid.uuid4())
        retry_body = {'version': first['version'], 'reason': 'Проверка повтора при недоступном Kafka'}
        retried = call('POST', '/api/v1/admin/departments/' + did + '/sync-retry', retry_body, admin, retry_key, 202)
        assert retried['version'] == first['version'] + 1
        assert retried['sync_status'] == 'pending'
        assert call('POST', '/api/v1/admin/departments/' + did + '/sync-retry', retry_body, admin, retry_key, 202) == retried
        deadline = time.monotonic() + 35
        while True:
            query = f"SELECT count(*) FROM outbox WHERE aggregate_id='{did}' AND delivered_at IS NULL AND attempts>0"
            result = subprocess.run(['docker', 'compose', 'exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'catalog', '-tAc', query], cwd=root, capture_output=True, text=True, check=True)
            if result.stdout.strip() == '2':
                break
            if time.monotonic() > deadline:
                raise AssertionError('Catalog outbox retry not recorded')
            time.sleep(.5)
        print('Kafka outage: department pending, public access denied, two versioned events retrying; HTTP replay created no duplicate', flush=True)
    finally:
        compose('start', '--wait', 'kafka')

    deadline = time.monotonic() + 90
    while call('GET', '/api/v1/admin/departments/' + did, token=admin)['sync_status'] != 'ready':
        if time.monotonic() > deadline:
            raise AssertionError('Projection recovery timeout')
        time.sleep(.5)
    call('GET', '/api/v1/departments/' + did)
    assert len(call('GET', '/api/v1/departments/' + did + '/resources')['resources']) == 1
    replay = call('POST', bath_path + '/departments', body, admin, key, 202)
    assert replay == first
    assert call('POST', '/api/v1/admin/departments/' + did + '/sync-retry', retry_body, admin, retry_key, 202) == retried
    print('Kafka recovery: projection acknowledged, one resource created, original receipt replayed', flush=True)
finally:
    bath = call('GET', bath_path, token=admin)
    departments = call('GET', bath_path + '/departments', token=admin)['items']
    call('PATCH', bath_path, {'version': bath['version'], 'active': False}, admin, expected=202 if departments else 200)
    call('DELETE', '/api/v1/admin/sessions/current', token=admin, expected=204)

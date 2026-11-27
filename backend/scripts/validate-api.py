#!/usr/bin/env python3
"""Check all public OpenAPI operations against real HTTP responses on a local MVP."""
import datetime as dt
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import warnings
from pathlib import Path
from zoneinfo import ZoneInfo

import yaml
from openapi_spec_validator import validate_spec
warnings.filterwarnings('ignore', category=DeprecationWarning)
from jsonschema import Draft202012Validator, RefResolver
from jsonschema.exceptions import ValidationError

root = Path(__file__).resolve().parents[1]
env = dict(line.split('=', 1) for line in Path(os.environ.get('BANYA_TEST_ENV_FILE', str(root / '.env'))).read_text().splitlines() if '=' in line)
if env.get('APP_ENV') != 'development':
    raise SystemExit('HTTP checks create synthetic orders and are restricted to development')
base = os.environ.get('BANYA_TEST_URL', 'http://localhost:' + env.get('HTTP_PORT', '8080'))
spec = yaml.safe_load((root / 'api/openapi.yaml').read_text())
validate_spec(spec)
resolver = RefResolver.from_schema(spec)
covered = set()
results = []
rate_limited_retries = 0


def call(method, path, template=None, body=None, token=None, key=None, want=200):
    global rate_limited_retries
    template = template or path.split('?', 1)[0]
    operation = spec['paths'][template][method.lower()]
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if method in ('POST', 'PUT', 'PATCH'):
        headers['X-Idempotency-Key'] = key or str(uuid.uuid4())
    request = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    for attempt in range(2):
        try:
            response = urllib.request.urlopen(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            status = response.status
            data = response.read()
            retry = response.headers.get('Retry-After', '')
        if status != 429 or want == 429 or attempt or not retry.isdigit() or not 1 <= int(retry) <= 60:
            break
        rate_limited_retries += 1
        time.sleep(int(retry))
    if status != want:
        raise SystemExit(f'{method} {template}: HTTP {status}, expected {want}')
    definition = operation['responses'].get(str(status))
    if not definition:
        raise SystemExit(f'{method} {template}: undocumented status {status}')
    value = None
    schema = definition.get('content', {}).get('application/json', {}).get('schema')
    if schema:
        value = json.loads(data)
        try:
            Draft202012Validator(schema, resolver=resolver).validate(value)
        except ValidationError as error:
            location = '/'.join(str(part) for part in error.absolute_path)
            raise SystemExit(f'{method} {template}: schema mismatch at {location}') from None
    elif 'text/plain' in definition.get('content', {}):
        value = data.decode()
        Draft202012Validator(definition['content']['text/plain']['schema']).validate(value)
    elif 'application/pdf' in definition.get('content', {}):
        if not data.startswith(b'%PDF-') or b'%%EOF' not in data:
            raise SystemExit(f'{method} {template}: invalid PDF')
        target = root / 'tmp/pdfs/ticket-verification.pdf'
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        target.chmod(0o600)
    elif data:
        raise SystemExit(f'{method} {template}: expected empty response')
    covered.add((method.lower(), template))
    results.append({'method': method, 'path': template, 'status': status})
    return value


def public_free_selection():
    zone = ZoneInfo('Asia/Yekaterinburg')
    for day in range(8):
        date = (dt.datetime.now(zone) + dt.timedelta(days=day)).date().isoformat()
        available = call('GET', '/api/v1/departments/b1-male-general/availability?date=' + date, '/api/v1/departments/{departmentID}/availability')
        for slot in available['items']:
            free = [item for item in slot['resources'] if item['state'] == 'free']
            if free:
                return {'department_id': 'b1-male-general', 'resource_ids': [free[0]['id']], 'starts_at': slot['starts_at'], 'ends_at': slot['ends_at'], 'guests': [{'category': 'adult', 'count': 1}]}
    raise SystemExit('No free session in the local synthetic dataset')


def create(selection):
    return call('POST', '/api/v1/orders', body={'selection': selection, 'buyer': {'name': 'Проверка контракта API', 'phone': '+79000000000'}}, want=201)


call('GET', '/healthz')
for bath in call('GET', '/api/v1/baths')['items']:
    call('GET', f'/api/v1/baths/{bath["id"]}', '/api/v1/baths/{bathID}')
    departments = call('GET', f'/api/v1/baths/{bath["id"]}/departments', '/api/v1/baths/{bathID}/departments')
    for department in departments['items']:
        call('GET', '/api/v1/departments/' + department['id'], '/api/v1/departments/{departmentID}')
call('GET', '/api/v1/departments/b1-male-general/booking-config', '/api/v1/departments/{departmentID}/booking-config')
call('GET', '/api/v1/departments/b1-male-general/resources', '/api/v1/departments/{departmentID}/resources')
selection = public_free_selection()
call('POST', '/api/v1/quotes', body=selection)
created = create(selection)
oid, token = created['order']['id'], created['order_token']
call('PATCH', f'/api/v1/orders/{oid}', '/api/v1/orders/{orderID}', body={'version': created['order']['version'], 'buyer': {'name': 'Исправленный тестовый контакт', 'phone': '+79000000000'}}, token=token)
date = selection['starts_at'][:10]
call('GET', f'/api/v1/departments/b1-male-general/availability-calendar?from={date}&to={date}', '/api/v1/departments/{departmentID}/availability-calendar')
held = call('POST', f'/api/v1/orders/{oid}/hold', '/api/v1/orders/{orderID}/hold', body={}, token=token)
conflicting = create(selection)
conflict = call('POST', f'/api/v1/orders/{conflicting["order"]["id"]}/hold', '/api/v1/orders/{orderID}/hold', body={}, token=conflicting['order_token'], want=409)
if conflict['error']['code'] != 'resource_unavailable' or conflict['error']['details']['resource_ids'] != selection['resource_ids']:
    raise SystemExit('Hold conflict did not identify exactly the unavailable selected resources')
call('POST', f'/api/v1/orders/{conflicting["order"]["id"]}/release', '/api/v1/orders/{orderID}/release', body={}, token=conflicting['order_token'])
quote = held['order']['quote']
attempt = call('POST', f'/api/v1/orders/{oid}/payments', '/api/v1/orders/{orderID}/payments', body={'amount_kopecks': quote['amount_kopecks'], 'currency': 'RUB', 'price_hash': quote['price_hash'], 'outcome': 'success'}, token=token, want=202)
deadline = time.monotonic() + 30
while time.monotonic() < deadline:
    payment = call('GET', f'/api/v1/orders/{oid}/payments/{attempt["id"]}', '/api/v1/orders/{orderID}/payments/{paymentID}', token=token)
    if payment['status'] != 'pending':
        if payment['status'] != 'accepted':
            raise SystemExit('Synthetic payment was not accepted')
        break
    time.sleep(0.25)
else:
    raise SystemExit('Payment result timeout')
order = call('GET', f'/api/v1/orders/{oid}', '/api/v1/orders/{orderID}', token=token)
call('GET', urllib.parse.urlsplit(order['ticket_url']).path, '/api/v1/tickets/{token}')
call('GET', urllib.parse.urlsplit(order['ticket_url']).path + '/pdf', '/api/v1/tickets/{token}/pdf')
call('GET', f'/api/v1/orders/{oid}/payments', '/api/v1/orders/{orderID}/payments', token=token)
other = create(public_free_selection())
call('POST', f'/api/v1/orders/{other["order"]["id"]}/release', '/api/v1/orders/{orderID}/release', body={}, token=other['order_token'])

session = call('POST', '/api/v1/admin/sessions', body={'username': env['ADMIN_USERNAME'], 'password': env['ADMIN_PASSWORD']}, want=201)
admin = session['access_token']
call('GET', '/api/v1/admin/me', token=admin)
call('GET', '/api/v1/admin/sessions', token=admin)
call('GET', '/api/v1/admin/employees', token=admin)
username, password = 'schema-' + str(uuid.uuid4()), str(uuid.uuid4())
employee = call('POST', '/api/v1/admin/employees', body={'username': username, 'display_name': 'Проверка Swagger', 'password': password, 'role': 'admin'}, token=admin, want=201)
call('GET', '/api/v1/admin/employees/' + employee['id'], '/api/v1/admin/employees/{employeeID}', token=admin)
employee_session = call('POST', '/api/v1/admin/sessions', body={'username': username, 'password': password}, want=201)
employee_token = employee_session['access_token']
call('PUT', '/api/v1/admin/me/password', body={'current_password': password, 'new_password': str(uuid.uuid4())}, token=employee_token)
profile = call('GET', '/api/v1/admin/me', token=employee_token)
call('DELETE', '/api/v1/admin/sessions/' + profile['session_id'], '/api/v1/admin/sessions/{sessionID}', token=employee_token, want=204)
call('PATCH', '/api/v1/admin/employees/' + employee['id'], '/api/v1/admin/employees/{employeeID}', body={'version': profile['employee']['version'], 'active': False}, token=admin)
call('GET', '/api/v1/admin/security-events?employee_id=' + employee['id'] + '&limit=2', '/api/v1/admin/security-events', token=admin)
call('GET', '/api/v1/admin/security-events?limit=1', '/api/v1/admin/security-events', token=admin)
call('GET', '/api/v1/admin/orders?limit=10', '/api/v1/admin/orders', token=admin)
call('POST', '/api/v1/admin/orders/search', body={'order_id': oid, 'phone': '+79000000000', 'department_id': selection['department_id']}, token=admin)
occupancy_query = urllib.parse.urlencode({'from': selection['starts_at'], 'until': selection['ends_at'], 'resource_id': selection['resource_ids'][0]})
call('GET', '/api/v1/admin/departments/' + selection['department_id'] + '/occupancy?' + occupancy_query, '/api/v1/admin/departments/{departmentID}/occupancy', token=admin)
admin_order = call('GET', f'/api/v1/admin/orders/{oid}', '/api/v1/admin/orders/{orderID}', token=admin)
call('GET', f'/api/v1/admin/orders/{oid}/payments', '/api/v1/admin/orders/{orderID}/payments', token=admin)
call('GET', f'/api/v1/admin/orders/{oid}/history', '/api/v1/admin/orders/{orderID}/history', token=admin)
if admin_order['payment']['status'] != 'accepted' or admin_order['payment']['paid_kopecks'] != admin_order['amount_kopecks'] or not admin_order['payment']['test_mode']:
    raise SystemExit('Administrative payment summary does not match the confirmed order')
for kind, field, version in [('schedules', 'schedule', 'schedule_version'), ('tariffs', 'tariffs', 'tariff_version')]:
    path = '/api/v1/admin/' + kind + '/b1-male-general'
    template = '/api/v1/admin/' + kind + '/{departmentID}'
    config = call('GET', path, template, token=admin)
    call('PUT', path, template, body={'version': config[version], field: config[field]}, token=admin)
    if kind == 'schedules':
        call('POST', '/api/v1/admin/departments/b1-male-general/schedule-preview', '/api/v1/admin/departments/{departmentID}/schedule-preview', body={'schedule': config['schedule']}, token=admin)
    else:
        call('POST', '/api/v1/admin/departments/b1-male-general/tariff-preview', '/api/v1/admin/departments/{departmentID}/tariff-preview', body={'tariffs': config['tariffs'], 'selection': selection}, token=admin)
block_selection = public_free_selection()
call('GET', '/api/v1/admin/resource-blocks?department_id=b1-male-general', '/api/v1/admin/resource-blocks', token=admin)
block = call('POST', '/api/v1/admin/resource-blocks', body={'department_id': block_selection['department_id'], 'resource_ids': block_selection['resource_ids'], 'starts_at': block_selection['starts_at'], 'ends_at': block_selection['ends_at'], 'reason': 'Синтетическая проверка API'}, token=admin, want=201)
block = call('PATCH', f'/api/v1/admin/resource-blocks/{block["id"]}', '/api/v1/admin/resource-blocks/{blockID}', body={'version': block['version'], 'reason': 'Исправленная причина'}, token=admin)
call('DELETE', f'/api/v1/admin/resource-blocks/{block["id"]}?version={block["version"]}', '/api/v1/admin/resource-blocks/{blockID}', token=admin)
release_block = call('POST', '/api/v1/admin/resource-blocks', body={'department_id': block_selection['department_id'], 'resource_ids': block_selection['resource_ids'], 'starts_at': block_selection['starts_at'], 'ends_at': block_selection['ends_at'], 'reason': 'Адресное снятие API'}, token=admin, want=201)
call('POST', f'/api/v1/admin/resource-blocks/{release_block["id"]}/release', '/api/v1/admin/resource-blocks/{blockID}/release', body={'version': release_block['version'], 'resource_ids': release_block['resource_ids'], 'starts_at': release_block['starts_at'], 'ends_at': release_block['ends_at']}, token=admin)
info_path = '/api/v1/admin/departments/b2-shower-large/info'
info_template = '/api/v1/admin/departments/{departmentID}/info'
info = call('GET', info_path, info_template, token=admin)
call('PUT', info_path, info_template, body={'version': info['version'], 'description': info['description']}, token=admin)
call('GET', '/api/v1/admin/audit?limit=10', '/api/v1/admin/audit', token=admin)

# Create a complete new department and exercise all administrative contracts.
call('GET', '/api/v1/admin/baths', token=admin)
bath = call('POST', '/api/v1/admin/baths', body={'name': 'Проверка Swagger', 'address': 'Екатеринбург, Тестовая, 1', 'description': 'Синтетические данные', 'active': True}, token=admin, want=201)
bid = bath['id']
bath = call('PATCH', f'/api/v1/admin/baths/{bid}', '/api/v1/admin/baths/{bathID}', body={'version': bath['version'], 'description': 'Проверено'}, token=admin)
call('GET', f'/api/v1/admin/baths/{bid}', '/api/v1/admin/baths/{bathID}', token=admin)
call('GET', f'/api/v1/admin/baths/{bid}/departments', '/api/v1/admin/baths/{bathID}/departments', token=admin)
source = call('GET', '/api/v1/departments/b1-male-general/booking-config', '/api/v1/departments/{departmentID}/booking-config')
setup = {field: source[field] for field in ('base_guests', 'max_guests', 'min_duration_minutes', 'duration_step_minutes', 'start_step_minutes', 'buffer_minutes', 'schedule', 'tariffs')}
setup['resources'] = [{'label': '1', 'x': 20, 'y': 20}]
department = call('POST', f'/api/v1/admin/baths/{bid}/departments', '/api/v1/admin/baths/{bathID}/departments', body={'name': 'Проверка отделения', 'mode': 'session', 'active': True, 'booking_setup': setup}, token=admin, want=202)
did = department['id']

def wait_department():
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        item = call('GET', '/api/v1/admin/departments/' + did, '/api/v1/admin/departments/{departmentID}', token=admin)
        if item['sync_status'] == 'ready':
            return item
        if item['sync_status'] == 'error':
            raise SystemExit('Catalog projection failed')
        time.sleep(0.25)
    raise SystemExit('Catalog projection timeout')

department = wait_department()
department = call('PATCH', '/api/v1/admin/departments/' + did, '/api/v1/admin/departments/{departmentID}', body={'version': department['version'], 'description': 'Изменено через API'}, token=admin, want=202)
department = wait_department()
call('POST', '/api/v1/admin/departments/' + did + '/sync-retry', '/api/v1/admin/departments/{departmentID}/sync-retry', body={'version': department['version'], 'reason': 'Проверка контракта повторной синхронизации'}, token=admin, want=202)
wait_department()
policy_path = f'/api/v1/admin/departments/{did}/booking-policy'
policy_template = '/api/v1/admin/departments/{departmentID}/booking-policy'
policy = call('GET', policy_path, policy_template, token=admin)
policy = call('PATCH', policy_path, policy_template, body={'version': policy['version'], 'max_guests': policy['max_guests'], 'online_sales_enabled': False, 'sales_paused_until': None, 'sales_pause_reason': 'Проверка паузы'}, token=admin)
paused = call('GET', '/api/v1/departments/' + did + '/booking-config', '/api/v1/departments/{departmentID}/booking-config')
if paused['online_sales_enabled'] or paused['sales_pause_reason'] != 'Проверка паузы':
    raise SystemExit('Public config did not publish the sales pause')
call('PATCH', policy_path, policy_template, body={'version': policy['version'], 'online_sales_enabled': True}, token=admin)
resource_path = f'/api/v1/admin/departments/{did}/resources'
resource_template = '/api/v1/admin/departments/{departmentID}/resources'
layout = call('GET', resource_path, resource_template, token=admin)
resource = call('POST', resource_path, resource_template, body={'scheme_version': layout['version'], 'label': '2', 'x': 80, 'y': 20}, token=admin, want=201)['resource']
call('PATCH', resource_path + '/' + resource['id'], resource_template + '/{resourceID}', body={'version': resource['version'], 'active': False}, token=admin)
call('GET', f'/api/v1/admin/departments/{did}/history', '/api/v1/admin/departments/{departmentID}/history', token=admin)
bath = call('GET', f'/api/v1/admin/baths/{bid}', '/api/v1/admin/baths/{bathID}', token=admin)
call('PATCH', f'/api/v1/admin/baths/{bid}', '/api/v1/admin/baths/{bathID}', body={'version': bath['version'], 'active': False}, token=admin, want=202)
wait_department()
call('DELETE', '/api/v1/admin/sessions/current', token=admin, want=204)
call('GET', '/api/v1/admin/orders', token=admin, want=401)
required = {(method, path) for path, item in spec['paths'].items() for method in item if method in ('get', 'post', 'put', 'patch', 'delete')}
missing = required - covered
if missing:
    raise SystemExit('Operations not checked: ' + ', '.join(f'{method.upper()} {path}' for method, path in sorted(missing)))
report = {'operations': len(covered), 'requests': len(results), 'rate_limited_retries': rate_limited_retries, 'results': results}
target = Path(os.environ.get('BANYA_TEST_REPORT_FILE', str(root / 'tmp/api-verification.json')))
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(json.dumps(report, ensure_ascii=False, indent=2))
print(f'Validated {len(covered)}/{len(required)} OpenAPI operations; {len(results)} real HTTP responses match their schemas.')

#!/usr/bin/env python3
"""Two synthetic buyers: real holds, Kafka test payments, JSON/PDF tickets.

Only loopback targets are accepted. Use on a disposable sizing environment.
No tokens or buyer details are printed or saved.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import json
from pathlib import Path
import ssl
import threading
import time
import urllib.parse
import urllib.request
import uuid
from zoneinfo import ZoneInfo

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', default='https://localhost:18443')
parser.add_argument('--ca-file')
parser.add_argument('--orders-per-user', type=int, default=8)
parser.add_argument('--report', type=Path)
args = parser.parse_args()
if urllib.parse.urlsplit(args.url).hostname not in {'localhost', '127.0.0.1', '::1'}:
    parser.error('Only isolated loopback targets are accepted')
if not 1 <= args.orders_per_user <= 10:
    parser.error('orders-per-user must be 1..10 to stay below the normal rate limits')
context = ssl.create_default_context(cafile=args.ca_file)
latencies = []
lock = threading.Lock()


def call(method, path, body=None, token=None, binary=False):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if method in {'POST', 'PUT', 'PATCH'}:
        headers['X-Idempotency-Key'] = str(uuid.uuid4())
    request = urllib.request.Request(
        args.url.rstrip('/') + path,
        data=json.dumps(body).encode() if body is not None else None,
        headers=headers, method=method,
    )
    started = time.monotonic()
    with urllib.request.urlopen(request, timeout=30, context=context) as response:
        data = response.read()
    with lock:
        latencies.append(time.monotonic() - started)
    return data if binary else json.loads(data)


def checkout(department):
    date = (dt.datetime.now(ZoneInfo('Asia/Yekaterinburg')) + dt.timedelta(days=1)).date().isoformat()
    availability = call('GET', f'/api/v1/departments/{department}/availability?date={date}')
    selection = None
    for slot in availability['items']:
        free = [r for r in slot['resources'] if r['state'] == 'free']
        if free:
            selection = {'department_id': department, 'resource_ids': [free[0]['id']],
                         'starts_at': slot['starts_at'], 'ends_at': slot['ends_at'],
                         'guests': [{'category': 'adult', 'count': 1}]}
            break
    if selection is None:
        raise RuntimeError('No free future session in isolated test data')
    created = call('POST', '/api/v1/orders', {'selection': selection, 'buyer': {'name': 'Sizing Test', 'phone': '+79000000000'}})
    oid, token = created['order']['id'], created['order_token']
    order = call('POST', f'/api/v1/orders/{oid}/hold', {}, token)['order']
    quote = order['quote']
    attempt = call('POST', f'/api/v1/orders/{oid}/payments', {
        'amount_kopecks': quote['amount_kopecks'], 'currency': 'RUB',
        'price_hash': quote['price_hash'], 'outcome': 'success',
    }, token)
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        order = call('GET', f'/api/v1/orders/{oid}', token=token)
        attempt = call('GET', f'/api/v1/orders/{oid}/payments/{attempt["id"]}', token=token)
        if order['status'] == 'confirmed' and attempt['status'] == 'accepted':
            break
        time.sleep(0.5)
    else:
        raise RuntimeError('Kafka payment confirmation did not finish within 60 seconds')
    ticket = urllib.parse.urlsplit(order['ticket_url']).path.rsplit('/', 1)[-1]
    call('GET', '/api/v1/tickets/' + ticket)
    pdf = call('GET', '/api/v1/tickets/' + ticket + '/pdf', binary=True)
    if not pdf.startswith(b'%PDF-'):
        raise RuntimeError('Ticket PDF is invalid')


def buyer(department):
    for _ in range(args.orders_per_user):
        checkout(department)
    return args.orders_per_user


started = time.monotonic()
for path in ['/', '/admin/login', '/swagger/', '/openapi.yaml']:
    call('GET', path, binary=True)
with ThreadPoolExecutor(max_workers=2) as executor:
    results = list(executor.map(buyer, ['b1-male-general', 'b1-female-general']))
ordered = sorted(latencies)
report = {
    'simultaneous_buyers': 2, 'confirmed_orders': sum(results),
    'pdf_tickets': sum(results), 'successful_http_requests': len(latencies),
    'elapsed_seconds': round(time.monotonic() - started, 2),
    'http_p95_ms': round(ordered[min(len(ordered) - 1, int(len(ordered) * 0.95))] * 1000, 1),
    'http_max_ms': round(max(ordered) * 1000, 1),
}
if args.report:
    args.report.write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))

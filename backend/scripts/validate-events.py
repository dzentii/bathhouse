#!/usr/bin/env python3
"""Validate persisted events from all producer outboxes against the Kafka contract."""
import json
import subprocess
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker

root = Path(__file__).resolve().parents[1]
schema = json.loads((root / 'api/events.v1.schema.json').read_text())
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema, format_checker=FormatChecker())
total = 0
for service in ('catalog', 'booking', 'payment'):
    result = subprocess.run(['docker', 'compose', 'exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', service, '-tAc', 'SELECT payload FROM outbox ORDER BY created_at,id'], cwd=root, text=True, capture_output=True, check=True)
    count = 0
    for line in result.stdout.splitlines():
        if not line.strip():
            continue
        event = json.loads(line)
        errors = list(validator.iter_errors(event))
        if errors:
            raise SystemExit(f'{service}: event {event.get("type", "unknown")} violates Kafka schema')
        count += 1
    total += count
    print(f'{service}: {count} persisted event envelopes validated')
print(f'Total: {total} events match the Kafka contract')

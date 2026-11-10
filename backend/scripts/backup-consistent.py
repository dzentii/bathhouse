#!/usr/bin/env python3
"""Take a cross-service checkpoint with a short planned write outage.

Only services running before this command are restarted. Infrastructure is never recreated.
"""
import json
import subprocess
from pathlib import Path

root=Path(__file__).resolve().parents[1]
def compose(*args,capture=False):
    return subprocess.run(['docker','compose',*args],cwd=root,text=True,capture_output=capture,check=True)

result=compose('ps','--format','json',capture=True)
services=[]
text=result.stdout.strip()
if text.startswith('['):items=json.loads(text)
else:items=[json.loads(line) for line in text.splitlines() if line.strip()]
for item in items:
    if item.get('Service') in {'catalog','booking','payment','identity'} and item.get('State')=='running':services.append(item['Service'])
try:
    if services:compose('stop',*services)
    compose('--profile','ops','run','--rm','--no-deps','backup','once')
finally:
    if services:compose('start','--wait',*services)
print('Consistent checkpoint completed; previously running services restored')

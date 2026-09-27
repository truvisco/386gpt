#!/usr/bin/env python3
"""Authenticated native gateway checks without printing credentials or transcripts."""
import argparse
import json
from pathlib import Path
import time
import urllib.request
import uuid
p=argparse.ArgumentParser();p.add_argument('origin');p.add_argument('key_file',type=Path);a=p.parse_args()
key=a.key_file.read_text().strip();chat='isolation-'+uuid.uuid4().hex
headers={'Authorization':'Bearer '+key,'X-Hermes-Session-Key':'agent:main:386gpt:dm:owner:thread:'+chat,'Content-Type':'application/json'}
def request(path,data=None,extra=None):
 req=urllib.request.Request(a.origin+path,data=json.dumps(data).encode() if data is not None else None,headers={**headers,**(extra or {})})
 with urllib.request.urlopen(req,timeout=30) as r:return json.load(r)
caps=request('/v1/capabilities')
assert caps['runtime']['transport']=='messaging_gateway'
assert caps['runtime']['cwd']=='/workspace'
for text,expected in [('/status','Hermes Gateway Status'),('Reply with exactly SANDBOX_READY.','SANDBOX_READY'),('Use the terminal tool to run: id -u; pwd. Report the actual output.','10001')]:
 rid=uuid.uuid4().hex
 reply=request('/v1/runs',{'input':text},{'Idempotency-Key':rid})
 run_id=reply.get('run_id') or reply.get('id') or reply.get('run',{}).get('run_id')
 if not run_id:raise AssertionError('Missing receipt ID: '+str(list(reply)))
 deadline=time.monotonic()+240
 completed_at=None
 while time.monotonic()<deadline:
  result=request('/v1/runs/'+run_id)
  if result['status']=='completed':
   # Native command delivery can follow its processing-complete callback.
   if expected in result.get('output',''): break
   if completed_at is None: completed_at=time.monotonic()
   if time.monotonic()-completed_at>10: break
  elif result['status'] not in ('running','queued','waiting_for_approval','stopping'):break
  if result['status']=='waiting_for_approval':raise AssertionError('Harmless smoke command unexpectedly requires approval')
  time.sleep(2)
 assert result['status']=='completed',result.get('error',result['status'])
 assert expected in result.get('output',''), 'Missing expected reply for '+text
 print('PASS:', 'native command' if text=='/status' else 'model reply' if expected=='SANDBOX_READY' else 'terminal reports sandbox UID')

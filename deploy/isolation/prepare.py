#!/usr/bin/env python3
"""Prepare reviewed configuration and a safe state snapshot, never mount a host profile.
Run with the existing Hermes venv; the source gateway must be stopped for final cutover.
"""
import argparse
import os
from pathlib import Path
import secrets
import tarfile
import time
import yaml
from dotenv import dotenv_values

p=argparse.ArgumentParser()
p.add_argument('--profile',required=True,type=Path)
p.add_argument('--output',required=True,type=Path)
p.add_argument('--bind',required=True)
p.add_argument('--upstream',required=True)
a=p.parse_args()
os.umask(0o077)
a.output.mkdir(parents=True,exist_ok=True)
a.output.chmod(0o700)
source=a.profile.resolve()
config=yaml.safe_load((source/'config.yaml').read_text())
env=dotenv_values(source/'.env')
provider_key=env.get('UNSLOTH_API_KEY')
if not provider_key:
    raise SystemExit('Missing Unsloth key in source profile; refusing to copy unrelated credentials')
if (a.output/'config.yaml').exists():
    raise SystemExit('Prepared configuration already exists; retain its keys and use snapshot.py for a fresh state migration')
# Archive only: this backup is never mounted in a container.
with tarfile.open(a.output/f'profile-backup-{int(time.time())}.tar.gz','w:gz',dereference=False) as archive:
    archive.add(source,arcname=source.name,filter=lambda t: None if t.name.endswith(('.sock',)) else t)
external,internal=secrets.token_hex(32),secrets.token_hex(32)
# Only reviewed declarative settings survive. No external tools/hooks/MCP/SSH settings.
reviewed={k:config[k] for k in ('model','custom_providers','auxiliary','agent','display','compression') if k in config}
model=reviewed.setdefault('model',{})
if model.get('provider')!='unsloth': raise SystemExit('Expected user-selected Unsloth provider')
model['base_url']='http://model:8080/v1';model['api_key']='sandbox-proxy'
reviewed['custom_providers']=[{**provider,'base_url':'http://model:8080/v1','key_env':'UNSLOTH_API_KEY'} for provider in config.get('custom_providers',[]) if provider.get('name')=='unsloth']
for provider in reviewed['custom_providers']: provider.pop('api_key',None)
reviewed['terminal']={'backend':'local','cwd':'/workspace'}
reviewed['plugins']={'enabled':['386gpt-channel']}
reviewed['platforms']={'chat386':{'enabled':True,'extra':{'host':'0.0.0.0','port':8644,'key':internal,'owner':'386gpt-owner'}}}
reviewed['platform_toolsets']={'chat386':config.get('platform_toolsets',{}).get('chat386',['hermes-telegram'])}
# Preserve the actual home channel selection (including its absence).
home=config.get('platforms',{}).get('chat386',{}).get('home_channel')
if home: reviewed['platforms']['chat386']['home_channel']=home
(a.output/'config.yaml').write_text(yaml.safe_dump(reviewed,sort_keys=False))
(a.output/'profile.env').write_text('UNSLOTH_API_KEY=sandbox-proxy\nCHAT386_ALLOWED_USERS=386gpt-owner\nAPI_SERVER_ENABLED=false\n')
for name,value in [('gateway.key',external),('internal.key',internal),('provider.key',provider_key)]:
    (a.output/name).write_text(value+'\n')
# World-readable individual files are under a 0700 host directory; read-only mounts
# must be readable by the container's unrelated non-root UIDs.
for name in ['config.yaml','profile.env','gateway.key','internal.key','provider.key']:(a.output/name).chmod(0o444)
(a.output/'compose.env').write_text(f'ISOLATION_CONFIG={a.output.resolve()}\nGATEWAY_BIND={a.bind}\nUNSLOTH_UPSTREAM={a.upstream}\n')
print('Prepared isolated configuration and private backup; secret values hidden.')

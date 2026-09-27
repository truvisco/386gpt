#!/usr/bin/env python3
"""Add a private account provisioner to an existing isolated installation."""
import argparse
import os
from pathlib import Path
import secrets
import yaml

p=argparse.ArgumentParser()
p.add_argument('--settings',type=Path,required=True)
p.add_argument('--client',type=Path,required=True)
p.add_argument('--url',required=True)
a=p.parse_args()
os.umask(0o077)
key=a.settings/'broker.key'
if not key.exists():key.write_text(secrets.token_hex(32)+'\n')
key.chmod(0o600)
c=yaml.safe_load(a.client.read_text())
c['tenants']={'base_url':a.url,'api_key':key.read_text().strip()}
temp=a.client.with_suffix('.accounts.tmp');temp.write_text(yaml.safe_dump(c));temp.chmod(0o600);temp.replace(a.client)
print('Account provisioner configured; credentials hidden.')

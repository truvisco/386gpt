#!/usr/bin/env python3
"""Update only the selected runtime connection; never print a transport key."""
import argparse
import os
from pathlib import Path
import shutil
import yaml
p=argparse.ArgumentParser();p.add_argument('client',type=Path);p.add_argument('key',type=Path);p.add_argument('--runtime',choices=['local','crash'],required=True);p.add_argument('--url',required=True);a=p.parse_args()
os.umask(0o077)
c=yaml.safe_load(a.client.read_text())
backup=a.client.with_suffix(a.client.suffix+'.before-isolation')
if not backup.exists():shutil.copy2(a.client,backup);backup.chmod(0o600)
entry=c['runtimes'][a.runtime] if 'runtimes' in c else c['agent']
entry.update(base_url=a.url,api_key=a.key.read_text().strip())
temp=a.client.with_suffix('.isolation.tmp');temp.write_text(yaml.safe_dump(c));temp.chmod(0o600);temp.replace(a.client)
print('Runtime connection updated; credentials hidden.')

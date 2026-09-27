#!/usr/bin/env python3
"""Install only a messaging transport into an existing, user-owned profile."""
import argparse
import os
from pathlib import Path
import shutil
import yaml

p = argparse.ArgumentParser()
p.add_argument('--profile', required=True)
p.add_argument('--host', default='127.0.0.1')
p.add_argument('--port', type=int, default=8644)
p.add_argument('--client', default=str(Path.home()/'.hermes/386gpt.yaml'))
a = p.parse_args()
home = Path.home()/'.hermes/profiles'/a.profile
config_path = home/'config.yaml'
config = yaml.safe_load(config_path.read_text())
client = yaml.safe_load(Path(a.client).read_text())
connection = client.get('agent') or client['runtimes']['local']
plugin = home/'plugins/386gpt-channel'
shutil.copytree(Path(__file__).parent/'386gpt-channel', plugin, dirs_exist_ok=True, ignore=shutil.ignore_patterns('__pycache__'))
backup = config_path.with_suffix('.yaml.before-channel')
if not backup.exists(): shutil.copy2(config_path, backup)
config.setdefault('plugins', {}).setdefault('enabled', [])
if '386gpt-channel' not in config['plugins']['enabled']: config['plugins']['enabled'].append('386gpt-channel')
config.setdefault('platforms', {})['chat386'] = {'enabled': True, 'extra': {'host':a.host,'port':a.port,'key':connection['api_key'],'owner':'386gpt-owner'}}
# Match the normal messaging toolset; provider, model, prompts, memory, and
# existing tool choices are owned by Hermes and are never regenerated here.
config.setdefault('platform_toolsets', {}).setdefault('chat386', ['hermes-telegram'])
config_path.write_text(yaml.safe_dump(config, sort_keys=False)); config_path.chmod(0o600)
env_path = home/'.env'
env = env_path.read_text() if env_path.exists() else ''
lines = [line for line in env.splitlines() if not line.startswith('CHAT386_ALLOWED_USERS=')]
env_path.write_text('\n'.join(lines+['CHAT386_ALLOWED_USERS=386gpt-owner'])+'\n'); env_path.chmod(0o600)
print(f'Installed chat386 into profile {a.profile} on {a.host}:{a.port}; model settings preserved.')

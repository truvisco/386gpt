#!/usr/bin/env python3
import argparse
import os
from pathlib import Path
import plistlib
import subprocess
p=argparse.ArgumentParser();p.add_argument('settings',type=Path);a=p.parse_args()
root=Path(__file__).resolve().parents[2]
label='co.truvis.386gpt.hermes'
logs=Path.home()/'Library/Logs/386gpt-hermes';logs.mkdir(parents=True,exist_ok=True)
plist=Path.home()/'Library/LaunchAgents'/f'{label}.plist'
content={'Label':label,'ProgramArguments':['/bin/sh',str(root/'deploy/isolation/supervise-local.sh'),str(root),str(a.settings.resolve())],'RunAtLoad':True,'KeepAlive':True,'ThrottleInterval':30,'StandardOutPath':str(logs/'container.log'),'StandardErrorPath':str(logs/'container.log')}
domain=f'gui/{os.getuid()}'
subprocess.run(['launchctl','bootout',f'{domain}/{label}'],capture_output=True)
plist.write_bytes(plistlib.dumps(content));plist.chmod(0o600)
subprocess.run(['launchctl','bootstrap',domain,str(plist)],check=True)

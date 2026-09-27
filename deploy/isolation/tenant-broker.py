#!/usr/bin/env python3
"""Private control plane: only the backend can provision fixed, isolated Compose stacks.
No shell, image, path, bind mount or command is accepted from API callers.
"""
import argparse
import copy
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import yaml

ACCOUNT = re.compile(r'[a-f0-9]{64}')

class Broker:
    def __init__(self, root, template, compose, bind, upstream, port_start=19000, limit=8, runner=subprocess.run):
        ip = ipaddress.ip_address(bind)
        if not (ip.is_loopback or ip in ipaddress.ip_network('100.64.0.0/10')):
            raise ValueError('Broker and gateways must bind to loopback or Tailscale')
        if not 1 <= limit <= 64 or not 1024 <= port_start <= 65535-limit:
            raise ValueError('Invalid account capacity or port range')
        self.root, self.template, self.compose = Path(root), Path(template), Path(compose).resolve()
        self.bind, self.upstream, self.port_start, self.limit = bind, upstream, port_start, limit
        self.runner, self.lock = runner, threading.Lock()
        self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.root.chmod(0o700)
        self.ready = set()

    def prepare(self, account):
        if not ACCOUNT.fullmatch(account):
            raise ValueError('Invalid account ID')
        target = self.root / account
        if target.is_symlink():
            raise ValueError('Account directory cannot be a symlink')
        manifest = target / 'connection.json'
        if manifest.exists():
            return json.loads(manifest.read_text())
        # Reserve a durable port before Compose starts. Incomplete preparations
        # retain their slot, so a retry cannot collide with another account.
        occupied = [p for p in self.root.iterdir() if ACCOUNT.fullmatch(p.name)]
        if target not in occupied and len(occupied) >= self.limit:
            raise OverflowError('Account environment capacity reached')
        used = {int((p/'port').read_text()) for p in occupied if (p/'port').exists()}
        target.mkdir(mode=0o700, exist_ok=True)
        port_file = target/'port'
        port = int(port_file.read_text()) if port_file.exists() else next(p for p in range(self.port_start,self.port_start+self.limit) if p not in used)
        port_file.write_text(str(port))
        config = copy.deepcopy(yaml.safe_load((self.template/'config.yaml').read_text()))
        external, internal = secrets.token_hex(32), secrets.token_hex(32)
        identity = 'account:'+account
        # New accounts never inherit home channel, credentials, sessions, cron,
        # plugins or a workspace from an existing account.
        config['platforms'] = {'chat386': {'enabled':True, 'extra':{'host':'0.0.0.0','port':8644,'key':internal,'owner':identity}}}
        config['terminal'] = {'backend':'local','cwd':'/workspace'}
        config['plugins'] = {'enabled':['386gpt-channel']}
        for name,value in {
            'config.yaml':yaml.safe_dump(config,sort_keys=False),
            'profile.env':f'UNSLOTH_API_KEY=sandbox-proxy\nCHAT386_ALLOWED_USERS={identity}\nAPI_SERVER_ENABLED=false\n',
            'gateway.key':external+'\n', 'internal.key':internal+'\n',
            'provider.key':(self.template/'provider.key').read_text(),
        }.items():
            (target/name).write_text(value)
            (target/name).chmod(0o444)
        project = '386gpt-account-'+account
        (target/'compose.env').write_text(f'ISOLATION_CONFIG={target.resolve()}\nGATEWAY_BIND={self.bind}\nGATEWAY_PORT={port}\nUNSLOTH_UPSTREAM={self.upstream}\nHERMES_STATE_VOLUME={project}_state\n')
        connection = {'account_id':account, 'base_url':f'http://{self.bind}:{port}', 'api_key':external, 'session_key':identity}
        temporary = target/'connection.tmp'
        temporary.write_text(json.dumps(connection))
        temporary.chmod(0o600)
        temporary.replace(manifest)
        return connection

    def ensure(self, account):
        # Serialize first provisioning and port allocation, including retries.
        with self.lock:
            connection = self.prepare(account)
            if account not in self.ready:
                result = self.runner(['docker','compose','--project-name','386gpt-account-'+account,
                    '--env-file',str(self.root/account/'compose.env'),'-f',str(self.compose),
                    'up','-d','--no-build','--wait','--wait-timeout','120'],
                    capture_output=True, timeout=135, check=False)
                if result.returncode:
                    # Do not echo Compose output: it may contain configuration.
                    raise RuntimeError('Account container startup failed')
                self.ready.add(account)
            return connection

    def reconcile(self):
        # Restore known stacks after a host/Docker restart, even when the API
        # stayed up and already cached their gateway connections.
        while True:
            for target in self.root.iterdir():
                if not ACCOUNT.fullmatch(target.name) or not (target/'connection.json').exists():
                    continue
                try:
                    names=['386gpt-account-'+target.name+'-'+service+'-1' for service in ('hermes','gateway','model','egress')]
                    result=self.runner(['docker','inspect','--format','{{.State.Running}}',*names],capture_output=True,text=True,timeout=15,check=False)
                    if result.returncode or result.stdout.split()!=['true']*4:
                        with self.lock:self.ready.discard(target.name)
                        self.ensure(target.name)
                except Exception:
                    pass  # Retry next pass without discarding persistent data.
            time.sleep(30)

class Handler(BaseHTTPRequestHandler):
    def do_PUT(self):
        if not hmac.compare_digest(self.headers.get('Authorization','').encode(), ('Bearer '+self.server.key).encode()):
            return self.reply(401, {'error':'Unauthorized'})
        account = self.path.removeprefix('/v1/accounts/')
        if self.path != '/v1/accounts/'+account or not ACCOUNT.fullmatch(account):
            return self.reply(404, {'error':'Not found'})
        if self.headers.get('Transfer-Encoding') or self.headers.get('Content-Length','0') != '0':
            return self.reply(400, {'error':'Request body is not accepted'})
        try:
            connection = self.server.broker.ensure(account)
        except OverflowError:
            return self.reply(503, {'error':'Account environment capacity reached'})
        except Exception:
            return self.reply(503, {'error':'Account environment unavailable'})
        self.reply(200,connection)

    def reply(self, status, body):
        data=json.dumps(body).encode()
        self.send_response(status)
        self.send_header('Content-Type','application/json')
        self.send_header('Cache-Control','no-store')
        self.send_header('Content-Length',str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_):
        pass

if __name__=='__main__':
    os.umask(0o077)
    p=argparse.ArgumentParser()
    p.add_argument('--root',type=Path,required=True)
    p.add_argument('--template',type=Path,required=True)
    p.add_argument('--compose',type=Path,required=True)
    p.add_argument('--key-file',type=Path,required=True)
    p.add_argument('--bind',required=True)
    p.add_argument('--upstream',required=True)
    p.add_argument('--port',type=int,default=8650)
    p.add_argument('--port-start',type=int,default=19000)
    p.add_argument('--limit',type=int,default=8)
    a=p.parse_args()
    broker=Broker(a.root,a.template,a.compose,a.bind,a.upstream,a.port_start,a.limit)
    key=a.key_file.read_text().strip()
    if len(key)<32:raise SystemExit('A dedicated random broker key is required')
    server=ThreadingHTTPServer((a.bind,a.port),Handler)
    server.broker,server.key=broker,key
    threading.Thread(target=broker.reconcile,daemon=True).start()
    server.serve_forever()

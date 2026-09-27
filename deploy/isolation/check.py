"""Run inside Hermes: test isolation without reading any host credential contents."""
import os
from pathlib import Path
import socket
import subprocess
import urllib.request
import urllib.error
assert os.getuid()==10001
status=Path('/proc/self/status').read_text()
assert 'CapEff:\t0000000000000000' in status and 'NoNewPrivs:\t1' in status
for path in ['/var/run/docker.sock','/run/docker.sock','/Users/grimlock','/home/grimlock','/root/.ssh']:
    try: exists=Path(path).exists()
    except PermissionError: exists=False
    assert not exists, path
for path in ['/opt/hermes/ISOLATION_TEST','/state/profile/config.yaml','/etc/ISOLATION_TEST']:
    try:
        fd=os.open(path,os.O_WRONLY|os.O_CREAT,0o600)
    except OSError: pass
    else:
        os.close(fd); raise AssertionError(f'Writable protected path: {path}')
for host,port in [('192.168.68.180',22),('100.74.13.43',8888),('169.254.169.254',80),('1.1.1.1',443),('host.docker.internal',80)]:
    try: connection=socket.create_connection((host,port),timeout=2)
    except OSError: pass
    else: connection.close();raise AssertionError(f'Direct egress allowed: {host}:{port}')
for address in ['http://127.0.0.1/','http://192.168.68.180/','http://100.74.13.43/','http://169.254.169.254/','http://[::1]/','http://127.0.0.1.nip.io/']:
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({'http':'http://egress:8080'}))
    # urllib normally bypasses proxies for loopback; explicitly force the proxy request.
    request=urllib.request.Request(address);request.set_proxy('egress:8080','http')
    try: opener.open(request,timeout=10)
    except urllib.error.HTTPError as error: assert error.code==403, error.code
    else: raise AssertionError(f'Proxy allowed {address}')
assert urllib.request.urlopen('https://example.com',timeout=30).status==200
assert urllib.request.urlopen('http://model:8080/v1/models',timeout=30).status==200
subprocess.run(['git','ls-remote','https://github.com/truvisco/386gpt.git','HEAD'],check=True,stdout=subprocess.DEVNULL,timeout=60)
Path('/workspace/isolation-check.txt').write_text('Persistent sandbox workspace\n')
print('PASS: non-root, no capabilities, no privilege escalation, protected mounts, direct/private network denial, public HTTPS, Git, Unsloth, workspace write.')

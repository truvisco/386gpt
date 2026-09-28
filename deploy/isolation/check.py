"""Run inside Hermes: test isolation without reading any host credential contents."""
import os
from pathlib import Path
import socket
import subprocess
import urllib.request
import urllib.error
root_check = "--root" in __import__("sys").argv
assert os.getuid()==(0 if root_check else 10001)
status=Path('/proc/self/status').read_text()
assert 'NoNewPrivs:\t0' in status and 'Seccomp:\t2' in status
allowed=sum(1<<bit for bit in (0,1,3,4,5,6,7,29,31))
fields=dict(line.split(':',1) for line in status.splitlines() if ':' in line)
assert int(fields['CapBnd'].strip(),16)==allowed
if not root_check: assert int(fields['CapEff'].strip(),16)==0
for path in ['/var/run/docker.sock','/run/docker.sock','/Users/grimlock','/home/grimlock']:
    try: exists=Path(path).exists()
    except PermissionError: exists=False
    assert not exists, path
protected=['/state/profile/config.yaml','/state/profile/.env']
if not root_check: protected+=['/opt/hermes/ISOLATION_TEST','/etc/ISOLATION_TEST']
for path in protected:
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
if not root_check: Path('/workspace/isolation-check.txt').write_text('Persistent sandbox workspace\n')
print('PASS:', 'sudo root' if root_check else 'normal user', 'bounded capabilities, seccomp, read-only config mounts, host/private network denial, public HTTPS, Git, Unsloth, workspace write.')

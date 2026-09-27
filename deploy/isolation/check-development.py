"""Run as the normal Hermes user in a disposable or staged environment."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

assert os.getuid()==10001
assert subprocess.check_output(['sudo','-n','id','-u'],text=True).strip()=='0'
for command in ('git','curl','wget','gcc','g++','make','cmake','ninja','pkg-config','python','pip','node','npm','go','jq','sqlite3','rg','rsync','ssh','zip','unzip','nano','vi','tree'):
    assert shutil.which(command),command
with tempfile.TemporaryDirectory(dir='/workspace',prefix='.dev-check-') as directory:
    root=Path(directory)
    (root/'hello.c').write_text('#include <stdio.h>\nint main(void) { puts("compiled"); return 0; }\n')
    subprocess.run(['cc',str(root/'hello.c'),'-o',str(root/'hello')],check=True)
    assert subprocess.check_output([str(root/'hello')],text=True).strip()=='compiled'
    subprocess.run(['python','-m','venv',str(root/'venv')],check=True)
    subprocess.run([str(root/'venv/bin/python'),'-m','pip','--version'],check=True,stdout=subprocess.DEVNULL)
    assert subprocess.check_output(['node','-p','1+1'],text=True).strip()=='2'
    subprocess.run(['go','version'],check=True,stdout=subprocess.DEVNULL)
# Sudo must retain the account's egress proxy for apt/curl/package installers.
for key in ('HTTP_PROXY','HTTPS_PROXY','http_proxy','https_proxy'):
    if key in os.environ:
        actual=subprocess.check_output(['sudo','-n','printenv',key],text=True).strip()
        assert actual==os.environ[key],key
print('PASS: passwordless container sudo, C compilation, Python venv/pip, Node, Go, development tools, proxy preservation')

#!/usr/bin/env python3
"""Switch only the transport endpoint, preserving all connection credentials."""
from pathlib import Path
import shutil
import sys
import yaml
p = Path(sys.argv[1]).expanduser()
mode = p.stat().st_mode & 0o777
d = yaml.safe_load(p.read_text())
backup = p.with_suffix(p.suffix+'.before-channel')
if not backup.exists(): shutil.copy2(p,backup)
for c in ([d['agent']] if 'agent' in d else d['runtimes'].values()):
    c['base_url'] = c['base_url'].replace(':8643', ':8644')
p.write_text(yaml.safe_dump(d));p.chmod(mode)
print('Client now uses the messaging gateway channel; credentials unchanged.')

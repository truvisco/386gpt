"""Initialize account state from the image and read-only configuration mounts."""
import os
from pathlib import Path
for directory in ('/state/home', '/state/profile', '/state/profile/plugins', '/state/profile/cache'):
    Path(directory).mkdir(parents=True, exist_ok=True)
for target, source in [('plugins/386gpt-channel', '/opt/channel')]:
    link = Path('/state/profile') / target
    if link.is_symlink() and str(link.readlink()) == source:
        continue
    if link.exists() or link.is_symlink():
        raise RuntimeError(f'Refusing to replace unexpected state file: {link}')
    link.symlink_to(source)
os.execv('/opt/venv/bin/python', ['python', '-m', 'hermes_cli.main', 'gateway', 'run'])

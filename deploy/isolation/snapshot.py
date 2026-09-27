#!/usr/bin/env python3
"""Export data only; do not import host executable/configuration state."""
import argparse
import json
import os
from pathlib import Path
import shutil
import sqlite3
p=argparse.ArgumentParser();p.add_argument('profile',type=Path);p.add_argument('output',type=Path);a=p.parse_args()
os.umask(0o077)
a.output.mkdir(parents=True,exist_ok=False)
profile=a.output/'profile';profile.mkdir()
# Fresh runtime/tool sessions force cwd=/workspace instead of resurrecting host paths.
for name in ['state.db','386gpt-channel.db']:
 source=a.profile/name
 if source.is_file() and not source.is_symlink():
  with sqlite3.connect(f'file:{source}?mode=ro',uri=True) as src,sqlite3.connect(profile/name) as dst: src.backup(dst)
for name in ['sessions','memories']:
 source=a.profile/name
 if source.is_dir() and not source.is_symlink():
  for item in source.rglob('*'):
   if item.is_symlink() or any(parent.is_symlink() for parent in item.parents): continue
   if item.is_file() and item.suffix in ('.json','.jsonl','.md','.txt'):
    dest=profile/item.relative_to(a.profile);dest.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(item,dest)
print('Exported session data and memory; no credentials, executable hooks, or terminal state.')

home=a.profile/'home-channel.json'
if home.is_file() and not home.is_symlink():
    shutil.copyfile(home, profile/'home-channel.json')

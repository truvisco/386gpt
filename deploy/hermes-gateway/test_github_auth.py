"""Credential lifecycle tests using a fake GitHub CLI; no external login required."""
import asyncio
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('github_auth', Path(__file__).parent/'386gpt-channel/github_auth.py')
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)

FAKE = r'''#!/usr/bin/env python3
import os,sys,time
from pathlib import Path
root=Path(os.environ['GH_CONFIG_DIR']);args=sys.argv[1:]
if args[:2]==['auth','login']:
 if '--web' in args:
  print('First copy your one-time code: ABCD-1234',flush=True)
  while not (Path.home()/'approve-device').exists(): time.sleep(.02)
  token='device-secret'
 else:
  token=sys.stdin.read().strip()
  if token=='invalid_token':
   print('invalid_token must never be returned',file=sys.stderr);sys.exit(1)
 root.mkdir(parents=True,exist_ok=True);(root/'hosts.yml').write_text('github.com:\n    oauth_token: '+token+'\n')
elif args[:2]==['api','user']:
 if not (root/'hosts.yml').exists():sys.exit(1)
 print('fixture-user')
elif args[:2]==['auth','logout']:
 (root/'hosts.yml').write_text('{}\n')
'''

class GitHubAuthTest(unittest.IsolatedAsyncioTestCase):
 async def asyncSetUp(self):
  self.temp=tempfile.TemporaryDirectory()
  self.home=Path(self.temp.name)
  binary=self.home/'bin';binary.mkdir();(binary/'gh').write_text(FAKE);(binary/'gh').chmod(0o755)
  self.env=patch.dict(os.environ, {'HOME':str(self.home),'PATH':str(binary)+os.pathsep+os.environ['PATH'], 'GH_TOKEN':'deployment-secret'})
  self.env.start();self.auth=module.GitHubAuth()
 async def asyncTearDown(self):
  await self.auth.cancel();self.env.stop();self.temp.cleanup()
 async def test_token_persists_private_and_uses_git_helper(self):
  result=await self.auth.token('github_pat_fixture_secret')
  self.assertEqual(result,{'state':'connected','login':'fixture-user'})
  self.assertNotIn('secret',json.dumps(result))
  self.assertEqual((self.auth.directory/'hosts.yml').stat().st_mode & 0o777,0o600)
  self.assertNotIn('GH_TOKEN', self.auth.environment())
  self.assertEqual((await module.GitHubAuth().status())['state'],'connected')
  config=(self.home/'.gitconfig').read_text()
  self.assertIn('git@github.com:',config)
  self.assertIn('ssh://git@github.com/',config)
  with self.assertRaisesRegex(ValueError,'GitHub authentication failed'):
   await self.auth.token('invalid_token')
  self.assertEqual((await self.auth.status())['state'],'connected')
  await self.auth.disconnect()
  self.assertEqual((await self.auth.status())['state'],'disconnected')
 async def test_device_code_completion_and_cancel(self):
  pending=await self.auth.start_device()
  self.assertEqual(pending['user_code'],'ABCD-1234')
  self.assertEqual(pending['verification_uri'],'https://github.com/login/device')
  self.assertEqual(await self.auth.start_device(),pending)
  await self.auth.disconnect()
  self.assertIsNone(self.auth.process)
  self.assertEqual((await self.auth.status())['state'],'disconnected')
  await self.auth.start_device()
  (self.home/'approve-device').touch()
  await asyncio.wait_for(self.auth.task,5)
  self.assertEqual((await self.auth.status())['state'],'connected')
  self.assertFalse(list(self.home.glob('.386gpt-gh-*')))
 async def test_accounts_do_not_share_credentials(self):
  await self.auth.token('github_pat_account_one')
  other_home=self.home/'other';other_home.mkdir()
  with patch.dict(os.environ,{'HOME':str(other_home)}):
   other=module.GitHubAuth()
   self.assertEqual((await other.status())['state'],'disconnected')
   await other.token('github_pat_account_two')
   await other.disconnect()
  self.assertEqual((await self.auth.status())['state'],'connected')

if __name__=='__main__':unittest.main()

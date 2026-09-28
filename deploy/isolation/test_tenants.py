import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
import yaml
import sys
sys.path.insert(0,str(Path(__file__).parent))

spec=importlib.util.spec_from_file_location('broker',Path(__file__).with_name('tenant-broker.py'))
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)

class Tenants(unittest.TestCase):
    def test_separate_credentials_volumes_and_retry(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);template=root/'template';template.mkdir()
            (template/'config.yaml').write_text(yaml.safe_dump({'model':{'provider':'unsloth'},'platforms':{'chat386':{'home_channel':'owner-private'}}}))
            (template/'provider.key').write_text('model-secret')
            commands=[]
            def runner(args,**kwargs):
                commands.append(args)
                return types.SimpleNamespace(returncode=1 if 'inspect' in args else 0, stdout='',stderr='no such container; no such volume')
            def broker():return module.Broker(root/'accounts',template,root/'compose.yaml','127.0.0.1','http://model',limit=2,runner=runner)
            b=broker();a=b.ensure('a'*64);c=b.ensure('b'*64)
            self.assertNotEqual(a['api_key'],c['api_key']);self.assertNotEqual(a['base_url'],c['base_url']);self.assertNotEqual(a['session_key'],c['session_key'])
            self.assertEqual(b.ensure('a'*64),a);self.assertEqual(len([c for c in commands if c[1]=='compose']),3)
            self.assertEqual(broker().ensure('a'*64),a)
            for account in ('a'*64,'b'*64):
                target=root/'accounts'/account
                env=(target/'compose.env').read_text()
                self.assertIn('HERMES_STATE_VOLUME=386gpt-account-'+account+'_state',env)
                config=yaml.safe_load((target/'config.yaml').read_text())
                self.assertNotIn('home_channel',config['platforms']['chat386'])
                self.assertEqual(config['platforms']['chat386']['extra']['owner'],'account:'+account)
                self.assertEqual(target.stat().st_mode & 0o777,0o700)
            self.assertNotEqual([c for c in commands if c[1]=='compose'][0][3],[c for c in commands if c[1]=='compose'][1][3])
            with self.assertRaises(OverflowError):b.ensure('c'*64)
            for invalid in ['../escape','x'*64,'a'*63,'a'*64+'/../b']:
                with self.assertRaises(ValueError):b.ensure(invalid)
    def test_failed_startup_keeps_credentials_for_retry(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);template=root/'template';template.mkdir()
            (template/'config.yaml').write_text('{}');(template/'provider.key').write_text('model')
            b=module.Broker(root/'accounts',template,root/'compose.yaml','127.0.0.1','http://model',runner=lambda *a,**kw:types.SimpleNamespace(returncode=1,stdout='',stderr='no such container; no such volume'))
            with self.assertRaises(RuntimeError):b.ensure('d'*64)
            old=json.loads((root/'accounts'/('d'*64)/'connection.json').read_text())
            b.runner=lambda args,**kw:types.SimpleNamespace(returncode=1 if 'inspect' in args else 0,stdout='',stderr='no such container; no such volume')
            self.assertEqual(b.ensure('d'*64),old)

if __name__=='__main__':unittest.main()

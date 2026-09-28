import json
from pathlib import Path
import tempfile
import types
import unittest

from workspace import ensure_workspace


class Workspace(unittest.TestCase):
    def test_missing_registered_directory_is_not_recreated(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            target = root / 'workspace'
            env = root / 'compose.env'
            env.write_text(f'HERMES_WORKSPACE={target}\n')
            with self.assertRaisesRegex(RuntimeError, 'missing or mismatched'):
                ensure_workspace('386gpt-isolated', env, target,
                                 lambda *a, **kw: self.fail('Must fail before Docker'))
            self.assertFalse(target.exists())

    def test_copy_failure_restarts_original_and_retains_volume(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            env = root / 'compose.env'
            env.write_text('EXISTING=value\n')
            calls = []
            def runner(args, **kwargs):
                calls.append(args)
                value = []
                if args[1:3] == ['container', 'inspect']:
                    value = [{'Mounts': [{'Destination': '/workspace', 'Type': 'volume',
                                         'Name': '386gpt-isolated_workspace'}],
                              'State': {'Running': True}}]
                elif args[1:3] == ['volume', 'inspect']:
                    value = [{'Labels': {'com.docker.compose.project': '386gpt-isolated'}}]
                return types.SimpleNamespace(returncode=int(args[1] == 'run'),
                                             stdout=json.dumps(value), stderr='')
            with self.assertRaisesRegex(RuntimeError, 'original volume retained'):
                ensure_workspace('386gpt-isolated', env, root / 'workspace', runner)
            self.assertIn(['docker', 'stop', '386gpt-isolated-hermes-1'], calls)
            self.assertEqual(calls[-1], ['docker', 'start', '386gpt-isolated-hermes-1'])
            self.assertFalse((root / 'workspace').exists())
            self.assertEqual(env.read_text(), 'EXISTING=value\n')
            self.assertFalse(any('rm' in call for call in calls))

    def test_copy_retry_uses_fresh_staging_after_source_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            target = root / 'workspace'
            env = root / 'compose.env'
            env.write_text('EXISTING=value\n')
            attempts = []
            source_files = {'renamed.txt': 'before'}

            def runner(args, **kwargs):
                value = []
                if args[1:3] == ['container', 'inspect']:
                    value = [{'Mounts': [{'Destination': '/workspace', 'Type': 'volume',
                                         'Name': '386gpt-isolated_workspace'}],
                              'State': {'Running': True}}]
                elif args[1:3] == ['volume', 'inspect']:
                    value = [{'Labels': {'com.docker.compose.project': '386gpt-isolated'}}]
                elif args[1] == 'run':
                    mount = next(arg for arg in args if arg.startswith('type=bind,'))
                    staging = Path(next(part[4:] for part in mount.split(',') if part.startswith('src=')))
                    attempts.append(staging)
                    if len(attempts) == 1:
                        (staging / 'obsolete.txt').write_text('partial copy')
                        return types.SimpleNamespace(returncode=1, stdout='', stderr='copy failed')
                    for name, contents in source_files.items():
                        (staging / name).write_text(contents)
                return types.SimpleNamespace(returncode=0, stdout=json.dumps(value), stderr='')

            with self.assertRaisesRegex(RuntimeError, 'original volume retained'):
                ensure_workspace('386gpt-isolated', env, target, runner)
            source_files = {'current.txt': 'after'}
            ensure_workspace('386gpt-isolated', env, target, runner)

            self.assertEqual(len(attempts), 2)
            self.assertNotEqual(attempts[0], attempts[1])
            self.assertFalse((target / 'obsolete.txt').exists())
            self.assertFalse((target / 'renamed.txt').exists())
            self.assertEqual((target / 'current.txt').read_text(), 'after')

    def test_inspection_failure_does_not_create_empty_workspace(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            env = root / 'compose.env'
            env.write_text('EXISTING=value\n')
            def runner(*args, **kwargs):
                return types.SimpleNamespace(returncode=1, stdout='', stderr='Docker daemon unavailable')
            with self.assertRaisesRegex(RuntimeError, 'Cannot inspect'):
                ensure_workspace('386gpt-isolated', env, root / 'workspace', runner)
            self.assertFalse((root / 'workspace').exists())

if __name__ == '__main__':
    unittest.main()

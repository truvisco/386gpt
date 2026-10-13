"""Per-account GitHub CLI authentication. Never return command output or tokens."""
import asyncio
import os
from pathlib import Path
import re
import shutil
import tempfile


class GitHubAuth:
    def __init__(self):
        self.directory = Path.home() / '.config' / 'gh'
        self.lock = asyncio.Lock()
        self.task = None
        self.process = None
        self.device = None
        self.error = None
        self.ready = asyncio.Event()

    def environment(self, directory=None):
        env = os.environ.copy()
        # Interactive account credentials must not inherit a deployment token.
        for name in ('GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN'):
            env.pop(name, None)
        env.update(GH_CONFIG_DIR=str(directory or self.directory), GH_BROWSER='/bin/true',
                   GH_PROMPT_DISABLED='1', GH_HOST='github.com', NO_COLOR='1')
        return env

    async def command(self, *args, directory=None, data=None):
        proc = await asyncio.create_subprocess_exec('gh', *args, env=self.environment(directory),
            stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL)
        try:
            out, _ = await asyncio.wait_for(proc.communicate(data), 15)
        except BaseException:
            if proc.returncode is None:
                proc.kill()
                await proc.wait()
            raise
        if proc.returncode:
            raise ValueError('GitHub authentication failed. Check the token permissions or try device sign-in.')
        return out.decode().strip()

    def configured(self):
        hosts = self.directory / 'hosts.yml'
        return hosts.is_file() and re.search(r'(?m)^github\.com:\s*', hosts.read_text()) is not None

    async def status(self):
        if not shutil.which('gh'):
            return {'state': 'unavailable', 'error': 'This workspace needs the updated image with GitHub CLI.'}
        if self.task and not self.task.done():
            return {'state': 'pending', **(self.device or {})}
        if self.error:
            return {'state': 'error', 'error': self.error}
        if not self.configured():
            return {'state': 'disconnected'}
        try:
            login = await self.command('api', 'user', '--jq', '.login')
            if not re.fullmatch(r'[A-Za-z0-9-]{1,39}', login):
                raise ValueError('Invalid GitHub username')
            return {'state': 'connected', 'login': login}
        except (ValueError, OSError, asyncio.TimeoutError):
            return {'state': 'error', 'error': 'Could not verify GitHub access. Check the connection or sign in again.'}

    async def commit(self, directory):
        login = await self.command('api', 'user', '--jq', '.login', directory=directory)
        if not re.fullmatch(r'[A-Za-z0-9-]{1,39}', login):
            raise ValueError('GitHub authentication could not be verified.')
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.directory.chmod(0o700)
        source = directory / 'hosts.yml'
        source.chmod(0o600)
        os.replace(source, self.directory / 'hosts.yml')
        await self.command('auth', 'setup-git', '--hostname', 'github.com')
        # SSH remotes use HTTPS too, through the existing egress proxy and gh helper.
        for prefix in ('git@github.com:', 'ssh://git@github.com/'):
            proc = await asyncio.create_subprocess_exec('git', 'config', '--global', '--replace-all',
                'url.https://github.com/.insteadOf', prefix, '^' + re.escape(prefix) + '$',
                stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.DEVNULL)
            if await proc.wait():
                raise ValueError('Could not configure GitHub HTTPS access.')
        self.error = None
        return {'state': 'connected', 'login': login}

    async def token(self, token):
        if not isinstance(token, str) or not re.fullmatch(r'[A-Za-z0-9_]{10,4096}', token):
            raise ValueError('Enter a valid GitHub personal access token.')
        async with self.lock:
            await self.cancel()
            with tempfile.TemporaryDirectory(prefix='.386gpt-gh-', dir=Path.home()) as temp:
                directory = Path(temp)
                await self.command('auth', 'login', '--hostname', 'github.com', '--git-protocol', 'https',
                                   '--with-token', directory=directory, data=(token+'\n').encode())
                return await self.commit(directory)

    async def start_device(self):
        async with self.lock:
            if self.task and not self.task.done():
                return await self.status()
            self.error = None
            self.device = None
            self.ready.clear()
            self.task = asyncio.create_task(self.run_device())
        try:
            await asyncio.wait_for(self.ready.wait(), 8)
        except asyncio.TimeoutError:
            pass
        return await self.status()

    async def run_device(self):
        try:
            with tempfile.TemporaryDirectory(prefix='.386gpt-gh-', dir=Path.home()) as temp:
                directory = Path(temp)
                self.process = await asyncio.create_subprocess_exec('gh', 'auth', 'login', '--hostname',
                    'github.com', '--git-protocol', 'https', '--web', env=self.environment(directory),
                    stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT)
                self.process.stdin.write(b'\n')
                await self.process.stdin.drain()
                self.process.stdin.close()
                async def consume():
                    async for line in self.process.stdout:
                        match = re.search(rb'\b[A-Z0-9]{4}-[A-Z0-9]{4}\b', line)
                        if match:
                            self.device = {'user_code': match.group().decode(), 'verification_uri': 'https://github.com/login/device'}
                            self.ready.set()
                    if await self.process.wait():
                        raise ValueError('Device authorization failed or expired. Start again.')
                    await self.commit(directory)
                await asyncio.wait_for(consume(), 900)
        except asyncio.CancelledError:
            raise
        except (ValueError, OSError, asyncio.TimeoutError):
            self.error = 'Device authorization failed or expired. Start again.'
        finally:
            if self.process and self.process.returncode is None:
                self.process.kill()
                await self.process.wait()
            self.process = None
            self.ready.set()

    async def cancel(self):
        if self.task and not self.task.done():
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
        self.task = None
        self.device = None
        self.error = None

    async def disconnect(self):
        async with self.lock:
            await self.cancel()
            if self.configured():
                await self.command('auth', 'logout', '--hostname', 'github.com', data=b'y\n')
            return {'state': 'disconnected'}

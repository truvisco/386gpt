"""Run with the dedicated Hermes venv; all state belongs to a temporary home."""
import os
import tempfile
from pathlib import Path
home = tempfile.TemporaryDirectory(prefix='386gpt-channel-test-')
os.environ['HERMES_HOME'] = home.name
os.environ['API_SERVER_KEY'] = 'test-' + 'a' * 64
import asyncio
import importlib.util
import json
import unittest
from aiohttp import web
from aiohttp.test_utils import TestClient, TestServer
from gateway.config import PlatformConfig, GatewayConfig
from gateway.platform_registry import PlatformEntry, platform_registry
from gateway.session import SessionStore
spec = importlib.util.spec_from_file_location('channel', Path(__file__).parent/'386gpt-channel/adapter.py')
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
platform_registry.register(PlatformEntry(name='chat386',label='386GPT',adapter_factory=module.ChatAdapter,check_fn=lambda:True))

class ChannelTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.adapter = module.ChatAdapter(PlatformConfig(enabled=True,extra={'key':os.environ['API_SERVER_KEY']}))
        self.adapter.set_session_store(SessionStore(Path(home.name)/'sessions', GatewayConfig()))
        self.received = []
        async def handler(event):
            self.received.append(event)
            return 'Native gateway reply: ' + event.text
        self.adapter.set_message_handler(handler)
        self.adapter._create_agent = lambda **kw: self.fail('API agent creation must never run')
        app = web.Application()
        for method,path,fn in self.adapter._http_route_table(): app.router.add_route(method,path,fn)
        self.client = TestClient(TestServer(app)); await self.client.start_server()
        self.headers={'Authorization':'Bearer '+os.environ['API_SERVER_KEY'],'X-Hermes-Session-Key':'owner:thread:fixture','Idempotency-Key':'request-fixture'}

    async def asyncTearDown(self):
        await self.client.close()
        self.adapter.records.execute('DELETE FROM deliveries'); self.adapter.records.commit(); self.adapter.records.close()
        self.adapter._close_cached_session_dbs()

    async def test_gateway_dispatch_idempotency_and_ownership(self):
        response = await self.client.post('/v1/runs',json={'input':'/status'},headers=self.headers)
        self.assertEqual(response.status,202)
        for _ in range(100):
            status=await (await self.client.get('/v1/runs/request-fixture',headers=self.headers)).json()
            if status['status']=='completed': break
            await asyncio.sleep(.01)
        self.assertEqual(status['status'],'completed')
        self.assertIn('Native gateway reply: /status',status['output'])
        self.assertEqual(self.received[0].source.platform.value,'chat386')
        self.assertIsNone(self.received[0].channel_prompt)
        replay=await self.client.post('/v1/runs',json={'input':'/status'},headers=self.headers)
        self.assertEqual(replay.status,202); self.assertEqual(len(self.received),1)
        conflict=await self.client.post('/v1/runs',json={'input':'different'},headers=self.headers)
        self.assertEqual(conflict.status,409)
        other={**self.headers,'X-Hermes-Session-Key':'owner:thread:other'}
        self.assertEqual((await self.client.get('/v1/runs/request-fixture',headers=other)).status,404)
        self.assertEqual((await self.client.get('/v1/runs/request-fixture')).status,401)

    async def test_queued_update_completes_after_both_gateway_turns(self):
        started, release = asyncio.Event(), asyncio.Event()
        async def handler(event):
            self.received.append(event)
            if len(self.received) == 1:
                started.set()
                await release.wait()
            return event.text
        self.adapter.set_message_handler(handler)
        response = await self.client.post('/v1/runs',json={'input':'first'},headers=self.headers)
        self.assertEqual(response.status,202)
        await asyncio.wait_for(started.wait(), 2)
        response = await self.client.post('/v1/runs/request-fixture/steer',json={'input':'follow-up'},headers=self.headers)
        self.assertEqual(response.status,200)
        release.set()
        for _ in range(300):
            status=await (await self.client.get('/v1/runs/request-fixture',headers=self.headers)).json()
            if status['status']=='completed': break
            await asyncio.sleep(.01)
        self.assertEqual([event.text for event in self.received], ['first', 'follow-up'])
        self.assertEqual(status['status'],'completed')
        self.assertIn('follow-up', status['output'])

    async def test_real_gateway_approval_queue_and_stale_choice(self):
        from tools.approval_gateway_wait import _await_gateway_decision
        started, release = asyncio.Event(), asyncio.Event()
        async def handler(event):
            started.set(); await release.wait(); return 'done'
        self.adapter.set_message_handler(handler)
        await self.client.post('/v1/runs',json={'input':'approval fixture'},headers=self.headers)
        await asyncio.wait_for(started.wait(),2)
        session = self.adapter._run('request-fixture')['approval_session']
        loop = asyncio.get_running_loop()
        def notify(data):
            asyncio.run_coroutine_threadsafe(self.adapter.send_exec_approval(
                chat_id='fixture', command=data['command'], description='fixture', session_key=session,
                allow_permanent=False, allow_session=False),loop).result(2)
        for choice in ['deny','once']:
            task = asyncio.create_task(asyncio.to_thread(_await_gateway_decision,session,notify,
                {'command':'rm -rf /tmp/disposable-test-only','description':'fixture','pattern_key':'fixture',
                 'allow_permanent':False,'allow_session':False}))
            for _ in range(100):
                status=await (await self.client.get('/v1/runs/request-fixture',headers=self.headers)).json()
                if status.get('approval'): break
                await asyncio.sleep(.01)
            pending=status['approval']
            self.assertEqual(pending['choices'],['once','deny'])
            stale=await self.client.post('/v1/runs/request-fixture/approval',json={'request_id':'stale','choice':choice},headers=self.headers)
            self.assertEqual(stale.status,409)
            answer=await self.client.post('/v1/runs/request-fixture/approval',json={'request_id':pending['request_id'],'choice':choice},headers=self.headers)
            self.assertEqual(answer.status,200)
            result=await asyncio.wait_for(task,2)
            self.assertTrue(result['resolved']);self.assertEqual(result['choice'],choice)
        release.set()
        await asyncio.sleep(.02)

    async def test_rejects_agent_overrides(self):
        response=await self.client.post('/v1/runs',json={'input':'hello','instructions':'override'},headers=self.headers)
        self.assertEqual(response.status,400); self.assertEqual(self.received,[])

if __name__=='__main__': unittest.main()

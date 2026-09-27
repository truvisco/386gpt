"""386GPT is a messaging transport. Only GatewayRunner creates/runs agents.

Reuse Hermes's authenticated HTTP and read-only transcript/skills surfaces;
replace agent submission with normal BasePlatformAdapter MessageEvents.
"""
import asyncio
import hashlib
import json
import re
import sqlite3
import uuid
from pathlib import Path

from aiohttp import web
from gateway.config import Platform
from gateway.platforms.api_server import APIServerAdapter, _require_auth
from gateway.platforms.base import SendResult
from gateway.platforms.event import MessageEvent
from hermes_constants import get_hermes_home

ACTIVE = {"queued", "running", "waiting_for_approval", "stopping"}


class ChatAdapter(APIServerAdapter):
    def __init__(self, config):
        super().__init__(config)
        self.platform = Platform("chat386")
        self.owner = str(config.extra.get("owner", "386gpt-owner"))
        self.records = sqlite3.connect(Path(get_hermes_home()) / "386gpt-channel.db")
        self.records.execute("CREATE TABLE IF NOT EXISTS deliveries (id TEXT PRIMARY KEY, chat TEXT NOT NULL, fingerprint TEXT NOT NULL, snapshot TEXT NOT NULL)")
        # An accepted message is never silently re-executed after gateway restart.
        for rid, raw in self.records.execute("SELECT id,snapshot FROM deliveries").fetchall():
            run = json.loads(raw)
            if run["status"] in ACTIVE:
                run.update(status="interrupted", error="Gateway restarted during message processing.", approval=None)
                self._save(run)

    def _http_route_table(self):
        return [
            ("GET", "/v1/capabilities", self.capabilities),
            ("GET", "/v1/skills", self._handle_skills),
            ("GET", "/api/sessions/{session_id}/messages", self._handle_session_messages),
            ("POST", "/v1/runs", self.submit_message),
            ("GET", "/v1/runs/{run_id}", self.status),
            ("GET", "/v1/runs/{run_id}/events", self.events),
            ("POST", "/v1/runs/{run_id}/{action}", self.control),
        ]

    def _save(self, run):
        self.records.execute("INSERT INTO deliveries VALUES (?,?,?,?) ON CONFLICT(id) DO UPDATE SET snapshot=excluded.snapshot",
                             (run["run_id"], run["chat"], run["fingerprint"], json.dumps(run)))
        self.records.commit()

    def _run(self, rid):
        row = self.records.execute("SELECT snapshot FROM deliveries WHERE id=?", (rid,)).fetchone()
        return json.loads(row[0]) if row else None

    def _chat_run(self, chat):
        row = self.records.execute("SELECT snapshot FROM deliveries WHERE chat=? ORDER BY rowid DESC LIMIT 1", (chat,)).fetchone()
        return json.loads(row[0]) if row else None

    def _chat(self, request):
        key = request.headers.get("X-Hermes-Session-Key", "")
        chat = key.rsplit(":thread:", 1)[-1]
        if not re.fullmatch(r"[a-zA-Z0-9_-]{1,100}", chat):
            raise web.HTTPBadRequest(text='{"error":"Conversation identity required"}', content_type="application/json")
        return chat

    def _event(self, chat, text, rid, skills=None):
        return MessageEvent(text=text, source=self.build_source(chat_id=chat, chat_type="dm", user_id=self.owner, user_name="386GPT owner"),
                            message_id=rid, auto_skill=skills or None)

    def _public(self, run):
        return {k: v for k, v in run.items() if k not in {"chat", "fingerprint", "messages", "approval_session"}}

    def _refresh_approval(self, run):
        from tools.approval import get_pending_gateway_approval
        from agent.redact import redact_sensitive_text
        pending = get_pending_gateway_approval(run.get("approval_session", ""))
        if pending and run["status"] in ACTIVE:
            actions = self._exec_approval_actions(allow_permanent=pending.get("allow_permanent") is not False,
                allow_session=pending.get("allow_session") is not False, smart_denied=bool(pending.get("smart_denied")))
            run.update(status="waiting_for_approval", approval={"request_id":pending["request_id"],
                "command":redact_sensitive_text(str(pending.get("command", "")), force=True),
                "description":redact_sensitive_text(str(pending.get("description", "")), force=True),
                "choices":[choice for _,choice,_ in actions]})
        elif run["status"] == "waiting_for_approval":
            run.update(status="running", approval=None)

    @_require_auth
    async def capabilities(self, request):
        runtime = self._client_runtime_metadata()
        runtime.update(transport="messaging_gateway", profile=Path(get_hermes_home()).name)
        return web.json_response({"runtime": runtime, "features": {"run_submission": True, "run_status": True,
            "run_stop": True, "run_steer": True, "run_approval_response": True, "skills_api": True,
            "gateway_commands": True, "runs_idempotency": {"supported": True, "durable": True}}})

    @_require_auth
    async def submit_message(self, request):
        body = await request.json()
        chat = self._chat(request)
        key = request.headers.get("Idempotency-Key", "")
        if not re.fullmatch(r"[a-zA-Z0-9_-]{8,100}", key) or not isinstance(body.get("input"), str) or not body["input"].strip():
            return web.json_response({"error": "Valid request identity and input required"}, status=400)
        if any(k in body for k in ("instructions", "model", "provider", "system_message")):
            return web.json_response({"error": "Provider and agent instructions belong to the Hermes profile"}, status=400)
        fingerprint = hashlib.sha256(json.dumps({"chat":chat,"body":body}, sort_keys=True).encode()).hexdigest()
        prior = self._run(key)
        if prior:
            if prior["fingerprint"] != fingerprint:
                return web.json_response({"error":"Request identity conflict"}, status=409)
            return web.json_response(self._public(prior), status=202)
        prior = self._chat_run(chat)
        if prior and prior["status"] in ACTIVE:
            return web.json_response({"error":"Conversation is busy"}, status=409)
        event = self._event(chat, body["input"], key, body.get("skills"))
        entry = self._session_store.get_or_create_session(event.source)
        run = {"run_id":key, "chat":chat, "fingerprint":fingerprint, "status":"queued", "session_id":entry.session_id,
               "output":"", "messages":{}, "approval":None, "approval_session":self._event_session_key(event)}
        self._save(run)
        try:
            await self.handle_message(event)
            if not event._gateway_accepted:
                raise RuntimeError("Gateway did not admit this message")
        except Exception as exc:
            run.update(status="failed", error=str(exc)); self._save(run)
        return web.json_response(self._public(self._run(key)), status=202)

    def _owned(self, request):
        run = self._run(request.match_info["run_id"])
        if not run or run["chat"] != self._chat(request):
            raise web.HTTPNotFound()
        return run

    @_require_auth
    async def status(self, request):
        run = self._owned(request)
        self._refresh_approval(run)
        event = self._event(run["chat"], "", run["run_id"])
        live_id = self._session_store.peek_session_id(self._event_session_key(event))
        if live_id:
            run["session_id"] = live_id
        self._save(run)
        return web.json_response(self._public(run))

    @_require_auth
    async def events(self, request):
        self._owned(request)
        # Message snapshots and Hermes transcripts are durable; disconnecting
        # this optional live transport never changes gateway execution.
        return web.Response(text="", content_type="text/event-stream")

    @_require_auth
    async def control(self, request):
        run = self._owned(request)
        self._refresh_approval(run)
        if run["status"] not in ACTIVE:
            return web.json_response({"error":"Message is no longer active"}, status=409)
        body = await request.json()
        action = request.match_info["action"]
        if action == "approval":
            from tools.approval import resolve_gateway_approval
            pending = run.get("approval") or {}
            if body.get("request_id") != pending.get("request_id") or body.get("choice") not in pending.get("choices", []):
                return web.json_response({"error":"Stale or unsupported approval"}, status=409)
            resolved = resolve_gateway_approval(run["approval_session"], body["choice"], request_id=pending["request_id"])
            if not resolved:
                return web.json_response({"error":"Approval has expired"}, status=409)
            run.update(status="running", approval=None); self._save(run)
        elif action in {"stop", "steer"}:
            text = "/stop" if action == "stop" else body.get("input", "")
            if not isinstance(text, str) or not text.strip():
                return web.json_response({"error":"Input required"}, status=400)
            if action == "steer" and not text.lstrip().startswith("/"):
                run.setdefault("pending_steer", []).append(text)
                self._save(run)
            # Controls enter the same two command guards as every messaging client.
            await self.handle_message(self._event(run["chat"], text, str(uuid.uuid4())))
        else:
            raise web.HTTPNotFound()
        return web.json_response({"ok":True})

    async def on_processing_start(self, event):
        run = self._chat_run(event.source.chat_id)
        if run and run["status"] in ACTIVE:
            run.update(status="running", pending_steer=[]); self._save(run)

    async def on_processing_complete(self, event, outcome):
        run = self._chat_run(event.source.chat_id)
        if run and run["status"] in ACTIVE:
            if self._event_session_key(event) in self._pending_messages:
                run["status"] = "queued"
                self._save(run)
                return
            run.update(status={"success":"completed", "failure":"failed", "cancelled":"cancelled"}.get(outcome.value, "failed"), approval=None)
            self._save(run)

    async def send(self, chat_id, content, reply_to=None, metadata=None, **kwargs):
        return await self.edit_message(chat_id, str(uuid.uuid4()), content, metadata=metadata)

    async def edit_message(self, chat_id, message_id, content, metadata=None, **kwargs):
        run = self._chat_run(chat_id)
        if not run:
            return SendResult(success=False, error="Conversation not found")
        from agent.redact import redact_sensitive_text
        run["messages"][str(message_id)] = redact_sensitive_text(content, force=True)
        run["output"] = "\n\n".join(run["messages"].values())
        self._save(run)
        return SendResult(success=True, message_id=str(message_id))

    async def send_typing(self, chat_id, metadata=None):
        pass

    async def _send_exec_approval_prompt(self, prompt):
        run = self._chat_run(prompt.chat_id)
        if not run:
            return SendResult(success=False, error="Conversation not found")
        run["approval_session"] = prompt.session_key
        self._refresh_approval(run)
        self._save(run)
        return SendResult(success=True, message_id=(run.get("approval") or {}).get("request_id"))


def register(ctx):
    ctx.register_platform(name="chat386", label="386GPT", adapter_factory=ChatAdapter, check_fn=lambda: True,
                          allowed_users_env="CHAT386_ALLOWED_USERS", max_message_length=100000,
                          platform_hint="You are talking to the owner through the 386GPT web messaging client.")

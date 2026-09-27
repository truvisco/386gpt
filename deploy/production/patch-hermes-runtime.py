#!/usr/bin/env python3
"""Apply reviewed compatibility fixes to the pinned, dedicated 386GPT runtime."""
from pathlib import Path
import subprocess
import sys

REVISION = "498abb677ec39ea3ae9f8f5ed60e7def6bc47e70"
root = Path(sys.argv[1]).resolve()
actual = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
if actual != REVISION:
    raise SystemExit(f"Expected pinned Hermes {REVISION}; found {actual}. Review patches before upgrading.")

def replace(relative, before, after):
    path = root / relative
    source = path.read_text()
    # Repair duplicate insertion blocks from the first development installer.
    if after.endswith(before):
        inserted = after[:-len(before)]
        while inserted and source.count(inserted) > 1:
            source = source.replace(inserted, "", 1)
    if after in source and (before not in source or before in after):
        if source != path.read_text():
            compile(source, str(path), "exec")
            path.write_text(source)
        return
    if source.count(before) != 1:
        raise SystemExit(f"Compatibility patch does not match {relative}")
    source = source.replace(before, after)
    compile(source, str(path), "exec")
    path.write_text(source)

api = "gateway/platforms/api_server.py"
replace(api, "skip_disabled=False, include_editorial=True", "skip_disabled=False")
replace(api, '"runtime": {\n                "mode": "server_agent",',
        '"runtime": {\n                **self._client_runtime_metadata(),\n                "mode": "server_agent",')
replace(api, '    @_require_auth\n    async def _handle_capabilities(', '''    def _client_runtime_metadata(self):
        import socket
        import platform
        from pathlib import Path
        from gateway.run import _load_gateway_config, _resolve_gateway_model
        config = _load_gateway_config()
        return {
            "hostname": socket.gethostname(), "os": platform.system(),
            "home": str(Path.home()),
            "cwd": str(config.get("terminal", {}).get("cwd") or Path.home()),
            "model": _resolve_gateway_model(),
            "provider": config.get("model", {}).get("provider", ""),
            "revision": "498abb677ec39ea3ae9f8f5ed60e7def6bc47e70+386gpt",
        }

    @_require_auth
    async def _handle_capabilities(''')
# The transcript is execution evidence, not a vehicle for forwarding credentials.
replace(api, '        return {key: message.get(key) for key in safe_keys if key in message}', '''        def redact(value):
            if isinstance(value, str):
                return redact_sensitive_text(value, force=True)
            if isinstance(value, list):
                return [redact(item) for item in value]
            if isinstance(value, dict):
                return {key: redact(item) for key, item in value.items()}
            return value
        return {key: redact(message.get(key)) for key in safe_keys if key in message}''')
# Terminal affinity belongs to a conversation; approval authorization remains per run.
replace("gateway/platforms/api_server_runs.py",
        'chat_id=session_id or "", session_key=run.approval_session_key, session_id=session_id or "",',
        'chat_id=session_id or "", session_key=run.gateway_session_key or session_id or run.run_id, session_id=session_id or "",')
replace("tools/terminal_tool.py",
        'session_key = get_current_session_key(default="") or (task_id or "")',
        'session_key = task_id or _current_session_key() or get_current_session_key(default="")')
print("Hermes compatibility fixes installed and syntax checked.")

#!/usr/bin/env python3
"""Install the Mac's dedicated Hermes profile and loopback launchd service."""
raise SystemExit('Host Hermes execution is retired. Run sh deploy/isolation/install-local.sh')
import os
from pathlib import Path
import plistlib
import secrets
import shutil
import subprocess
import sys
import yaml

home = Path.home()
runtime = home / ".hermes/386gpt-runtime"
python = runtime / "venv/bin/python"
profile = home / ".hermes/profiles/386gpt-local"
client = Path(os.environ.get("HERMES_CONFIG", str(home / ".hermes/386gpt.yaml"))).expanduser()
host = os.environ.get("HERMES_SSH_HOST", "crash")
source = Path(__file__).resolve().parents[1] / "production/hermes-unsloth.yaml"
if not python.exists():
    raise SystemExit("Run deploy/production/prepare-hermes-runtime.sh first")
environment = {**os.environ, "PYTHONPATH": str(runtime)}
existing_profile_config = (profile / "config.yaml").exists()
if not profile.exists():
    subprocess.run([str(python), "-m", "hermes_cli.main", "profile", "create", "386gpt-local", "--no-alias"], cwd=runtime, env=environment, check=True)
old = yaml.safe_load(client.read_text()) if client.exists() else yaml.safe_load(subprocess.check_output([
    "ssh", "-o", "BatchMode=yes", host, "sudo -n cat /etc/hermes-agent/386gpt-client.yaml"], text=True))
crash = old.get("runtimes", {}).get("crash") or old["agent"]
key = old.get("runtimes", {}).get("local", {}).get("api_key") or secrets.token_hex(32)
unsloth_key = subprocess.check_output(["ssh", "-o", "BatchMode=yes", host, "cat /home/grimlock/.hermes/386gpt-unsloth-api-key"], text=True).strip()
if existing_profile_config:
    config = yaml.safe_load((profile / "config.yaml").read_text())
else:
    config = yaml.safe_load(source.read_text())
    config["model"]["base_url"] = "http://100.74.13.43:8888/v1"
    config["custom_providers"][0]["base_url"] = config["model"]["base_url"]
    config["terminal"] = {"backend": "local", "cwd": str(home)}
def private(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists() and not path.with_suffix(path.suffix + ".before-runs").exists():
        shutil.copy2(path, path.with_suffix(path.suffix + ".before-runs"))
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "wb") as output:
        output.write(data)
    path.chmod(0o600)
private(profile / "config.yaml", yaml.safe_dump(config).encode())
existing_env = (profile / ".env").read_text() if (profile / ".env").exists() else ""
env_lines = [line for line in existing_env.splitlines() if not line.startswith("API_SERVER_KEY=")]
if not any(line.startswith("UNSLOTH_API_KEY=") for line in env_lines):
    env_lines.append(f"UNSLOTH_API_KEY={unsloth_key}")
env_lines.append(f"API_SERVER_KEY={key}")
private(profile / ".env", ("\n".join(env_lines) + "\n").encode())
local = {"base_url": "http://127.0.0.1:8644", "api_key": key, "session_key": "agent:main:386gpt:dm:owner"}
crash["base_url"] = crash["base_url"].replace(":8643", ":8644")
private(client, yaml.safe_dump({"default_runtime": "local", "runtimes": {"local": local, "crash": crash}}).encode())
subprocess.run([str(python), str(Path(__file__).resolve().parents[1] / "hermes-gateway/install-channel.py"), "--profile", "386gpt-local", "--client", str(client)], check=True)
logs = home / "Library/Logs/386gpt-hermes"
logs.mkdir(parents=True, exist_ok=True)
label = "co.truvis.386gpt.hermes"
plist = home / "Library/LaunchAgents" / (label + ".plist")
private(plist, plistlib.dumps({
    "Label": label, "ProgramArguments": [str(python), "-m", "hermes_cli.main", "-p", "386gpt-local", "gateway", "run"],
    "WorkingDirectory": str(runtime), "RunAtLoad": True, "KeepAlive": True, "ThrottleInterval": 10,
    "EnvironmentVariables": {"HOME": str(home), "USER": os.environ.get("USER", "grimlock"), "PYTHONPATH": str(runtime),
        "PATH": f"{runtime}/venv/bin:{home}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
        "API_SERVER_ENABLED": "true", "API_SERVER_HOST": "127.0.0.1", "API_SERVER_PORT": "8643"},
    "StandardOutPath": str(logs / "gateway.log"), "StandardErrorPath": str(logs / "gateway.log"),
}))
domain = f"gui/{os.getuid()}"
subprocess.run(["launchctl", "bootout", f"{domain}/{label}"], capture_output=True)
subprocess.run(["launchctl", "bootstrap", domain, str(plist)], check=True)
print("Local Hermes messaging channel installed on 127.0.0.1:8644. Existing conversations remain on crash; credentials hidden.")

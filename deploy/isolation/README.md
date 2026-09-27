# Isolated Hermes deployment

386GPT's Hermes process runs inside a non-root Docker container, not as the host login user. The image pins Hermes revision `498abb677ec39ea3ae9f8f5ed60e7def6bc47e70`, base-image digests, and Python dependencies. Existing model/provider choices come from the source profile. No Telegram setup is needed.

## Boundaries

Hermes has read-only system/runtime/configuration files, UID/GID 10001, no capabilities, `no-new-privileges`, default seccomp, and only its own workspace/state volumes plus bounded `/tmp`. Its terminal runs locally **inside the container**, at `/workspace`. It has neither host home directories nor Docker/SSH sockets, host credentials, devices, or GPU access. Coding dependencies may be installed in workspace virtual environments; system packages belong in the image.

The internal bridge uses `gateway_mode_ipv4=isolated`; require Docker Engine 28+ and Compose v2. Hermes has no external network attachment. Separate non-root proxies expose the authenticated messaging gateway, allow public HTTP/HTTPS destinations only, and forward a fixed set of Unsloth inference endpoints. The public proxy resolves and checks every destination then dials the checked IP, preventing DNS rebinding. Private/reserved IPv4 and IPv6 destinations are denied. SSH Git URLs are unavailable; use HTTPS public repositories. Only the model proxy receives the real Unsloth key. Only the gateway proxy receives the external transport key; the agent has a separate internal key.

This is host isolation, not separation between conversations: the owner’s conversations share one workspace per environment. Containers still share a kernel. Persistent volumes have no filesystem quota; monitor Docker disk usage. The agent has a 4 GiB memory limit, 4-CPU quota, 512-PID limit and 512 MiB temporary filesystem; logs rotate at 3 × 10 MiB per service.

## Local migration / update

Docker Desktop must be running. With the existing Hermes profile and migration venv present:

```sh
sh deploy/development/configure-hermes-client.sh
```

The installer backs up the original profile under `~/.config/386gpt-isolation`, exports only session/database/memory data, updates the local client key, and replaces `co.truvis.386gpt.hermes` with a launchd supervisor. The supervisor waits for Docker Desktop after login. It does not start the unrestricted host gateway. Enable Docker Desktop startup at login for unattended recovery after macOS login.

Run the backend with `go tool air` (explicit development auth and loopback binding are in `.air.toml`), or:

```sh
AUTH_MODE=development ADDRESS=127.0.0.1:8080 go run .
```

Development bypass refuses non-loopback listen addresses. Existing conversations assigned to the crash runtime continue using crash.

## Crash migration / update

Copy the reviewed deploy files to `/opt/386gpt-isolation`. Build both images there. Prepare the configuration using the existing Hermes venv:

```sh
sudo /home/grimlock/.hermes/386gpt-runtime/venv/bin/python deploy/isolation/prepare.py \
  --profile /home/grimlock/.hermes/profiles/386gpt --output /etc/386gpt-isolation \
  --bind 100.74.13.43 --upstream http://host.docker.internal:8888
```

The output directory is owner-only. Individual read-only mounted files must be readable by the container UID. Never commit it, publish it in logs, or mount the entire directory into Hermes. `prepare.py` refuses to overwrite existing keys; retain them on updates.

Validate staging on port 18644 (`GATEWAY_PORT=18644 docker compose ... up -d --no-build`) before stopping the old gateway. Enable the owner access gate before reconnecting public traffic. The cutover installer stops the old gateway and imports a final data-only snapshot into a fresh volume. Never overwrite a populated state volume.

Run `sh deploy/isolation/install-crash.sh` on crash after the origin authentication check passes. It migrates the profile into a fresh volume, archives old service overrides, installs the container systemd unit, updates the private client file, and runs native gateway checks. It refuses cutover while web1 still accepts unauthenticated API requests. For manual recovery, `hermes-container.service` is the replacement `hermes-gateway-386gpt.service` unit. Update the crash connection in `/etc/hermes-agent/386gpt-client.yaml`, book14’s client, and etcd `/prod/386gpt/hermes-agent-config` using the new external key. Restart the Go backends to reload connections. Port 8643 must no longer listen. Unsloth's existing service remains unchanged.

State migration preserves database-backed history, gateway receipts, session routing, and memory. It excludes executable hooks/plugins, cron schedules, credentials, and terminal state. Old workspace files stay in the archived source profile. Pending runs become interrupted and are not replayed. An explicit `/sethome` choice is stored separately in writable state; the home-channel reminder is unchanged.

## Cloudflare Access / CI

Run `deploy/cloudflare/configure-access.mjs PRIVATE_OUTPUT_JSON` through the existing `with-cloudflare-env.sh` wrapper. Required account permissions: Access identity-provider/organization/group Edit, Access apps/policies Edit, and Access service-token Edit. It provisions email login for `mauriciootta@gmail.com`, an eight-hour session, and a separate Jenkins service identity. Never broaden the owner allowlist to make a failing check pass.

Store the resulting JSON, including `issuer` (`https://TEAM.cloudflareaccess.com`), at `/prod/386gpt/access-config` in etcd. `render-production-config.sh` creates a backend metadata-only environment file and a private CI environment file. The backend validates signed Access JWTs even when contacted at the origin. WebSockets close at token expiry. The Worker validates identities too and proxies API/WebSocket requests under the frontend origin. Browser or Hermes code never receives the CI service secret.

Jenkins requires this configuration, validates auth/proxy tests, deploys both components, and runs authenticated smoke tests. Missing authentication configuration fails closed. A failed first protected release must not roll back to an unprotected binary; retain maintenance/unavailable status instead.

## Verification and recovery

```sh
docker exec -i 386gpt-isolated-hermes-1 python - < deploy/isolation/check.py
python3 deploy/isolation/smoke.py http://127.0.0.1:8644 ~/.config/386gpt-isolation/gateway.key
```

On crash use its Tailscale gateway address and root-owned key file. Check the service’s actual Docker mounts, capabilities, user, networks, resource limits, restart policy, and health. Test public/private DNS destinations, literal IPs, disabled proxy variables, inference and native terminal execution. Test unauthenticated and forged JWT requests against both public hostnames and the direct origin. Verify owner browser login and Jenkins service access separately.

Restart the stack and check retained conversation state and workspace files. Validate crash recovery after a scheduled reboot; validate local recovery after Docker Desktop restarts. Revert only to an already isolated image and an auth-enforcing backend release. Preserve volumes and backups; never reactivate the former unrestricted host service.

# Isolated Hermes deployment

386GPT's Hermes process runs inside a non-root Docker container, not as the host login user. The image pins Hermes revision `498abb677ec39ea3ae9f8f5ed60e7def6bc47e70`, base-image digests, and Python dependencies. Existing model/provider choices come from the source profile. No Telegram setup is needed.

## Boundaries

Hermes starts as UID/GID 10001 and has passwordless `sudo` inside its container. Its root filesystem is writable; configuration mounts remain read-only. It retains default seccomp and a limited capability set needed for sudo and package installation, without mount, network-administration or tracing capabilities. The gateway, model and egress proxies retain read-only filesystems, no capabilities and `no-new-privileges`. Hermes mounts only its own host workspace directory and state volume, plus bounded `/tmp`. Its terminal runs locally **inside the container**, at `/workspace`. It has neither host home directories nor Docker/SSH sockets, host credentials, devices, or GPU access. The image includes Git, SSH client, curl/wget, Python/pip/venv, Node/npm, Go, GCC/G++/make, CMake/Ninja/pkg-config, ripgrep, jq, SQLite, editors and archive tools. `sudo apt-get` works through the restricted proxy. Additional system packages survive a restart but must be reinstalled after image replacement; put reusable dependencies in the image or workspace.

The internal bridge uses `gateway_mode_ipv4=isolated`; require Docker Engine 28+ and Compose v2. Hermes has no external network attachment. Separate non-root proxies expose the authenticated messaging gateway, allow public HTTP/HTTPS destinations only, and forward a fixed set of Unsloth inference endpoints. The public proxy resolves and checks every destination then dials the checked IP, preventing DNS rebinding. Private/reserved IPv4 and IPv6 destinations are denied. SSH Git URLs are unavailable; use HTTPS public repositories. Only the model proxy receives the real Unsloth key. Only the gateway proxy receives the external transport key; the agent has a separate internal key.

Accounts have separate environments. Conversations within the same account share that account’s workspace. Containers still share a kernel. Persistent workspace directories and state volumes have no filesystem quota; monitor Docker disk usage. The agent has a 4 GiB memory limit, 4-CPU quota, 512-PID limit and 512 MiB temporary filesystem; logs rotate at 3 × 10 MiB per service.

## Local migration / update

Docker Desktop must be running. With the existing Hermes profile and migration venv present:

```sh
sh deploy/development/configure-hermes-client.sh
```

The installer backs up the original profile under `~/.config/386gpt-isolation`, exports only session/database/memory data, updates the local client key, and replaces `co.truvis.386gpt.hermes` with a launchd supervisor. The supervisor waits for Docker Desktop after login. It does not start the unrestricted host gateway. Enable Docker Desktop startup at login for unattended recovery after macOS login.

Run the backend with `go tool air` (explicit development auth and loopback binding are in `.air.toml`), or:

```sh
go tool air
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

Validate staging on port 18644 (`GATEWAY_PORT=18644 docker compose ... up -d --no-build`) before stopping the old gateway. Enable API authentication before reconnecting public traffic. The cutover installer stops the old gateway and imports a final data-only snapshot into a fresh volume. Never overwrite a populated state volume.

Run `sh deploy/isolation/install-crash.sh` on crash after the origin authentication check passes. It migrates the profile into a fresh volume, archives old service overrides, installs the container systemd unit, updates the private client file, and runs native gateway checks. It refuses cutover while web1 still accepts unauthenticated API requests. For manual recovery, `hermes-container.service` is the replacement `hermes-gateway-386gpt.service` unit. Update the crash connection in `/etc/hermes-agent/386gpt-client.yaml`, book14’s client, and etcd `/prod/386gpt/hermes-agent-config` using the new external key. Restart the Go backends to reload connections. Port 8643 must no longer listen. Unsloth's existing service remains unchanged.

State migration preserves database-backed history, gateway receipts, session routing, and memory. It excludes executable hooks/plugins, cron schedules, credentials, and terminal state. Old workspace files stay in the archived source profile. Pending runs become interrupted and are not replayed. An explicit `/sethome` choice is stored separately in writable state; the home-channel reminder is unchanged.

## Cloudflare routing / CI

Google OAuth and session validation run in the API. The Worker forwards app
cookies to the fixed API origin. The deployment probes the origin before enabling
Cloudflare's bypass policy. The old `configure-access.mjs` script is for the former
single-owner setup and must not be used for account deployments.

`/prod/386gpt/access-config` retains the existing app identifier and Jenkins service
credential. Both the private CI environment and the API's protected environment
file receive that credential. It authenticates only the dedicated CI account;
browser and Hermes code never receive it. Cloudflare JWT headers are ignored by
the Google OAuth API.

## Verification and recovery

```sh
docker exec -i 386gpt-isolated-hermes-1 python - < deploy/isolation/check.py
python3 deploy/isolation/smoke.py http://127.0.0.1:8644 ~/.config/386gpt-isolation/gateway.key
```

On crash use its Tailscale gateway address and root-owned key file. Check the service’s actual Docker mounts, capabilities, user, networks, resource limits, restart policy, and health. Test public/private DNS destinations, literal IPs, disabled proxy variables, inference and native terminal execution. Test unauthenticated and forged JWT requests against both public hostnames and the direct origin. Verify owner browser login and Jenkins service access separately.

Restart the stack and check retained conversation state and workspace files. Validate crash recovery after a scheduled reboot; validate local recovery after Docker Desktop restarts. Revert only to an already isolated image and an auth-enforcing backend release. Preserve volumes and backups; never reactivate the former unrestricted host service.

## Google accounts

Google OAuth terminates in the Go API, using the private web-client JSON stored
in etcd at `/prod/386gpt/google-oauth-client`. The registered production callback is
`https://386gpt.truvis.co/api/callback/google/oauth`. The frontend Worker proxies
this route and app session cookies; it does not implement OAuth or trust identity
headers. `configure-api-oauth.mjs` changes Cloudflare Access to a bypass policy
only after checking that the deployed origin offers Google login and rejects an
anonymous account request. API authentication is mandatory even at the origin.

Each verified Google subject gets a stable internal account ID. Accounts have
separate SQLite files, run workers, WebSocket hubs, Hermes gateways, profile/state
volumes, host workspace directories, gateway keys and terminal-session namespaces. Email
changes do not change the account ID. The first verified Google login for
`mauriciootta@gmail.com` binds the existing conversation database and isolated
owner container to that identity. This binding is persisted and cannot be claimed
by another subject. Jenkins uses a separate service account and container.

The private broker runs on crash's Tailscale address, port 8650. It accepts only
an authenticated `PUT /v1/accounts/<64 hex account ID>` or `PUT /v1/owner`, with no body. It can start
only the repository's fixed Compose definition; API callers cannot choose an
image, mount, command, port or host path. The broker's host-level Docker permission
is not given to the API, any Hermes container, or its tools. The broker credential
exists only in the API connection config and on the host.

Install on crash after building the existing isolation images:

```sh
sudo /home/grimlock/.hermes/386gpt-runtime/venv/bin/python deploy/isolation/install-accounts.py \
  --settings /etc/386gpt-isolation --client /etc/hermes-agent/386gpt-client.yaml \
  --url http://100.74.13.43:8650
sudo install -d -m 0700 /var/lib/386gpt-accounts
sudo install -m 0644 deploy/isolation/tenant-broker.service /etc/systemd/system/386gpt-accounts.service
sudo systemctl daemon-reload
sudo systemctl enable --now 386gpt-accounts
```

Publish the updated client YAML to `/prod/386gpt/hermes-agent-config`. Do not copy
broker/gateway credentials to the frontend. The broker defaults to eight new
account environments, in addition to the existing owner's environment. Each has
the Compose CPU, RAM and PID limits. At capacity, login still works, but new
workspaces stay unavailable until capacity is increased or an operator retires an
unused environment. There is no automatic deletion of account data. Host workspace directories and named state volumes
persist across container replacement; they currently have no per-account disk quota. Google
sign-in requires the OAuth consent application to allow the intended users.

For book14, put the supplied client JSON at
`~/.config/386gpt/google-oauth.json` with mode 0600. Run `install-accounts.py` with
`~/.config/386gpt-isolation`, `~/.hermes/386gpt.yaml`, and
`--url http://127.0.0.1:8650`, then reinstall the existing local supervisor. Air uses
`deploy/development/start-api.sh`, enables Google login, and uses the registered
`http://localhost:5173/api/callback/google/oauth` callback. Both local API and
WebSocket requests go through Vite's same-origin proxy. Docker Desktop must be
running. The normal local development account is now the signed-in Google account.

Sessions last eight hours, are stored as hashes, and are revoked on sign-out.
Existing WebSockets close on expiry or within one second of revocation. Google
access/refresh tokens are not persisted or passed to Hermes. Failed OAuth state,
nonce, signature, issuer, audience or verified-email checks never create a session.


### Workspace persistence and image updates

Each account bind-mounts `/var/lib/386gpt-accounts/<account ID>/workspace` on crash
at `/workspace`. The existing owner uses `/etc/386gpt-isolation/workspace`.
On book14 these directories are under `~/.config/386gpt-isolation/accounts/`
and `~/.config/386gpt-isolation/workspace`, respectively. Only the workspace
subdirectory is mounted, never the parent containing credentials. Back up these
host directories along with the Hermes state volumes and account databases.

`workspace.py` migrates an existing named workspace volume before the first
updated stack start. It stops Hermes before copying, preserves file ownership,
and keeps the original volume as a backup. A missing registered host directory
is an error: Compose cannot silently create an empty replacement. Do not delete
old workspace volumes until their host copies have been verified and backed up.
If migration is interrupted after copying but before recording the host path,
the broker refuses to overwrite the unregistered directory; verify that copy
before recovering the `HERMES_WORKSPACE` entry in the private `compose.env`.

Every successful Google sign-in asks the broker to reconcile the account stack,
including cached accounts and the existing owner. The account remains in its
preparing state until this completes. Compose checks the image ID behind the
locally deployed `386gpt-hermes:isolated` tag and recreates stale containers;
current healthy containers are left running. This does not pull an arbitrary
registry `latest` tag. Build and validate the new image on each host before
promoting it to that deployment tag. Replacement interrupts an active agent run,
but retains the workspace bind mount, Hermes state volume and conversation data.

Validate development tools with `check-development.py`, and run `check.py` both
as the normal user and through `sudo -n ... --root` to check network boundaries
and read-only configuration mounts with elevated container privileges.

# 386GPT

A DOS-inspired chat interface with a React frontend and Go backend. Conversations are persisted in SQLite and new messages stream over WebSockets.

Each conversation has a bookmarkable `/conversations/<id>` URL. Reloading or
opening the link restores that thread; browser Back/Forward switches between
conversations. `/` starts a new chat, and sending its first message creates the
conversation URL.

## Development

Requirements: Node.js 20+, Go 1.26+, and npm. Older Go installations with automatic toolchain downloads enabled will fetch the required toolchain.

Install the frontend dependencies:

```sh
cd frontend
npm install
```

Run the Go backend with Air hot reload:

```sh
cd backend
go tool air
```

In a second terminal, run the frontend:

```sh
cd frontend
npm run dev
```

Open the Vite URL shown in the terminal. The backend listens on `http://localhost:8080` by default and stores its database at `backend/data/386gpt.db`.

## Configuration

Backend environment variables:

- `ADDRESS`: listen address, default `:8080`
- `DATABASE_PATH`: SQLite file path, default `data/386gpt.db`
- `HERMES_CONFIG`: 386GPT's Hermes Agent connection file, default `~/.hermes/386gpt.yaml`

Frontend environment variables:

- `VITE_API_URL`: backend HTTP origin, default `http://localhost:8080`

The backend uses Hermes's authenticated run API. It durably stores submissions before sending them, retries with the same idempotency key, and reconciles run status and tool transcripts after disconnects or backend restarts. Each conversation has a fixed runtime and a durable Hermes session. Instructions are supplied on every run.

The conversation panel shows the actual tool host, working directory, skills, tool arguments and results, pending approvals, and stop/update controls. Agent completion is distinct from successful tool execution. Only choices offered by Hermes appear in approval prompts. Credentials stay on the backend; reasoning text is not exposed as execution evidence. Conversation URLs use `/conversations/<id>` and survive reloads.

To check agent behavior against a running backend and the real Hermes host:

```sh
node deploy/smoke/agent-workflow.mjs http://localhost:8080 local
```

This opt-in test uses SSH to create a disposable Python Git repository on the
Hermes host. Across two turns it asks the agent to inspect failing tests, retain
the repository context, implement a function, test it, and commit on a new
branch. It independently checks the resulting code and Git state, then removes
the fixture and app thread. It never pushes a commit. The Hermes transcript is
retained for diagnosis. Unlike the short `websocket.mjs` connectivity check,
this test rejects replies that merely claim success without doing the work.
Install `ripgrep` on the Hermes host so its file-search tool can search safely.

The Gemma E2B Q4_K_P profile has not passed this coding acceptance test: it
attempted edits but stopped with failing tests and no implementation commit,
including when tested with thinking enabled. A successful connectivity smoke
test does not establish reliable autonomous coding behavior.

Local development runs tools on book14 while inference remains on crash's Unsloth server. Existing conversations stay pinned to crash; create a new conversation for local execution. After configuring crash below, run:

```sh
sh deploy/development/configure-hermes-client.sh
```

This prepares a dedicated pinned Hermes checkout and the `386gpt-local` profile, installs the loopback LaunchAgent `co.truvis.386gpt.hermes` on port 8643, and writes owner-only `~/.hermes/386gpt.yaml`. Restart `go tool air` afterward. The existing default Hermes profile is unaffected. Tailscale access to crash's Unsloth port 8888 is required. `HERMES_SSH_HOST` and `HERMES_CONFIG` override connection installation defaults.

The private development configuration contains `default_runtime: local` and a `runtimes` mapping with `local` and `crash` entries; each entry has `base_url`, `api_key`, and `session_key`. Production retains the legacy `agent` mapping, which selects crash. Never move existing thread bindings between hosts implicitly.

Both dedicated runtimes pin Hermes revision `498abb677ec39ea3ae9f8f5ed60e7def6bc47e70`. The reviewed compatibility patch repairs skill discovery, reports host metadata, redacts transcript secrets, and keeps terminal state scoped to the conversation while approval authority stays scoped to the run. Upgrading that pin requires reviewing and validating the patch again. On crash, stage the scripts together, run `prepare-hermes-runtime.sh` as grimlock, then run `activate-hermes-runtime.sh` as root. The latter backs up Hermes state and switches only `hermes-gateway-386gpt.service`. Release installation backs up the app database before migration.

Validation: `go test ./...` and `go vet ./...` in backend; `npm run lint`, `npm run build`, and `npm run test:e2e` in frontend. The browser test expects Vite on port 15173 (override `UI_TEST_ORIGIN`) and mocks the API to exercise recovery and controls. Run `npx playwright install chromium` if the browser is missing.

## Production

The `master` branch deploys through the `386GPT` Jenkins multibranch job. Jenkins reads the Hermes Agent connection file from `/prod/386gpt/hermes-agent-config` in its authenticated local etcd instance, builds and verifies the application, and installs an atomic backend release on `web1`. The API-server bearer credential remains in etcd and in the root-owned runtime file; it is never shipped to the browser.

The production frontend is deployed as a Cloudflare Worker with static assets at `https://386gpt.truvis.co`. The production API is `https://api-386gpt.truvis.co`; it runs as the `386gpt.service` systemd unit on `127.0.0.1:20386`, with nginx and Cloudflare in front. SQLite data is retained outside individual releases at `/var/www/vhosts/api-386gpt.truvis.co/shared/data/386gpt.db`.

Hermes Agent runs on `crash` using the `unsloth` profile and listens only on its
Tailscale address. The profile uses Unsloth Studio at `http://127.0.0.1:8888/v1`
with `HauhauCS/Gemma-4-E2B-Uncensored-HauhauCS-Aggressive`, quantization `Q4_K_P`.
Hermes handles tools, sessions, and memory; Unsloth supplies model inference.
The profile reserves a 65,536-token context and caps generated output at 4,096
tokens, with thinking disabled. Background Hermes title generation is disabled
because 386GPT already titles its threads. Its configuration is in
`deploy/production/hermes-unsloth.yaml`.

Unsloth runs as `unsloth.service` on crash, enabled at boot with automatic
restart. Each service start loads Gemma Q4_K_P with a 66,304-token context and
four parallel slots. A Hermes systemd drop-in orders its boot startup after
Unsloth's authenticated model-readiness check. The Studio UI remains available
at `http://192.168.68.180:8888/`.

To install or update the unit, copy `unsloth.service`, `load-unsloth-model.py`,
and `install-unsloth-service.sh` from `deploy/production/` into the same directory
on crash, then run the installer as root. Stop any manually launched Studio
process first so port 8888 is available. The installer defaults to the dedicated
key file created during setup, `/home/grimlock/.hermes/386gpt-unsloth-api-key`;
override `UNSLOTH_API_KEY_FILE` to use a different private file. It installs a
root-only copy at `/etc/unsloth/api-key`, which systemd supplies to the readiness
helper using `LoadCredential`.

```sh
sudo sh deploy/production/install-unsloth-service.sh
systemctl status unsloth.service
journalctl -u unsloth.service -f
```

`sudo systemctl restart unsloth.service` restarts Studio and reloads Gemma;
Hermes stays running during recovery. Changing the loaded model in Studio is
temporary: the next service start restores this preferred model.

Copy both files from `deploy/production/configure-hermes-agent.sh` and
`deploy/production/hermes-unsloth.yaml` to the same directory on crash. For the
first setup, supply a private file containing a dedicated Unsloth API key:

```sh
sudo UNSLOTH_API_KEY_FILE=/path/to/private/unsloth-key \
  sh deploy/production/configure-hermes-agent.sh
```

Subsequent runs can omit `UNSLOTH_API_KEY_FILE`: the script reuses
`/prod/386gpt/unsloth-api-key` in etcd. It verifies the loaded model before
switching Hermes, creates the `unsloth` profile, installs the provider key in its
private `.env`, and restarts `hermes-gateway-386gpt.service`. It preserves the
existing `/prod/386gpt/hermes-api-key` and private gateway address, updates
`/prod/386gpt/hermes-agent-config` for Jenkins, and writes the client connection to
`/etc/hermes-agent/386gpt-client.yaml`. The old `386gpt` Hermes profile remains
available separately; a new profile has its own Hermes sessions and memory.

When crash does not have the local etcd environment file, the script reads etcd
through its non-interactive SSH connection to `brain`; etcd remains bound to
brain's loopback interface. The public frontend and API must be protected by the
same Cloudflare Access identity policy before deploying the agent-backed build.

## Stack

- React, TypeScript, and Vite
- [BOOTSTRA.386](https://github.com/kristopolous/BOOTSTRA.386) v5 theme from the upstream Git repository
- Go, Gorilla WebSocket, SQLite, and the Hermes Agent session API
- Air as a pinned Go project tool

> **Current security setup:** Hermes uses an isolated non-root Docker runtime on crash and book14. Production requires Cloudflare Access for `mauriciootta@gmail.com`; local development binds to loopback. See [deployment and migration instructions](deploy/isolation/README.md). The previous host-execution installers are disabled.

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

386GPT is a messaging client of the Hermes gateway. The `386gpt-channel` platform plugin accepts authenticated messages and dispatches ordinary `MessageEvent`s through Hermes's normal gateway handler—the same lifecycle used by messaging platforms. No Telegram account, token, library, or service is required. The profile owns the model, provider, system prompt, tools, memory, skills, and command handling. 386GPT does not create an API agent or inject agent instructions.

The transport retains the app's durable request/status interface, but `/v1/runs` on port 8644 is a messaging delivery receipt. It never invokes the standalone API agent path. Native commands such as `/status`, `/context`, `/new`, `/stop`, and `/sethome` go to Hermes. Updates queue through the normal messaging guards, and approval choices resolve Hermes's pending approval. Replies and edits are persisted by the channel; disconnecting the browser does not cancel a message. Interrupted deliveries are never automatically re-executed after a gateway restart.

The conversation panel shows the real host and gateway profile, skills, tool arguments/results, approvals, and stop/update controls. Agent completion is distinct from successful tool execution. Conversation URLs use `/conversations/<id>`. Existing app transcripts remain visible; switching from the former API creates a native gateway session for each conversation rather than importing a different profile's private history.

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

This builds the isolated Hermes container, migrates the existing `386gpt-local` profile data, installs the Docker supervisor `co.truvis.386gpt.hermes` on loopback port 8644, and updates owner-only `~/.hermes/386gpt.yaml`. Docker Desktop and the existing profile/migration venv are required. Restart `go tool air` afterward. The existing default Hermes profile is unaffected. Tailscale access to crash's Unsloth port 8888 is required. `HERMES_SSH_HOST` and `HERMES_CONFIG` override connection installation defaults.

The private development configuration contains `default_runtime: local` and a `runtimes` mapping with `local` and `crash` entries; each entry has `base_url`, `api_key`, and `session_key`. Production retains the legacy `agent` mapping, which selects crash. Never move existing thread bindings between hosts implicitly.

Historical host-profile setup (superseded by the container migration instructions above):

```sh
# On crash, using the corrected 386gpt profile:
~/.hermes/386gpt-runtime/venv/bin/python deploy/hermes-gateway/install-channel.py \
  --profile 386gpt --host 100.74.13.43 --client /etc/hermes-agent/386gpt-client.yaml
sudo systemctl restart hermes-gateway-386gpt.service
```

The dedicated runtime pins Hermes revision `498abb677ec39ea3ae9f8f5ed60e7def6bc47e70`. The small existing compatibility patch supplies skill discovery and runtime metadata. The gateway adapter lives outside Hermes core in the profile's `plugins/386gpt-channel` directory. Run its isolated transport tests with the Hermes venv and `PYTHONPATH` pointing to that checkout:

```sh
PYTHONPATH="$HOME/.hermes/386gpt-runtime" \
  "$HOME/.hermes/386gpt-runtime/venv/bin/python" deploy/hermes-gateway/test_channel.py
```

Connection files point to port 8644; port 8643 remains the old API and is rejected for new app submissions. `switch-client.py CONNECTION_FILE` updates the port without replacing credentials. Jenkins reads `/prod/386gpt/hermes-agent-config`; `deploy/jenkins/update-hermes-base-url.sh` updates that connection. Provider credentials stay entirely within Hermes. The profile-preserving local setup also keeps existing model and provider-key changes.

Validation: `go test ./...` and `go vet ./...` in backend; `npm run lint`, `npm run build`, and `npm run test:e2e` in frontend. The browser test expects Vite on port 15173 (override `UI_TEST_ORIGIN`) and mocks the API to exercise recovery and controls. Run `npx playwright install chromium` if the browser is missing.

## Production

The `master` branch deploys through the `386GPT` Jenkins multibranch job. Jenkins reads the Hermes Agent connection file from `/prod/386gpt/hermes-agent-config` in its authenticated local etcd instance, builds and verifies the application, and installs an atomic backend release on `web1`. The API-server bearer credential remains in etcd and in the root-owned runtime file; it is never shipped to the browser.

The production frontend is deployed as a Cloudflare Worker with static assets at `https://386gpt.truvis.co`. The production API is `https://api-386gpt.truvis.co`; it runs as the `386gpt.service` systemd unit on `127.0.0.1:20386`, with nginx and Cloudflare in front. SQLite data is retained outside individual releases at `/var/www/vhosts/api-386gpt.truvis.co/shared/data/386gpt.db`.

Hermes Agent runs on `crash` using the user-configured `386gpt` profile and listens only on its
Tailscale address. The profile uses Unsloth Studio at `http://127.0.0.1:8888/v1`
with `HauhauCS/Gemma-4-E2B-Uncensored-HauhauCS-Aggressive`, quantization `Q4_K_P`.
Hermes handles tools, sessions, and memory; Unsloth supplies model inference.
The running profile's settings are authoritative. `hermes-unsloth.yaml` is a historical bootstrap template; the messaging-channel installer does not overwrite a profile with it.

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

For an existing corrected profile, use the channel installer above. The older `configure-hermes-agent.sh` bootstraps an `unsloth` profile and changes provider settings; it is not the upgrade path for the user-maintained `386gpt` profile.

## Stack

- React, TypeScript, and Vite
- [BOOTSTRA.386](https://github.com/kristopolous/BOOTSTRA.386) v5 theme from the upstream Git repository
- Go, Gorilla WebSocket, SQLite, and a Hermes messaging gateway platform
- Air as a pinned Go project tool

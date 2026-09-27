# 386GPT

A DOS-inspired chat interface with a React frontend and Go backend. Conversations are persisted in SQLite and new messages stream over WebSockets.

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

The backend talks to Hermes Agent's authenticated API-server session endpoint instead of calling an LLM provider directly. Each 386GPT thread becomes a durable Hermes session, while `agent.session_key` supplies a stable chat identity for long-term memory across threads. Hermes owns the agent loop, tools, skills, memory, and provider selection.

Keep the connection file outside the repository:

```yaml
agent:
  base_url: http://127.0.0.1:8642
  api_key: replace-with-api-server-key
  session_key: agent:main:386gpt:dm:owner
```

Local development uses the same private Hermes agent on `crash` as production.
After the crash setup below, install its connection file on this Mac with
`sh deploy/development/configure-hermes-client.sh`, then restart `go tool air`.
This writes `~/.hermes/386gpt.yaml` with owner-only permissions. The Mac needs
Tailscale access to `100.74.13.43:8643`; it does not need to run the model or hold
the Unsloth API key. Override `HERMES_SSH_HOST` or `HERMES_CONFIG` if needed.

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

Keep Unsloth running with that model loaded and at least 65,536 context tokens.
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

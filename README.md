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

## Production

The `master` branch deploys through the `386GPT` Jenkins multibranch job. Jenkins reads the Hermes Agent connection file from `/prod/386gpt/hermes-agent-config` in its authenticated local etcd instance, builds and verifies the application, and installs an atomic backend release on `web1`. The API-server bearer credential remains in etcd and in the root-owned runtime file; it is never shipped to the browser.

The production frontend is deployed as a Cloudflare Worker with static assets at `https://386gpt.truvis.co`. The production API is `https://api-386gpt.truvis.co`; it runs as the `386gpt.service` systemd unit on `127.0.0.1:20386`, with nginx and Cloudflare in front. SQLite data is retained outside individual releases at `/var/www/vhosts/api-386gpt.truvis.co/shared/data/386gpt.db`.

Hermes Agent runs on `crash` and listens only on its Tailscale address. Run `deploy/production/configure-hermes-agent.sh` as root on `crash` to create or reuse `/prod/386gpt/hermes-api-key`, render the root-owned runtime environment, update `/prod/386gpt/hermes-agent-config`, and restart the gateway. When crash does not have the local etcd environment file, the script reads etcd through its non-interactive SSH connection to `brain`; etcd remains bound to brain's loopback interface. The public frontend and API must be protected by the same Cloudflare Access identity policy before deploying the agent-backed build.

## Stack

- React, TypeScript, and Vite
- [BOOTSTRA.386](https://github.com/kristopolous/BOOTSTRA.386) v5 theme from the upstream Git repository
- Go, Gorilla WebSocket, SQLite, and the Hermes Agent session API
- Air as a pinned Go project tool

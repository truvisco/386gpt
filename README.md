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
- `HERMES_CONFIG`: Hermes YAML configuration, default `~/.hermes/config.yaml`

Frontend environment variables:

- `VITE_API_URL`: backend HTTP origin, default `http://localhost:8080`

The backend reads Hermes's active custom provider, model, base URL, API mode, and API key directly from the YAML file. Credentials remain outside this repository. The configured provider must expose an OpenAI-compatible streaming Chat Completions endpoint. Production uses `https://freellmapi.portnumber53.com/v1`, which follows the monitored `brain.portnumber53.com` address and reaches the FreeLLM service through the existing `crash` reverse proxy.

## Production

The `master` branch deploys through the `386GPT` Jenkins multibranch job. Jenkins reads the Hermes configuration from `/prod/386gpt/hermes-config` in its authenticated local etcd instance, builds and verifies the application, and installs an atomic backend release on `web1`.

The production frontend is deployed as a Cloudflare Worker with static assets at `https://386gpt.truvis.co`. The production API is `https://api-386gpt.truvis.co`; it runs as the `386gpt.service` systemd unit on `127.0.0.1:20386`, with nginx and Cloudflare in front. SQLite data is retained outside individual releases at `/var/www/vhosts/api-386gpt.truvis.co/shared/data/386gpt.db`.

## Stack

- React, TypeScript, and Vite
- [BOOTSTRA.386](https://github.com/kristopolous/BOOTSTRA.386) v5 theme from the upstream Git repository
- Go, Gorilla WebSocket, and SQLite
- Air as a pinned Go project tool

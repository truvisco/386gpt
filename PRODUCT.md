# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

A power user/technical hobbyist who wants a retro DOS-styled AI chat for personal projects and learning. They value the terminal aesthetic, persistent bookmarkable conversations, and the ability to run an AI agent with visible tool use in an isolated workspace.

## Product Purpose

386GPT is a DOS-inspired chat interface where each user gets a fully isolated non-root Docker Hermes runtime with their own model context, tools, skills, and workspace — no shared state between accounts. Conversations are persistent SQLite-backed threads with bookmarkable URLs, WebSocket streaming, and real-time agent tool/approval visibility. The product combines the feel of a CLI with web accessibility, running on a Hermes messaging gateway with Unsloth-served models.

Success means: a user can sign in with Google, get their own isolated agent environment, chat persistently across sessions, use GitHub auth per conversation workspace, and see the agent's reasoning (tools, approvals, streaming output) in a terminal-styled UI that feels authentic to the DOS era.

## Positioning

The meaningfully different mechanism: **per-account isolated Hermes runtimes** provisioned on-demand via Docker, each with private model context, toolset, skills, and filesystem — a architecture inherited from the Hermes gateway that neighboring products (single-tenant SaaS chat UIs, shared-context wrappers) cannot trivially replicate. The BOOTSTRA.386 visual world is the expressive layer; the isolation architecture is the structural moat.

## Operating Context

- **Development**: Vite dev server (port 21350) + Go backend with Air hot reload (port 21351), SQLite at `backend/data/386gpt.db`. HTTPS dev proxy at `https://386gpt.dev14.portnumber53.com` for OAuth.
- **Production**: Cloudflare Workers frontend at `https://386gpt.truvis.co`, Go API at `https://api-386gpt.truvis.co` (systemd on web1:20386, nginx + Cloudflare), shared SQLite at `/var/www/vhosts/api-386gpt.truvis.co/shared/data/386gpt.db`.
- **Hermes/Unsloth**: Runs on `crash` (Tailscale) with `386gpt` profile, Unsloth Studio at `http://127.0.0.1:8888/v1` serving `HauhauCS/Gemma-4-E2B-Uncensored-HauhauCS-Aggressive` Q4_K_P (66K context, 4 slots). Hermes handles tools/sessions/memory; Unsloth supplies inference.
- **Deployment**: Jenkins multibranch rebuilds Hermes runtime on `crash`, reconciles account stacks without replacing state volumes, atomic backend release on `web1`.
- **Workflows**: Google OAuth → private container → persistent conversations → WebSocket streaming → agent tool visibility → GitHub auth per workspace (device code or PAT) → git credential helper + SSH→HTTPS rewrite via egress proxy.

## Capabilities and Constraints

**Confirmed functionality:**
- Google OAuth sign-in → per-account isolated Hermes container (non-root Docker)
- SQLite persistence: conversations, messages, runs, approvals, thread metadata
- WebSocket `/ws/` with auto-reconnect (exponential backoff, max 10s), `/ws` legacy support
- Message streaming with configurable typewriter animation (TURBO ×4 speed toggle, respects `prefers-reduced-motion`)
- Agent panel: tool calls, arguments, results, approval requests, stop/steer controls
- GitHub authentication per conversation workspace: device code flow + PAT, credentials in `~/.config/gh` (0700/0600), git credential helper, SSH remotes rewritten to HTTPS
- Account isolation tests: cross-account reads return 404, credentials never in conversation history or browser responses
- Bookmarkable `/conversations/<id>` URLs, browser Back/Forward navigation
- Quick prompts on empty state, copy-to-clipboard on messages

**Technical constraints:**
- Stack: React/TypeScript/Vite, BOOTSTRA.386 v5 (from upstream Git, not npm), Go/Gorilla WebSocket/SQLite, Hermes gateway platform plugin, Air for Go hot reload
- Frontend deploys as Cloudflare Worker with static assets; Go API as systemd unit behind nginx
- Hermes Agent connection file at `~/.hermes/386gpt.yaml` (dev) or etcd (prod); API bearer credential never shipped to browser
- `gh` CLI required in Hermes image; deployment tokens (`GH_TOKEN`, `GITHUB_TOKEN`, etc.) stripped from interactive auth environment
- Conversation URLs use path routing; no query-string session IDs

**Explicitly undecided:**
- Whether a landing/marketing surface will exist (currently none)
- Multi-model selection UI (currently single profile: Gemma Q4_K_P)
- Team/workspace sharing model beyond per-account isolation

## Brand Commitments

- **Name**: 386GPT (all caps, with number)
- **Visual identity**: BOOTSTRA.386 v5 theme — DOS aesthetic, green-on-black, monospace, box-drawing characters. Sourced from upstream Git repository, not npm.
- **Voice**: Technical, direct, terminal-native. Commands and system messages in caps (UPLINK ACTIVE, STANDBY, HERMES SESSION, TURBO, NEW CHAT). Minimal hand-holding.
- **Assets**: Custom ASCII logo (386 block art in welcome screen). No logo file, no custom typography beyond the DOS monospace.
- **Terminology**: UPLINK, TURBO, CONVERSATIONS, HERMES SESSION, SQLITE ARCHIVE, AGENT TOOL, APPROVAL REQUIRED.

## Evidence on Hand

**Real, demonstrable:**
- Working app with SQLite persistence, WebSocket streaming, typewriter animation, agent tool visibility, GitHub auth, account isolation
- BOOTSTRA.386 v5 from upstream Git repo (not npm)
- Hermes Agent with Unsloth/Gemma on `crash` (Tailscale)
- Playwright E2E tests: account UI, agent UI, GitHub settings (device flow, token entry, disconnect, CTA contrast ≥4.5:1)
- Go tests: account isolation, WebSocket routes, GitHub settings account/runtime/secrets, Google OAuth validation
- Worker tests: public login assets, fixed API origin, session cookie forwarding, identity-header stripping, WebSocket upgrade

**Absent (future work must not fabricate):**
- Marketing copy, testimonials, case studies, press
- Screenshots for landing/marketing
- Pricing, licensing, or deployment claims beyond what's in README

## Product Principles

1. **Isolation over convenience** — Every account gets its own runtime, workspace, and model context. No shared state, no cross-account leakage, no implicit binding migration.
2. **Terminal authenticity over modernity** — The DOS aesthetic is not a skin; it's the interaction model. Monospace, caps, box-drawing, typewriter streaming, command-style controls.
3. **Visible agency** — The agent's reasoning (tools, args, results, approvals) is surfaced in real time, not hidden behind a "thinking" spinner. User sees what the agent does.
4. **Durable primitives** — SQLite, WebSocket, bookmarkable URLs, per-account containers. No proprietary lock-in at the data layer.
5. **Security fails closed** — OAuth validation rejects forged identities; cross-account reads return 404; credentials never in runs/history/browser; deployment tokens stripped from interactive auth.

## Accessibility & Inclusion

- WCAG AA contrast required (Playwright test enforces CTA button contrast ≥4.5:1)
- **Keyboard navigation is essential** — the DOS visual language demands full keyboard operability: Tab traversal through all interactive elements, Tab trapping in overlays (Settings), Escape to close modals/overlays, Enter to send, Shift+Enter for new line, arrow keys where native (thread list, scroll). No mouse-only interactions.
- `prefers-reduced-motion` respected for typewriter animation (animation disabled when set)
- No other product-specific accessibility requirements established
# Towline

**The agentic engineering SDK for Portainer. One command gives your AI agent a complete environment to build, deploy, and operate software on your Portainer infrastructure.**

<!-- Badges: uncomment and update when applicable
[![Build](https://img.shields.io/github/actions/workflow/status/changethisusername/towline/ci.yml?branch=main)](https://github.com/changethisusername/towline/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/changethisusername/towline)](https://goreportcard.com/report/github.com/changethisusername/towline)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
-->

AI coding agents can write code, run tests, and manage git. But they stop at the deploy boundary. Towline removes that boundary. Built on [Portainer](https://portainer.io), a single `towline init` provisions an isolated container stack, wires up MCP tools, installs operational skills, and configures your agent — so it can ship and operate what it builds, not just write it.

[Website](https://towline.dev) | [Getting Started](docs/getting-started.md) | [User Guide](docs/user-guide.md)

---

## The idea

```
  $ towline init my-saas --template ai-stack
  $ cd my-saas && claude

  You: "Build a RAG chatbot with document upload. Deploy it, expose it
       on chat.mylab.local, and make sure everything is healthy."

  Agent: writes code → deploys services → configures routing
         → checks health → iterates on errors → ships it
```

Your agent doesn't just generate code. It deploys containers, reads logs, scales services, manages domains, debugs connectivity, and rolls back broken deployments. Towline gives it everything it needs to do this safely.

---

## What `towline init` creates

```
my-saas/
├── AGENT.md                    # Instructions for your agent
├── docker-compose.yml          # Stack definition (from template)
├── skills/
│   ├── towline-devops.md       # How to deploy, debug, and operate
│   └── ai-ops.md               # Stack-specific knowledge (from pack)
├── .mcp.json                   # Claude Code ─┐
├── .cursor/mcp.json            # Cursor       ├─ MCP wired & ready (0600, gitignored)
├── .gemini/settings.json       # Gemini CLI   ─┘
├── towline.json                # Project config
└── .git/                       # Ready to push
```

**Behind the scenes**, Towline also provisioned:
- A Portainer team with scoped credentials (your agent can't see other projects)
- A Docker Compose stack on your infrastructure
- A dedicated API token that only accesses this project's containers (passed to the MCP server via the `TOWLINE_PORTAINER_TOKEN` env var, never on the command line)

Open the project in any MCP-compatible agent and start building.

---

## Quick start

### 1. Install

```bash
curl -fsSL https://towline.dev/install | sh
```

The installer verifies every archive's SHA-256 against the release's `checksums.txt` before extracting it, and aborts if verification fails or isn't possible.

### 2. Connect to Portainer

```bash
towline setup
```

Points Towline at your Portainer instance (CE 2.28+). Interactive wizard — takes 30 seconds. Portainer's TLS certificate is verified unless you tell the wizard it's self-signed. It also asks how prod-tier operations should be approved: by a human in Towline's built-in approval server (recommended), or by the agent itself (for autonomous setups). Re-running `towline setup` keeps your existing approval settings; change them any time with `towline approvals setup`.

### 3. Create a project

```bash
towline init my-app --template web-app
```

Project names must match `^[a-z0-9][a-z0-9_-]{0,62}$` (lowercase letters, digits, `-`, `_`).

### 4. Start building

```bash
cd ~/projects/my-app
claude   # or cursor, gemini, windsurf, codex...
```

Your agent has full deploy access. Ask it to build something.

---

## Starter kits

Every template gives your agent a running stack and the knowledge to operate it.

**Simple templates** (compose only):

| Template | Stack |
|----------|-------|
| `default` | Empty — agent builds from scratch |
| `web-app` | Nginx + PostgreSQL + Redis |
| `api` | Node.js API + PostgreSQL |

**Packs** (compose + agent skills + additional MCP tools):

| Pack | Stack | Agent gets |
|------|-------|-----------|
| `ai-stack` | Ollama + Open WebUI + PostgreSQL | AI operations skill |
| `n8n` | n8n + PostgreSQL | n8n workflow operations skill |

```bash
towline init my-project --template ai-stack
```

Packs are the key to making agents effective at specific domains. The compose file gives them infrastructure; the skills teach them how to operate it; the MCP configs give them additional tools. [Create your own packs](#custom-packs) for any stack.

---

## What your agent can do

Towline exposes 10 high-level tools via MCP, plus scoped access to the full Docker API. Your agent uses service names, not container IDs.

### Observe

| Tool | What it does |
|------|-------------|
| `towline_service_health` | State, uptime, restarts, CPU/memory, exit codes — for one or all services |
| `towline_service_logs` | Logs with filtering, Docker header stripping |
| `towline_env_get` | Environment variables (sensitive values masked) |
| `towline_domains_list` | All domain routing mappings |
| `towline_deployments` | What was deployed, when, what changed |

### Act

| Tool | What it does | Prod approval |
|------|-------------|:---:|
| `towline_env_set` | Set environment variables | Yes |
| `towline_domains_add` | Route a domain to a service (Traefik / Caddy / Cloudflare) | Yes |
| `towline_domains_remove` | Remove domain routing | Yes |
| `towline_scale` | Scale service replicas | No |
| `towline_exec` | Run commands inside containers | Yes |

Plus the full Portainer stack API (`updateLocalStack`, `getLocalStackFile`, etc.) — all scoped to this project. Omitting `env` in `updateLocalStack` keeps the stack's current variables, and every `updateLocalStack` and `towline_env_set` is recorded in `towline_deployments` (env changes record the variable name only, never the value).

---

## How agents learn to operate

The DevOps skill at `skills/towline-devops.md` isn't documentation — it's **agent training**. It teaches your agent:

- **Diagnostic thinking**: check health first, then logs, then dependencies, then env vars, then compose, then deployment history. Stop as soon as you find the cause.
- **Deployment discipline**: always read the current compose before modifying. Set env vars before deploying. Include descriptions for audit trail.
- **Failure patterns**: exit code 137 means OOM. "Connection refused" means the dependency isn't ready. High restart count means crash loop — don't restart, read logs.
- **The golden rule**: always submit the COMPLETE compose file. Partial submits delete services.

Pack skills add domain-specific knowledge on top. The `ai-stack` pack teaches your agent how to pull Ollama models, diagnose GPU issues, and manage Open WebUI connections.

---

## Safety model

Three independent layers ensure your agent only operates within its project:

**Layer 1 — Portainer team scoping** (hard boundary)
Each project gets a dedicated Portainer team with its own API key. The team gets Portainer's "Standard user" environment role (not Environment administrator), so one project's key can't manage other projects' stacks. Even if everything else fails, Portainer rejects cross-project requests. Because project users are non-admins, Portainer's environment security settings for regular users (e.g. disabling bind mounts or privileged mode) also apply — a good server-side backstop.

**Layer 2 — MCP stack filtering** (agent experience)
Docker API responses are filtered to show only this project's containers. Clear error messages if an agent references anything out of scope. On top of that:
- **Compose security policy** (dev and prod): `createLocalStack` / `updateLocalStack` reject compose files that could escape the container sandbox — `privileged`, `cap_add`, `devices`, host/container `network_mode`, `pid`, `ipc`, `uts`, `userns_mode`, `cgroup`, unconfined/disabled `security_opt`, host bind mounts (absolute, `./`, `~`, `${VAR}` sources, or `type: bind`), `volumes_from`, volume `driver_opts` or non-local drivers, `secrets`/`configs` with `file:`, `env_file` outside the stack directory, `external_links`, `build` from a local context (use a git/https URL or a prebuilt image), and top-level `include`/`extends`. Named volumes and tmpfs are fine. Specific bind mounts can be allowed per project in `towline.json` (see [Per-project](#per-project-towlinejson)).
- **Restricted `dockerProxy`**: the only mutations allowed are container lifecycle actions (`start`, `stop`, `restart`, `kill`, `pause`, `unpause`, `wait`, `resize`, `rename`, `update`) and `DELETE /containers/{id}` on the stack's own containers, in the stack's own environment. Container create, exec, archive upload, and anything on networks, volumes, images, or system are rejected, as are paths with query strings, `%`-encoding, `..` segments and similar tricks.

**Layer 3 — Tier-based approval** (deployment control)
In `dev` tier, agents have full autonomy. In `prod` tier, deploys, configuration changes, exec, and destructive operations need approval. You choose the approval mode at `towline setup` (or later with `towline approvals setup`); it's stored as `approval.mode` in `~/.towline/config.yaml` and passed to `towline-mcp` as `-approval-mode human|agent`.

**Human mode (recommended)** — a human approves each gated operation in an approval server. Towline ships one:

```bash
towline approvals setup --mode human   # generates tokens; prints your approver token ONCE
towline approvals serve                # listens on 127.0.0.1:8787, web UI at /ui
```

```
Agent: "Production operation requires human approval.
        Action: updateLocalStack
        An approval request has been sent (ID a1b2c3d4)."

You: [approve it at http://127.0.0.1:8787/ui, or: towline approvals approve a1b2c3d4]

Agent: [re-calls with the token] → [deploys] → [verifies health] → "All services healthy."
```

- `approvals setup` generates a **webhook token** (put in the generated MCP config as `TOWLINE_APPROVAL_WEBHOOK_TOKEN`; it can only submit and poll requests, never approve) and an **approver token**, shown once (only its SHA-256 is stored). Use the approver token to log in to the UI or with `towline approvals list | approve <id> | reject <id>`, which prompt for it on stdin — deliberately not read from env vars or flags, so agents in the same shell can't pick it up.
- Optional: `--ntfy <topic URL>` for push notifications, `--public-url` for the link in them, `--listen` to change the address (a warning is printed if it listens beyond localhost — put it behind HTTPS).
- The built-in server keeps requests in memory: restarting it drops pending requests (the agent just requests again).
- To use your own server instead: `towline approvals setup --mode human --url <server> [--webhook-token <token>]`. It must implement:

```
POST /       — receive approval request (id, project, action, description, stackContent)
GET  /{id}   — return {"status": "pending" | "approved" | "rejected"}
```

How humans approve on an external server is up to it — but it must authenticate whoever approves, and those endpoints must not be reachable by the agent.

When the agent calls a gated tool, `towline-mcp` sends the request and returns an `approvalToken`. Re-calling with identical arguments and that token runs the operation only once the server reports `approved` (`pending` runs nothing; `rejected` refuses). Tokens are single-use, bound to the tool and exact arguments, and expire after 30 minutes; undecided requests expire and are reported as rejected. If human mode is chosen but no approval server URL is configured, prod operations that need approval are refused.

**Agent mode** — for autonomous/agentic setups where the agent operating the MCP is in charge. The first call returns an `approvalToken`; the agent confirms by re-calling with identical arguments plus the token (single-use, bound to tool and exact arguments, 30 minutes). This is an explicit confirmation step, not a human gate.

If no mode is set (configs from earlier releases, or `towline-mcp` run without `-approval-mode`), the mode is human if `-approval-webhook` is set and agent otherwise — the previous behavior, so existing prod projects keep working after upgrading.

---

## Domain routing

Three proxy backends, auto-detected or explicit:

| Backend | How it works | Best for |
|---------|-------------|----------|
| **Traefik** | Docker labels in compose | Most setups — zero external config |
| **Caddy** | Admin API calls | Caddy users — automatic TLS |
| **Cloudflare Tunnels** | Delegates to Cloudflare MCP | External access without port forwarding |

Caddy routes are namespaced per stack (`towline:<stack>:<service>:<domain>`): a project only sees and removes its own routes, the service must exist in its compose file, and a hostname already routed elsewhere can't be claimed. Routes created by older versions (`towline-<service>-<domain>`) are no longer managed and must be removed manually.

```
You: "Expose the API on api.mylab.local"
Agent: [adds Traefik labels] → [redeploys] → [verifies routing]
```

---

## Custom packs

Create reusable starter kits that bundle infrastructure, agent skills, and tooling.

```
~/.towline/packs/my-pack/
├── pack.yaml           # Pack manifest
├── compose.yml         # Docker stack
├── my-ops-guide.md     # Domain-specific agent skill
└── ...
```

**pack.yaml**:
```yaml
name: my-pack
description: My custom engineering environment

compose: compose.yml

# Skills teach your agent how to operate this stack
skills:
  - my-ops-guide.md

# Additional MCP servers for the agent
mcps:
  my-tool:
    command: "npx"
    args: ["-y", "@my-org/my-mcp-server"]
```

```bash
towline init new-project --template my-pack
```

The pack's skills are copied into the project alongside the base DevOps skill. MCP configs are merged into `.mcp.json`, `.cursor/mcp.json`, and `.gemini/settings.json`. Your agent gets domain expertise out of the box. Pack MCP servers run automatically with your privileges when the agent starts — only add servers from verified publishers, pinned to an exact version.

---

## CLI reference

| Command | Description |
|---------|-------------|
| `towline setup` | Connect to your Portainer instance |
| `towline init <name>` | Create a project with full provisioning |
| `towline list` | List all projects |
| `towline destroy <name>` | Tear down Portainer resources (each ID in `towline.json` is verified against Portainer first; mismatches are skipped). Also works for projects created before name validation |
| `towline refresh [--all \| <name>...]` | Bring existing projects up to date with this release (see [Upgrading](#upgrading)) |
| `towline approvals setup` | Choose human or agent approval for prod operations |
| `towline approvals serve` | Run the built-in approval server (web UI at `/ui`) |
| `towline approvals list \| approve <id> \| reject <id>` | Decide approval requests from the terminal (prompts for the approver token) |
| `towline update` | Update Towline to the latest release |
| `towline promote <name>` | Add prod tier *(coming soon)* |
| `towline rotate-keys <name>` | Rotate API credentials *(coming soon)* |
| `towline status <name>` | Live stack health *(coming soon)* |

### towline init flags

| Flag | Default | Description |
|------|---------|-------------|
| `--tier` | `dev` | `dev` (full autonomy) or `prod` (approval-gated) |
| `--template` | `default` | Template or pack name |
| `--with-prod` | `false` | Also create a prod-tier stack |

---

## Configuration

### Global (`~/.towline/config.yaml`)

Created by `towline setup`. Stored with `0600` permissions.

```yaml
portainer_url: "https://192.168.1.50:9443"
portainer_admin_key: "ptr_xxxx..."
portainer_env_id: 2
projects_dir: "~/projects"
skip_tls_verify: false        # true only if Portainer uses a self-signed cert
approval:
  mode: human                 # human or agent
  url: "http://127.0.0.1:8787"  # approval server towline-mcp talks to
  webhook_token: "..."        # towline-mcp -> approval server (submit/poll only)
  listen: "127.0.0.1:8787"    # built-in server listen address
  approver_token_hash: "..."  # SHA-256 of your approver token
  ntfy_url: "https://ntfy.sh/my-private-topic"  # optional push notifications
  public_url: "https://approvals.mylab.local"   # optional, for notification links
```

The admin key is used only by the CLI for provisioning. It is never passed to agents. Prod-tier MCP configs get `-approval-mode` (plus `-approval-webhook` and the webhook token in human mode); `skip_tls_verify: true` adds `-skip-tls-verify`.

Configs from earlier releases have no `skip_tls_verify` key and keep skipping certificate verification as before, with a warning from the CLI. Set `skip_tls_verify: false` to verify (or `true` to keep skipping and silence the warning), or re-run `towline setup`, which asks. New setups verify by default.

### Per-project (`towline.json`)

Created by `towline init`. Contains stack metadata and scoped credentials. The generated MCP configs (`.mcp.json`, `.cursor/mcp.json`, `.gemini/settings.json`) pass the project token via the `TOWLINE_PORTAINER_TOKEN` env var and are written with `0600` permissions. All token-bearing files are in the generated `.gitignore`; in an existing codebase, `towline init` appends any missing entries to its `.gitignore`.

`towline.json` can also relax the compose security policy for one project (edited by you, not the agent):

```json
"compose_policy": { "mode": "enforce", "allow_bind_mounts": ["/srv/app", "."] }
```

An absolute path allows itself and anything below it; `"."` allows relative paths inside the stack directory; `..`, `~` and `${VAR}` sources are never allowed. `"mode": "off"` disables the policy. The other policy rules still apply when bind mounts are allowed. Run `towline refresh` after editing; this renders `-allow-bind-mounts` / `-compose-policy off` into the MCP configs.

---

## Upgrading

```bash
towline update
towline refresh --all     # or: towline refresh <name>...  (or plain `towline refresh` inside a project)
```

Existing projects keep working without `refresh` (legacy `-token` flag, agent-confirmed prod approvals) — they just don't get this release's hardening. `refresh`, per project:

- rewrites only the `towline-<tier>` entry in `.mcp.json`, `.cursor/mcp.json` and `.gemini/settings.json` — other MCP servers, user-added flags (`-proxy`, `-caddy-api`, …) and extra env vars are kept; the token moves from `-token` to the `TOWLINE_PORTAINER_TOKEN` env var; approval mode and compose policy are applied
- makes sure `.claude/settings.json` allows `mcp__towline-<tier>`, keeping your other settings
- fixes permissions (`0600` files, `0700` dirs) and `.gitignore`, and warns if token-bearing files are tracked by git (`git rm --cached`; revoke the token if the repo was pushed)
- moves the project team to the Standard user role, after checking the team is `team-<stack>` (`--keep-role` skips this)
- on first run, allows the deployed stack's existing bind mounts in `compose_policy.allow_bind_mounts` — except host-control paths (`/`, `docker.sock`, `/var/lib/docker`, `/etc`, `/root`, `/proc`, `/sys`, `/dev`, `/boot`, `/home`) — and prints any remaining policy violations and how to resolve them

Restart your agent sessions afterwards. `tools.yaml` / `towline-tools.yaml` in project directories are upgraded automatically by `towline-mcp` when older than the embedded version (the old copy is kept as `.bak`).

---

## Development

```bash
git clone https://github.com/changethisusername/towline.git
cd towline
make build       # Both binaries → dist/
make test        # 207 tests (unit + integration + E2E regression)
make vet         # Go vet
make fmt         # gofmt
```

Cross-compile: `PLATFORM=linux ARCH=arm64 make build`

### Architecture

Towline is two Go binaries and a set of embedded templates:

- **`towline-mcp`** — MCP server (forked from [Portainer MCP](https://github.com/portainer/portainer-mcp) v0.7.0) with middleware for stack scoping, tier gating, and container ownership, plus 10 high-level operational tools
- **`towline` CLI** — project scaffolding and Portainer provisioning
- **Templates & skills** — embedded via `embed.FS`, overridable in `~/.towline/`

The Portainer MCP fork is designed to stay cleanly mergeable with upstream. All Towline additions live in new files and packages.

See [CONTRIBUTING.md](CONTRIBUTING.md) for project structure and development workflow.

---

## Roadmap

**Now**: Portainer deployment with three-layer permissions, 10 MCP tools, template packs with skills and MCP configs.

**Next**: `towline promote` (dev-to-prod workflow), `towline rotate-keys`, `towline status`, more approval integrations (e.g. Telegram).

**Later**: Encrypted keystore. Community pack registry. Plugin system for custom tools.

---

## License

Apache License 2.0. See [LICENSE](LICENSE).

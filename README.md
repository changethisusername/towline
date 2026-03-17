# Towline

**The agentic engineering SDK. One command gives your AI agent a complete environment to build, deploy, and operate software.**

<!-- Badges: uncomment and update when applicable
[![Build](https://img.shields.io/github/actions/workflow/status/changethisusername/towline/ci.yml?branch=main)](https://github.com/changethisusername/towline/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/changethisusername/towline)](https://goreportcard.com/report/github.com/changethisusername/towline)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
-->

AI coding agents can write code, run tests, and manage git. But they stop at the deploy boundary. Towline removes that boundary. A single `towline init` gives your agent an isolated deployment environment, operational tools, domain-specific skills, and the knowledge to ship and operate what it builds — not just write it.

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
├── .claude/settings.json       # Claude Code ─┐
├── .cursor/mcp.json            # Cursor       ├─ MCP wired & ready
├── .gemini/settings.json       # Gemini CLI   ─┘
├── towline.json                # Project config
└── .git/                       # Ready to push
```

**Behind the scenes**, Towline also provisioned:
- A Portainer team with scoped credentials (your agent can't see other projects)
- A Docker Compose stack on your infrastructure
- A dedicated API token that only accesses this project's containers

Open the project in any MCP-compatible agent and start building.

---

## Quick start

### 1. Install

```bash
curl -fsSL https://towline.dev/install | sh
```

### 2. Connect your infrastructure

```bash
towline setup
```

Points Towline at your [Portainer](https://portainer.io) instance. Interactive wizard — takes 30 seconds.

### 3. Create a project

```bash
towline init my-app --template web-app
```

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
| `ai-stack` | Ollama + Open WebUI + PostgreSQL | AI operations skill, Ollama MCP |
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
| `towline_scale` | Scale service replicas | Yes |
| `towline_exec` | Run commands inside containers | Yes |

Plus the full Portainer stack API (`updateLocalStack`, `getLocalStackFile`, etc.) — all scoped to this project.

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
Each project gets a dedicated Portainer team with its own API key. Even if everything else fails, Portainer rejects cross-project requests.

**Layer 2 — MCP stack filtering** (agent experience)
Docker API responses are filtered to show only this project's containers. Clear error messages if an agent references anything out of scope.

**Layer 3 — Tier-based approval** (human control)
In `dev` tier, agents have full autonomy. In `prod` tier, destructive and configuration-changing operations pause in the chat and ask for your approval before executing.

```
Agent: "Production operation requires approval.
        Action: updateLocalStack
        To confirm, re-call with approvalToken: 'a1b2c3d4'"

You: "Yes, deploy it."

Agent: [deploys] → [verifies health] → "All services healthy."
```

---

## Domain routing

Three proxy backends, auto-detected or explicit:

| Backend | How it works | Best for |
|---------|-------------|----------|
| **Traefik** | Docker labels in compose | Most setups — zero external config |
| **Caddy** | Admin API calls | Caddy users — automatic TLS |
| **Cloudflare Tunnels** | Delegates to Cloudflare MCP | External access without port forwarding |

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

The pack's skills are copied into the project alongside the base DevOps skill. MCP configs are merged into agent settings. Your agent gets domain expertise out of the box.

---

## CLI reference

| Command | Description |
|---------|-------------|
| `towline setup` | Connect to your Portainer instance |
| `towline init <name>` | Create a project with full provisioning |
| `towline list` | List all projects |
| `towline destroy <name>` | Tear down Portainer resources |
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
```

The admin key is used only by the CLI for provisioning. It is never passed to agents.

### Per-project (`towline.json`)

Created by `towline init`. Contains stack metadata and scoped credentials.

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

**Now**: Portainer-backed deployment with three-layer permissions, 10 MCP tools, template packs with skills and MCP configs.

**Next**: `towline promote` (dev → prod workflow), `towline rotate-keys`, `towline status`, approval server integrations (Telegram, ntfy.sh).

**Later**: Additional deployment backends beyond Portainer. Encrypted keystore. Community pack registry. Plugin system for custom tools.

---

## License

Apache License 2.0. See [LICENSE](LICENSE).

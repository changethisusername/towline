# Towline User Guide

This guide covers day-to-day usage of Towline — how to work with your agent to build, deploy, and operate software in Towline-managed projects. For installation and first-project setup, see [Getting Started](getting-started.md).

## Table of contents

- [Project lifecycle](#project-lifecycle)
- [Working with your agent](#working-with-your-agent)
- [Managing services](#managing-services)
- [Domain routing](#domain-routing)
- [Environment variables](#environment-variables)
- [Deployment history and rollbacks](#deployment-history-and-rollbacks)
- [Debugging and diagnostics](#debugging-and-diagnostics)
- [Production tier](#production-tier)
- [Templates and customization](#templates-and-customization)
- [Multiple agents and editors](#multiple-agents-and-editors)
- [CLI command reference](#cli-command-reference)
- [MCP tool reference](#mcp-tool-reference)

---

## Project lifecycle

### Creating a project

```bash
# Basic dev project (empty compose)
towline init my-app

# Dev project with web-app template (app + Postgres + Redis)
towline init my-app --template web-app

# Project with both dev and prod tiers
towline init my-app --with-prod

# Direct prod project
towline init my-app --tier prod

# Template pack (compose + skills)
towline init my-app --template ai-stack
```

Project names must match `^[a-z0-9][a-z0-9_-]{0,62}$` (lowercase letters, digits, `-` and `_`, starting with a letter or digit).

Every `towline init` creates:
- A Portainer team (with the "Standard user" environment role) and user with scoped API key
- A Docker Compose stack on your Portainer instance
- Agent configuration files for Claude Code, Cursor, and Gemini CLI
- The DevOps skill file that guides agent behavior
- A git repository with an initial commit
- A `.gitignore` that excludes token-bearing files (`.mcp.json`, `.claude/`, `.cursor/`, `.gemini/`, `towline.json`). In an existing codebase, missing entries are appended to its `.gitignore`.

### Listing projects

```bash
towline list
```

Shows all Towline-managed projects with their stack name, tier, and status.

### Destroying a project

```bash
towline destroy my-app           # interactive confirmation
towline destroy my-app --confirm # skip confirmation
```

This removes the Portainer stack, service user, and team. Because `towline.json` lives in the project directory (which the agent can write), each ID in it is verified against Portainer before deletion: the stack must be named `<project>-<tier>`, the user `towline-<stack>`, and the team `team-<stack>`. Mismatches are skipped with a message. Local files are preserved — delete them manually if needed.

---

## Working with your agent

### Starting a session

Open your project directory in any MCP-compatible agent:

```bash
cd ~/projects/my-app
claude       # Claude Code
cursor .     # Cursor
gemini       # Gemini CLI
```

The MCP server starts automatically as a child process. The agent reads `AGENT.md` and the DevOps skill, then has access to all Towline tools.

### What agents can do

Agents can perform any container management task within their scoped stack:

- **Deploy services**: modify the compose file, deploy updates
- **Read logs**: fetch and filter container logs
- **Check health**: monitor CPU, memory, restart counts, exit codes
- **Manage config**: set environment variables
- **Route domains**: add/remove Traefik labels, Caddy routes, or Cloudflare tunnels
- **Scale services**: adjust replica counts
- **Execute commands**: run debugging commands inside containers
- **Review history**: see what was deployed, when, and what changed

### How agents use the tools

When you ask an agent to do something, it translates your request into MCP tool calls. For example:

**You say**: "Deploy a Postgres database with persistent storage"

**Agent does**:
1. `getLocalStackFile` — reads current compose
2. Adds the postgres service with volumes to the compose YAML
3. `updateLocalStack` — deploys the updated compose
4. `towline_service_health` — verifies the database is running
5. Reports the result to you

The DevOps skill at `skills/towline-devops.md` teaches agents the correct order of operations and common pitfalls.

### The golden rule: complete compose files

The most important thing to understand is that `updateLocalStack` requires the **complete** compose file. If the agent submits only the new service, Portainer removes all existing services. The DevOps skill enforces this pattern:

1. Read current compose (`getLocalStackFile`)
2. Modify it (add/change/remove services)
3. Submit the full result (`updateLocalStack`)

If `env` is omitted from `updateLocalStack`, the stack's current environment variables are kept (older versions wiped them). Internal `_TOWLINE_*` variables and deployment history are always preserved.

After a successful `deleteLocalStack`, the agent can call `createLocalStack` again without restarting the MCP server.

### Compose security policy

In both dev and prod, `createLocalStack` and `updateLocalStack` reject compose files that could give a container access to the host:

- `privileged`, `cap_add`, `devices`
- `network_mode: host` or `container:...`, `pid`, `ipc`, `uts`, `userns_mode`, `cgroup`
- `security_opt` with `unconfined` or `disable`
- Host bind mounts — absolute, relative (`./`), `~`, or `${VAR}` sources, and long-syntax `type: bind`
- `volumes_from`
- Volumes with `driver_opts` or a non-`local` driver
- `secrets` / `configs` with `file:`
- `env_file` outside the stack directory
- Top-level `include` / `extends`

Named volumes and `tmpfs` are fine. As a server-side backstop, you can also restrict non-admin users in Portainer's environment security settings (e.g. disable bind mounts and privileged mode for regular users) — project users are non-admins, so those settings apply to them.

---

## Managing services

### Checking health

Ask your agent to check service health at any time:

> "Check the health of all services"

This calls `towline_service_health` which returns for each container:
- **State**: running, exited, restarting, etc.
- **Uptime**: how long it's been running
- **Restart count**: high counts indicate crash loops
- **CPU and memory usage**: current utilization
- **Exit code**: if stopped, why (137=OOM, 1=app error, 143=graceful shutdown)

### Scaling

> "Scale the worker service to 3 replicas"

The agent calls `towline_scale` which modifies `deploy.replicas` in the compose file and redeploys. If the service has persistent volumes, the agent will warn you about potential data corruption.

### Running commands

> "Run `psql -l` inside the database container to list databases"

The agent calls `towline_exec` to execute the command inside the running container. In prod tier, this requires your approval.

---

## Domain routing

Towline supports three reverse proxy backends. The backend is auto-detected from your compose file or set explicitly.

### Traefik (compose labels)

Most homelab setups with Portainer use Traefik. Domain routing is managed entirely through Docker labels in the compose file — no external configuration needed.

> "Expose the app service on app.mylab.local"

The agent calls `towline_domains_add` which:
1. Adds Traefik labels to the service in the compose file
2. Redeploys the stack

Labels added:
```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.myapp-dev-app.rule=Host(`app.mylab.local`)"
  - "traefik.http.routers.myapp-dev-app.entrypoints=websecure"
  - "traefik.http.routers.myapp-dev-app.tls.certresolver=letsencrypt"
  - "traefik.http.services.myapp-dev-app.loadbalancer.server.port=8080"
```

### Caddy (admin API)

If you use Caddy, start `towline-mcp` with the `-caddy-api` flag pointing to your Caddy admin endpoint:

```json
"args": ["-server", "...", "-stack", "...", "-tier", "dev", "-proxy", "caddy", "-caddy-api", "http://caddy:2019"]
```

Routes are managed via Caddy's admin API. TLS is handled automatically by Caddy.

Because a Caddy instance is usually shared between projects, routes are namespaced per stack with IDs of the form `towline:<stack>:<service>:<domain>`. A project only sees and removes its own routes, the service must exist in the stack's compose file, and a hostname already routed by any other route cannot be claimed. Routes created by older Towline versions (IDs `towline-<service>-<domain>`) are no longer listed or managed — remove them manually via Caddy's admin API.

### Cloudflare Tunnels

For exposing services to the internet through Cloudflare Tunnels:

> "Expose the app service on app.example.com via Cloudflare"

The agent specifies `method: "cloudflare"` in the `towline_domains_add` call. Towline returns structured instructions for the agent to execute via the Cloudflare MCP — it doesn't call Cloudflare APIs directly. This keeps Cloudflare credentials entirely separate from Towline.

**Prerequisite**: The [Cloudflare MCP server](https://github.com/cloudflare/mcp-server-cloudflare) must be configured in your agent's settings.

### Listing domains

> "What domains are configured?"

Calls `towline_domains_list` which scans the compose for Traefik labels, queries Caddy's admin API (this stack's routes only), or notes that Cloudflare tunnels are managed externally.

---

## Environment variables

### Reading variables

> "What environment variables are set?"

The agent calls `towline_env_get`. Sensitive values (variables with KEY, SECRET, PASSWORD, or TOKEN in their name) are automatically masked as `****`.

Internal Towline variables (`_TOWLINE_*`) are hidden from the output.

### Setting variables

> "Set the DATABASE_URL to postgres://app:secret@db:5432/myapp"

The agent calls `towline_env_set`. This updates the stack's environment variables in Portainer and records the change in deployment history (the variable name only, never the value). A service restart is usually needed to pick up the change — the agent knows this from the DevOps skill.

**Important**: Set environment variables **before** deploying services that need them. If a compose file references a variable that doesn't exist, the service starts with an empty value.

---

## Deployment history and rollbacks

### Viewing history

> "Show me the last 5 deployments"

The agent calls `towline_deployments` which returns a chronological list of stack changes — every `updateLocalStack` and `towline_env_set` — with:
- Timestamp
- Description (what changed)
- Diff (what lines were added/removed in the compose file)
- Outcome (success/failure)

Deployment history is stored in the `_TOWLINE_DEPLOYMENTS` stack environment variable, capped at the 50 most recent entries.

### Rolling back

> "Roll back to the deployment before the Redis addition"

The agent:
1. Reads deployment history to find the last known-good compose
2. Submits that compose via `updateLocalStack`
3. Verifies services are healthy

---

## Debugging and diagnostics

The DevOps skill teaches agents a systematic debugging approach. When something is broken, the agent follows this sequence:

### 1. Check health first

```
towline_service_health (all services)
```

Look for: containers not running, high restart counts, OOM kills (exit code 137), services stuck in "restarting" state.

### 2. Read logs

```
towline_service_logs (service: "failing-service", tail: 200)
```

Look for: stack traces, "connection refused" (dependency not ready), "permission denied" (volume issues), missing env var errors.

### 3. Check dependencies

Most "app broken" problems are actually "database not ready." The agent checks the health and logs of upstream dependencies.

### 4. Check environment

```
towline_env_get
```

Verify expected variables are set (sensitive values are masked but presence is confirmed).

### 5. Check compose structure

```
getLocalStackFile
```

Look for: wrong image tags, missing volume mounts, port conflicts.

### 6. Check what changed

```
towline_deployments
```

The diff between deployments often points directly at the problem.

### Exit code reference

| Code | Meaning | Action |
|------|---------|--------|
| 0 | Clean exit | Normal |
| 1 | Application error | Check logs |
| 126 | Permission denied on entrypoint | Check file permissions |
| 127 | Entrypoint binary not found | Check image/command |
| 137 | OOM killed | Increase memory limit |
| 143 | SIGTERM (graceful shutdown) | Usually normal |

---

## Production tier

### Setting up prod

Create a project with both tiers:

```bash
towline init my-app --with-prod
```

Or promote an existing dev project later (coming in Phase 3):

```bash
towline promote my-app  # not yet implemented
```

### Configuring the approval server

Prod-tier approval requires a **human** acting through an external approval server. `towline setup` asks for its URL (stored as `approval_webhook` in `~/.towline/config.yaml`), and `towline init --tier prod` / `--with-prod` passes it to the prod MCP server as `-approval-webhook <url>`. If it's missing, `towline init` warns you, and prod operations that need approval are **refused** (fail closed).

If the approval server requires authentication, set `TOWLINE_APPROVAL_WEBHOOK_TOKEN` in the MCP server's environment; it's sent as a bearer token.

The approval server must implement:

```
POST /       — receive approval request: {id, project, action, description, stackContent}
GET  /{id}   — return {"status": "pending" | "approved" | "rejected"}
```

How a human approves or rejects (e.g. `POST /{id}/approve`, `POST /{id}/reject`, a chat bot, a web page) is up to the approval server. It **must authenticate whoever approves or rejects**, and its approve/reject endpoints must not be reachable by the agent.

### How approval works

In prod tier, deploys, configuration changes, exec, and destructive operations require a human's approval. When the agent tries to perform a gated operation:

1. `towline-mcp` POSTs the request to the approval server and returns a message with an `approvalToken` (the request ID)
2. The agent tells you what it wants to do and waits
3. You approve (or reject) it in the approval server
4. The agent re-calls the tool with identical arguments plus the `approvalToken`
5. `towline-mcp` checks `GET /{id}`: if **approved**, the operation executes; if **pending**, nothing runs and the agent is told to keep waiting; if **rejected**, the call is refused

Saying "yes" in the agent chat does not approve anything — only the approval server's status counts, so the agent can't approve its own requests.

**What requires approval in prod:**

| Operation | Example |
|-----------|---------|
| Configuration changes | Setting env vars, adding/removing domains |
| Deployments | Creating or updating the stack compose |
| Destructive actions | Deleting or stopping the stack |
| Command execution | Running exec inside containers |
| Docker API writes | Allowed Docker proxy mutations other than start/stop/restart (e.g. kill, pause, rename, update, container delete) |

**What does NOT require approval:**

| Operation | Example |
|-----------|---------|
| Reading state | Health checks, log viewing, listing stacks |
| Operational | Starting a stack, scaling, container start/stop/restart via Docker proxy |

Approval tokens are single-use, bound to the tool name and exact arguments, and expire after 30 minutes. If you don't approve in time, the agent needs to request a new token.

---

## Templates and customization

### Bundled templates

**Simple compose templates:**

| Template | Stack |
|----------|-------|
| `default` | Empty (`services: {}`) |
| `web-app` | Nginx + PostgreSQL 16 + Redis 7 |
| `api` | Node.js API + PostgreSQL 16 |

**Template packs** (compose + skills + MCP configs):

| Pack | Stack | Includes |
|------|-------|----------|
| `ai-stack` | Ollama + Open WebUI + PostgreSQL | AI ops skill |
| `n8n` | n8n + PostgreSQL | n8n ops skill |

### Creating custom compose templates

For simple stacks that just need a compose file:

```bash
mkdir -p ~/.towline/templates/compose
```

Write a compose YAML:

```yaml
# ~/.towline/templates/compose/ml-pipeline.yml
services:
  worker:
    image: python:3.12-slim
    environment:
      - REDIS_URL=redis://redis:6379
    depends_on:
      - redis
      - minio
  redis:
    image: redis:7-alpine
  minio:
    image: minio/minio:latest
    command: server /data
    volumes:
      - minio_data:/data
volumes:
  minio_data:
```

Use it: `towline init my-ml-project --template ml-pipeline`

### Creating template packs

Template packs bundle a compose file, domain-specific skills, and additional MCP server configs into a single reusable unit.

Create a pack directory:

```bash
mkdir -p ~/.towline/packs/my-pack
```

Create `pack.yaml`:

```yaml
name: my-pack
description: My custom stack with extra tooling

# Compose file (relative to this directory)
compose: compose.yml

# Additional skill files to copy into projects
skills:
  - my-custom-ops.md

# Additional MCP servers to add to agent configs
mcps:
  my-tool:
    command: "npx"
    args: ["-y", "@my-org/my-mcp-server"]
    env:
      MY_API_KEY: "${MY_API_KEY}"
```

Add your compose file and skill files in the same directory:

```
~/.towline/packs/my-pack/
├── pack.yaml
├── compose.yml
└── my-custom-ops.md
```

Use it: `towline init my-project --template my-pack`

The pack's skills are copied alongside the base DevOps skill. The pack's MCP configs are merged into the generated MCP config files (`.mcp.json`, `.cursor/mcp.json`, `.gemini/settings.json`). Pack MCP servers start automatically with your privileges whenever the agent starts, so only use servers from verified publishers, pinned to an exact version.

### What goes in a skill file?

Skill files are markdown documents that teach agents how to operate a specific stack. Good skills include:

- **Key environment variables** the stack needs
- **Common tasks** with the exact tool calls to use
- **Troubleshooting** for known failure modes
- **Architecture notes** (which service talks to which, what ports)

See the bundled `skills/towline-devops.md` and the pack skills in `templates/packs/` for examples.

### Overriding agent instructions

To customize the AGENT.md template for all new projects:

```bash
cp templates/agent-md.tmpl ~/.towline/templates/agent-md.tmpl
# Edit to your preferences
```

Available template variables:

| Variable | Example |
|----------|---------|
| `{{.ProjectName}}` | `my-app` |
| `{{.StackName}}` | `my-app-dev` |
| `{{.Tier}}` | `dev` |
| `{{.PortainerURL}}` | `https://192.168.1.50:9443` |
| `{{.EnvironmentID}}` | `2` |
| `{{.APIToken}}` | `ptr_xxxx...` |
| `{{.MCPBinaryPath}}` | `towline-mcp` |
| `{{.SkipTLSVerify}}` | `false` |
| `{{.ApprovalWebhook}}` | `https://approvals.mylab.local` |

---

## Multiple agents and editors

Towline generates configuration for multiple agents simultaneously. Each project contains:

- `.mcp.json` — for Claude Code (plus `.claude/settings.json` for permissions)
- `.cursor/mcp.json` — for Cursor
- `.gemini/settings.json` — for Gemini CLI

All point to the same `towline-mcp` binary with the same stack and credentials. You can use any supported editor interchangeably. The API token is passed in each config's `env` block as `TOWLINE_PORTAINER_TOKEN` rather than on the command line, and the files are written with `0600` permissions (directories `0700`).

### Adding support for other agents

The generic configuration is in `towline.json` at the project root. It contains the MCP binary path and all required arguments. To configure a new MCP-compatible agent, point it at:

```
Command: towline-mcp
Args: -server <url> -stack <stack-name> -tier <tier> [-skip-tls-verify] [-approval-webhook <url>]
Env:  TOWLINE_PORTAINER_TOKEN=<token>
```

Add `-skip-tls-verify` only for a self-signed Portainer certificate, and `-approval-webhook` for prod tier. `towline-mcp` still accepts `-token` for backward compatibility, but that exposes the token in the process list. Read `towline.json` for the exact values for each project.

---

## CLI command reference

| Command | Description |
|---------|-------------|
| `towline setup` | Interactive first-run configuration. Connects to Portainer (TLS verified unless you choose self-signed) and asks for an optional approval webhook URL. |
| `towline init <name>` | Create a new project with Portainer provisioning. |
| `towline list` | List all Towline-managed projects. |
| `towline destroy <name>` | Delete Portainer resources for a project (IDs verified against Portainer first). |
| `towline promote <name>` | Create prod tier from dev. *(Coming soon)* |
| `towline rotate-keys <name>` | Rotate API keys. *(Coming soon)* |
| `towline status <name>` | Show live stack health. *(Coming soon)* |

### towline init flags

| Flag | Default | Description |
|------|---------|-------------|
| `--tier` | `dev` | Deployment tier: `dev` or `prod` |
| `--template` | `default` | Compose template or pack name |
| `--with-prod` | `false` | Also create a prod stack |

### towline destroy flags

| Flag | Default | Description |
|------|---------|-------------|
| `--confirm` | `false` | Skip interactive confirmation |

---

## MCP tool reference

### Observation tools (no side effects)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `towline_service_health` | `service` (optional) | Health: state, uptime, restarts, CPU/memory, exit code |
| `towline_service_logs` | `service` (required), `tail`, `since`, `filter` | Logs with Docker header stripping and line filtering |
| `towline_env_get` | `name` (optional) | Env vars with sensitive value masking |
| `towline_domains_list` | — | Domain mappings: service, domain, port, method |
| `towline_deployments` | `limit` (default 10) | Deployment history with diffs |

### Action tools (require prod approval, except `towline_scale`)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `towline_env_set` | `name`, `value` | Set env var (recorded in deployment history, name only). Restart may be needed. |
| `towline_domains_add` | `service`, `domain`, `port`, `method` | Add domain via Traefik/Caddy/Cloudflare |
| `towline_domains_remove` | `service`, `domain` | Remove domain routing |
| `towline_scale` | `service`, `replicas` | Scale service replicas (operational: no approval) |
| `towline_exec` | `service`, `command` | Run command in container |

### Upstream Portainer tools (scoped to stack)

| Tool | Description |
|------|-------------|
| `listLocalStacks` | List stacks (filtered to project) |
| `getLocalStackFile` | Get current compose YAML |
| `createLocalStack` | Create new stack (compose security policy applies; prod: approval required) |
| `updateLocalStack` | Update compose (submit COMPLETE file; omitting `env` keeps current vars; prod: approval required) |
| `startLocalStack` | Start the stack |
| `stopLocalStack` | Stop the stack (prod: approval required) |
| `deleteLocalStack` | Delete the stack (prod: approval required) |
| `dockerProxy` | Raw Docker API access (scoped to stack containers; see below) |

### dockerProxy limits

In scoped mode, `dockerAPIPath` must be a plain path: query strings, fragments, `%`-encoding, backslashes, and `.`/`..` or empty segments are rejected (pass query parameters via `queryParams`). API version prefixes like `/v1.43/` are normalized before scoping.

The only mutating requests allowed are `POST /containers/{id}/{start|stop|restart|kill|pause|unpause|wait|resize|rename|update}` and `DELETE /containers/{id}`, on the stack's own containers. Container create, exec, archive upload, and anything on networks, volumes, images, system, etc. are rejected. In prod, only start/stop/restart are operational; the other allowed mutations need approval.

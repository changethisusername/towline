# Towline

**Give AI coding agents safe, project-scoped access to deploy and manage containers on your Portainer infrastructure.**

<!-- Badges: uncomment and update when applicable
[![Build](https://img.shields.io/github/actions/workflow/status/OWNER/towline/ci.yml?branch=main)](https://github.com/OWNER/towline/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/OWNER/towline)](https://goreportcard.com/report/github.com/OWNER/towline)
[![License](https://img.shields.io/github/license/OWNER/towline)](LICENSE)
-->

Towline is an open-source framework that sits between AI agents and [Portainer](https://www.portainer.io/), providing isolated deploy environments with layered permissions and operational tooling. One command creates a fully provisioned project where an agent can deploy, scale, debug, and manage containers without touching anything outside its scope.

[Website](https://towline.dev) | [Getting Started](docs/getting-started.md) | [User Guide](docs/user-guide.md)

---

## What Towline does

```
                         ┌─────────────────────────────────────────┐
                         │              Portainer CE               │
                         │  ┌──────────┐  ┌──────────┐            │
                         │  │ project-a │  │ project-b │  ...      │
                         │  │   stack   │  │   stack   │           │
                         │  └──────────┘  └──────────┘            │
                         └──────────┬──────────┬──────────────────┘
                                    │          │
                         ┌──────────┴──┐  ┌────┴───────────┐
                         │ towline-mcp │  │  towline-mcp   │
                         │ -stack a    │  │  -stack b      │
                         │ -tier dev   │  │  -tier prod    │
                         └──────┬──────┘  └──────┬─────────┘
                                │                │
                         ┌──────┴──────┐  ┌──────┴─────────┐
                         │  Agent A    │  │    Agent B      │
                         │ (Claude,    │  │   (Cursor,      │
                         │  Gemini..)  │  │    Codex..)     │
                         └─────────────┘  └────────────────┘

  $ towline init my-project
    → Creates Portainer team + API key + stack
    → Generates agent configs (.claude/, .cursor/, .gemini/)
    → Copies DevOps skill into project
    → git init + initial commit
    → Agent starts with full isolated deploy access
```

Each agent gets a dedicated MCP server process that can only see and modify its own stack. Three independent permission layers ensure isolation even if one layer fails.

---

## Quick start

### 1. Install

```bash
# One-line installer (macOS, Linux, WSL)
curl -fsSL https://towline.dev/install | sh
```

Or build from source:

```bash
git clone https://github.com/changethisusername/towline.git
cd towline
make build
sudo cp dist/towline dist/towline-mcp /usr/local/bin/
```

### 2. Connect to Portainer

```bash
towline setup
```

Interactive wizard that authenticates with your Portainer instance, generates an admin API key, and writes `~/.towline/config.yaml`.

### 3. Create a project

```bash
towline init receipt-scanner
```

In under 60 seconds this provisions a Portainer team, scoped API key, stack, and generates all agent configuration files.

### 4. Open in your editor

```bash
cd ~/projects/receipt-scanner
claude   # or cursor, gemini, windsurf, codex...
```

The agent automatically connects to the Towline MCP server and can deploy containers, read logs, manage domains, and more — scoped exclusively to this project's stack.

### 5. Deploy something

Ask your agent:

> "Add a Postgres database and a Redis cache to the stack, then verify both are healthy."

The agent uses the MCP tools to modify the compose file, deploy, and check health — all within its isolated scope.

---

## How it works

### Three components

| Component | What it is | Role |
|-----------|-----------|------|
| **towline-mcp** | Go binary (forked from [Portainer MCP](https://github.com/portainer/portainer-mcp) v0.7.0) | MCP server with stack scoping, tier-based approval, and 10 high-level operational tools |
| **towline CLI** | Go binary | Project scaffolding — provisions Portainer teams, API keys, stacks, and multi-agent config |
| **DevOps skill** | Markdown file | Teaches agents operational decision-making, tool usage patterns, and debugging workflows |

### Three-layer permissions model

```
Layer 1: Portainer team-scoped API keys ─── Hard security boundary
         Each project gets a dedicated team. Even if all other layers
         fail, Portainer rejects cross-project requests.

Layer 2: MCP stack-name filtering ───────── Agent experience layer
         Filters Docker proxy responses by compose project label.
         Keeps agent context clean. Specific error messages for
         out-of-scope requests.

Layer 3: Tier-based approval gating ─────── Deployment control layer
         Dev tier: full autonomy.
         Prod tier: destructive/config ops require in-chat approval.
```

### Middleware chain

Every tool call passes through three middleware layers before reaching the handler:

```
Tool call → Tier gating → Stack scoping → Container ownership → Handler
```

### Prod tier approval flow

When a gated operation is called in prod tier:

1. Middleware generates a random approval token (5-minute TTL)
2. Returns the token with operation details to the agent
3. Agent presents the details to the user for confirmation
4. User approves, agent re-calls the tool with the `approvalToken` parameter
5. Middleware validates and forwards to the real handler

| Operation type | Approval required in prod |
|---|---|
| Read (list, logs, health, inspect, env_get, deployments) | No |
| Operational (restart, start, stop, scale) | No |
| Configuration (env_set, domains_add, domains_remove) | Yes |
| Deploy (create/update stack) | Yes |
| Destructive (delete/stop stack) | Yes |
| Exec, Docker proxy non-GET | Yes |

---

## CLI reference

### `towline setup`

Interactive first-run configuration.

```bash
towline setup
```

Prompts for Portainer URL, admin credentials, environment selection, and projects directory. Writes `~/.towline/config.yaml` with `0600` permissions.

### `towline init <project-name>`

Create a new project with full Portainer provisioning.

```bash
towline init my-app
towline init my-app --tier prod
towline init my-app --template web-app
towline init my-app --with-prod
```

| Flag | Default | Description |
|------|---------|-------------|
| `--tier` | `dev` | Deployment tier (`dev` or `prod`) |
| `--template` | `default` | Compose template name (matches files in `templates/compose/`) |
| `--with-prod` | `false` | Also create a prod stack with separate team and API key |

### `towline list`

List all towline projects.

```bash
towline list
```

```
PROJECT              STACK                     TIER   STATUS
-----------------------------------------------------------------
receipt-scanner      receipt-scanner-dev       dev    configured
blog                 blog-dev                  dev    configured
```

### `towline destroy <project-name>`

Destroy Portainer resources for a project.

```bash
towline destroy my-app
towline destroy my-app --confirm    # Skip interactive confirmation
```

Deletes the Portainer stack and team. Local files are preserved and must be removed manually.

| Flag | Default | Description |
|------|---------|-------------|
| `--confirm` | `false` | Skip interactive confirmation prompt |

### Coming in Phase 3

| Command | Description |
|---------|-------------|
| `towline promote <project>` | Create a prod stack from the current dev compose, add prod MCP config |
| `towline rotate-keys <project>` | Generate new API token, revoke old one, update config files |
| `towline status <project>` | Show live container health from Portainer (CLI equivalent of `towline_service_health`) |

---

## MCP tools reference

All towline tools accept compose **service names** — no container IDs needed. Tools are prefixed with `towline_` to avoid collisions with upstream Portainer MCP tools.

### Observation tools (no side effects)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `towline_service_health` | `service` (optional) | Health snapshot: state, uptime, restart count, CPU/memory, exit code. Returns all services if no service specified. |
| `towline_service_logs` | `service` (required), `tail` (int, default 200), `since` (RFC3339, optional), `filter` (string, optional) | Recent log output with Docker binary framing stripped. `filter` applies grep-like line matching. |
| `towline_env_get` | `name` (optional) | Read stack environment variables. Values containing KEY, SECRET, PASSWORD, or TOKEN in their name are masked. |
| `towline_domains_list` | — | List all domain/routing mappings: service, domain, port, proxy method. |
| `towline_deployments` | `limit` (int, default 10) | Deployment history with diffs and outcomes. |

### Action tools (cause mutations)

| Tool | Parameters | Prod approval | Description |
|------|-----------|:---:|-------------|
| `towline_env_set` | `name`, `value` | Yes | Update an environment variable. Service restart may be needed. |
| `towline_domains_add` | `service`, `domain`, `port` (default 80), `method` (optional) | Yes | Add domain routing via Traefik labels, Caddy API, or Cloudflare Tunnel delegation. |
| `towline_domains_remove` | `service`, `domain` | Yes | Remove a domain routing. |
| `towline_scale` | `service`, `replicas` | Yes | Scale a service. Warns if the service has persistent volumes. |
| `towline_exec` | `service`, `command` | Yes | Run a command inside a running container. |

### Inherited upstream tools (scoped to project stack)

| Tool | Description |
|------|-------------|
| `listLocalStacks` | List stacks (filtered to project stack) |
| `getLocalStackFile` | Get current docker-compose.yml content |
| `createLocalStack` | Create a new stack |
| `updateLocalStack` | Update stack compose definition (always submit COMPLETE file) |
| `startLocalStack` / `stopLocalStack` | Start or stop the stack |
| `deleteLocalStack` | Delete the stack |
| `dockerProxy` | Low-level Docker API access (scoped to stack containers) |

---

## Proxy backends

Towline supports three proxy backends for domain management. The backend is auto-detected from compose labels or set explicitly via the `-proxy` flag.

### Traefik

Compose label manipulation — no external API calls. `towline_domains_add` injects Traefik labels into the compose file and redeploys:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.mystack-app.rule=Host(`app.example.com`)"
  - "traefik.http.routers.mystack-app.entrypoints=websecure"
  - "traefik.http.routers.mystack-app.tls.certresolver=letsencrypt"
  - "traefik.http.services.mystack-app.loadbalancer.server.port=8080"
```

### Caddy

Uses the Caddy admin API (default `http://localhost:2019`, configurable via `-caddy-api`). Routes are added and removed via REST calls. Caddy handles TLS automatically.

### Cloudflare Tunnels

Towline does not call Cloudflare APIs directly. `towline_domains_add` with `method: "cloudflare"` returns structured instructions for the agent to execute via the [Cloudflare MCP](https://github.com/cloudflare/mcp-server-cloudflare), keeping the Cloudflare credential surface entirely separate.

---

## Configuration

### Global config (`~/.towline/config.yaml`)

Created by `towline setup`. Stored with `0600` permissions.

```yaml
portainer_url: "https://192.168.1.50:9443"
portainer_admin_key: "ptr_xxxx..."
portainer_env_id: 2
projects_dir: "~/projects"
```

### Project config (`towline.json`)

Created by `towline init` in each project directory. Contains stack metadata and MCP connection details.

```json
{
  "stack_name": "receipt-scanner-dev",
  "stack_id": 42,
  "tier": "dev",
  "team_id": 7,
  "user_id": 12,
  "env_id": 2,
  "mcp_binary": "/usr/local/bin/towline-mcp",
  "mcp_args": {
    "server": "https://192.168.1.50:9443",
    "token": "ptr_xxxx...",
    "stack": "receipt-scanner-dev",
    "tier": "dev"
  }
}
```

### Templates

Templates are embedded in the Go binary and can be overridden by placing files in `~/.towline/templates/`.

**Bundled compose templates:**

**Simple compose templates:**

| Template | Stack |
|----------|-------|
| `default` | Empty starter (`services: {}`) |
| `web-app` | Nginx + PostgreSQL 16 + Redis 7 |
| `api` | Node.js API + PostgreSQL 16 |

**Template packs** (compose + skills + MCP configs):

| Pack | Stack | Extras |
|------|-------|--------|
| `ai-stack` | Ollama + Open WebUI + PostgreSQL | AI ops skill, Ollama MCP |
| `n8n` | n8n + PostgreSQL | n8n ops skill |

Use packs with `towline init my-project --template ai-stack`. Packs include domain-specific skills that teach agents how to operate the stack, and can add additional MCP servers to agent configs.

**Template variables** (Go `text/template` syntax):

| Variable | Example |
|---|---|
| `{{.ProjectName}}` | `receipt-scanner` |
| `{{.StackName}}` | `receipt-scanner-dev` |
| `{{.Tier}}` | `dev` |
| `{{.PortainerURL}}` | `https://192.168.1.50:9443` |
| `{{.EnvironmentID}}` | `2` |
| `{{.APIToken}}` | `ptr_xxxx...` |
| `{{.MCPBinaryPath}}` | `/usr/local/bin/towline-mcp` |

---

## Generated project structure

```
~/projects/receipt-scanner/
├── AGENT.md                    # Project-specific agent instructions
├── towline.json                # Project config (stack, team, MCP args)
├── docker-compose.yml          # From selected template
├── .env.example                # Placeholder environment variables
├── .gitignore                  # Excludes settings files, .env, *.key
├── skills/
│   └── towline-devops.md       # Operational decision-making guide for agents
├── .claude/settings.json       # Claude Code MCP configuration
├── .cursor/mcp.json            # Cursor MCP configuration
├── .gemini/settings.json       # Gemini CLI MCP configuration
└── .git/
```

All agent config files (`.claude/`, `.cursor/`, `.gemini/`) point the MCP client at the `towline-mcp` binary with the correct flags for this project's stack and tier.

---

## Development

### Prerequisites

- **Portainer CE 2.28+** with API access
- **Docker** on the container host
- **Go 1.24+** for building from source
- **macOS, Linux, or WSL** on the development machine

### Building from source

```bash
git clone https://github.com/OWNER/towline.git
cd towline
make build          # Build both binaries to dist/
```

Individual targets:

```bash
make build-mcp      # Build only towline-mcp
make build-cli      # Build only towline CLI
make test           # Run unit tests
make test-coverage  # Tests with coverage report
make vet            # Go vet
make fmt            # gofmt
make clean          # Remove dist/
```

Cross-compile with `PLATFORM` and `ARCH`:

```bash
PLATFORM=linux ARCH=amd64 make build
PLATFORM=linux ARCH=arm64 make build
PLATFORM=darwin ARCH=arm64 make build
```

Version, commit hash, and build date are injected via ldflags.

### towline-mcp flags

| Flag | Required | Default | Description |
|------|:---:|---------|-------------|
| `-server` | Yes | — | Portainer server URL |
| `-token` | Yes | — | Portainer API token |
| `-stack` | Yes | — | Stack name to scope this MCP instance to |
| `-tier` | Yes | — | Deployment tier: `dev` or `prod` |
| `-tools` | No | `tools.yaml` | Path to upstream tools YAML file |
| `-read-only` | No | `false` | Run in read-only mode (disable all mutations) |
| `-disable-version-check` | No | `true` | Disable Portainer version check |
| `-proxy` | No | auto-detect | Proxy backend: `traefik`, `caddy`, or `cloudflare` |
| `-caddy-api` | No | `http://localhost:2019` | Caddy admin API URL |

### Project structure

```
towline/
├── cmd/
│   ├── towline-mcp/              # MCP server entry point
│   └── towline/                  # CLI entry point
├── internal/
│   ├── mcp/                      # Upstream Portainer MCP handlers (forked)
│   ├── middleware/                # Stack scoping, tier gating, ownership assertion
│   ├── towline/                  # Towline tool handlers (health, logs, env, etc.)
│   ├── approval/                 # In-chat approval token store
│   ├── proxy/                    # Traefik, Caddy, Cloudflare proxy managers
│   ├── tooldef/                  # Tool YAML definitions (upstream + towline)
│   └── cli/                      # CLI command implementations
├── pkg/
│   ├── portainer/                # Upstream Portainer client, models, utils
│   ├── toolgen/                  # YAML tool loader + parameter parser
│   └── config/                   # Global and project config types
├── skills/
│   └── towline-devops.md         # DevOps skill (embedded into projects)
├── templates/                    # Bundled templates (Go embed.FS)
│   ├── agent-md.tmpl
│   ├── claude-settings.tmpl
│   ├── cursor-mcp.tmpl
│   ├── gemini-settings.tmpl
│   ├── gitignore.tmpl
│   └── compose/
│       ├── default.yml
│       └── web-app.yml
├── Makefile
├── go.mod
└── go.sum
```

### Upstream tracking

Upstream Portainer MCP code lives in `internal/mcp/`, `internal/tooldef/`, `pkg/portainer/`, and `pkg/toolgen/`. Only two upstream files are modified (`cmd/towline-mcp/main.go` and `internal/mcp/server.go`). All towline additions are in new files and packages, keeping the fork cleanly mergeable.

### Running tests

```bash
make test
```

The test suite covers unit tests, integration tests (handler + middleware chain), and end-to-end regression tests.

---

## Roadmap

### Phase 3 — CLI extended

- `towline promote` — create prod stack from dev, add prod MCP entries
- `towline rotate-keys` — generate new API token, revoke old, update configs
- `towline status` — live container health from the CLI
- Approval server integrations (Telegram, ntfy.sh)

### Phase 4 — Community

- Documentation site ([towline.dev](https://towline.dev))
- Encrypted keystore for API tokens
- Upstream Portainer MCP merge workflow
- Plugin system for custom tools
- Community compose templates

---

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

**Design constraints to keep in mind:**

- **Upstream mergeability** — the MCP fork must stay cleanly mergeable with Portainer's official MCP releases. All additions go in new files; no upstream handler modifications.
- **Agent-agnostic** — works with any MCP-compatible tool (Claude Code, Cursor, Codex, Gemini CLI, Windsurf).
- **No cloud dependencies** — fully self-hostable. Runs wherever Portainer runs.
- **Tool design principle** — every tool answers a developer question in one call. Agents should never need raw Docker API calls for common operations.

---

## License

Apache License 2.0. See [LICENSE](LICENSE).

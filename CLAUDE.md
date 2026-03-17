# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

Towline is the agentic engineering SDK for Portainer. One command (`towline init`) gives an AI agent a complete environment to build, deploy, and operate software on Portainer-managed infrastructure — isolated container stack, operational MCP tools, domain-specific skills, and multi-agent configuration.

Three components:

1. **towline-mcp** — Go binary (forked Portainer MCP v0.7.0). MCP server with stack scoping middleware, tier-based approval gating, and 10 high-level operational tools
2. **towline CLI** — Go binary. Project scaffolding that provisions Portainer teams, API keys, stacks, and agent configuration
3. **Template packs** — Compose files + agent skills + MCP configs bundled as reusable starter kits

The core principle: a new project should go from idea to "agent building and shipping software in an isolated environment" in under 60 seconds.

## Build & Test

```bash
make build          # Build both binaries to dist/
make build-mcp      # Build only towline-mcp
make build-cli      # Build only towline CLI
make test           # Run unit tests
make test-coverage  # Tests with coverage report
make vet            # Go vet
make fmt            # gofmt
```

## Code style

- Follow upstream Portainer MCP conventions: PascalCase exported, camelCase unexported
- Error handling: `fmt.Errorf("failed to X: %w", err)`
- Table-driven tests with descriptive case names
- Functional options pattern for configurable types

## Architecture

### Two-component design

**towline-mcp** (Go binary, forked from github.com/portainer/portainer-mcp):
- Runs per-project as an MCP server process spawned by the AI agent
- Adds four middleware layers on top of upstream Portainer MCP handlers:
  - Stack-name scoping (`-stack` flag) — filters all API responses to a single stack
  - Tier-based access control (`-tier dev|prod`) — gates destructive prod operations
  - Approval webhook integration (`-approval-webhook` URL) — external approval for prod changes
  - Container ownership assertion — validates compose project labels before Docker proxy calls
- Fork additions are confined to new files (middleware package, config extensions) and modified entry point — no upstream handler changes
- Exposes high-level tools (`towline_service_health`, `towline_service_logs`, `towline_env_get/set`, `towline_domains_*`, `towline_scale`, `towline_deployments`, `towline_exec`) on top of upstream's stack CRUD and Docker proxy

**towline CLI** (scaffolding tool):
- `towline init <project>` creates: Portainer team + scoped API key + stack + `.claude/settings.json` + CLAUDE.md + compose files + git init
- Global config lives in `~/.towline/config.yaml` (portainer_url, admin key, env ID, projects dir, default MCPs, skills)
- Templates in `~/.towline/templates/` are interpolated with `{{PROJECT_NAME}}`, `{{STACK_NAME}}`, `{{TIER}}`

### Three-layer permissions model

1. **Portainer team-scoped API keys** — the hard security boundary. Each project gets a dedicated team. Even if all other layers fail, Portainer rejects cross-project requests.
2. **MCP stack-name filtering** — agent experience layer. Filters Docker proxy responses by `com.docker.compose.project` label. Keeps agent context clean and provides specific error messages for out-of-scope requests.
3. **Tier-based approval gating** — deployment control layer. Dev tier: full autonomy. Prod tier: read/operational ops are immediate; configuration/deploy/destructive/exec operations require webhook approval.

### Approval webhook contract

```
POST /           — receive approval request (id, project, action, description, stackContent)
GET  /{id}       — poll status (pending | approved | rejected)
POST /{id}/approve — approve
POST /{id}/reject  — reject
```

### MCP tool categories

| Category | Tools | Side effects |
|----------|-------|--------------|
| Observation | `towline_service_health`, `towline_service_logs`, `towline_env_get`, `towline_domains_list`, `towline_deployments`, `portainer_list_containers`, `portainer_inspect_container`, `portainer_stack_status`, `portainer_stack_file` | None |
| Action | `portainer_deploy_stack`, `towline_env_set`, `towline_domains_add/remove`, `towline_scale`, `portainer_restart/start/stop_container`, `towline_exec`, `portainer_delete_stack` | Mutations |

### Prod tier permission matrix

| Operation type | Approval required |
|---|---|
| Read (list, logs, health, inspect, env_get, deployments) | No |
| Operational (restart, start, stop, scale) | No |
| Configuration (env_set, domains_add/remove) | Yes |
| Deploy (create/update stack) | Yes |
| Destructive (delete/stop stack) | Yes |
| Exec, Docker proxy non-GET | Yes |

## Implementation phases

The project follows this roadmap:

1. **Phase 1: Portainer MCP fork** — towline-mcp binary with stack scoping, tier control, approval webhook, container ownership assertion, cross-platform builds (Linux amd64/arm64, macOS arm64)
2. **Phase 2: CLI core** — `towline init`, template system, `towline list`, `towline destroy`
3. **Phase 3: CLI extended** — `towline promote`, `towline rotate-keys`, `towline status`, approval server integrations (Telegram, ntfy.sh)
4. **Phase 4: Community** — docs site (towline.dev), encrypted keystore, upstream merge workflow, integration tests, plugin system, community templates

## Key design constraints

- **Upstream mergeability**: the MCP fork must stay cleanly mergeable with Portainer's official MCP releases. All additions go in new files; no upstream handler modifications.
- **Agent-agnostic**: works with any MCP-compatible tool (Claude Code, Cursor, Codex, Gemini CLI, Windsurf).
- **No cloud dependencies**: fully self-hostable. Runs wherever Portainer runs.
- **Tool design principle**: every tool answers a developer question in one call. Agents should never need raw Docker API calls for common operations.
- **Partial compose files are dangerous**: when updating a stack via `portainer_deploy_stack`, the COMPLETE compose file must be submitted. Portainer removes any service not included.

## Prerequisites

- Portainer v2.28+ with API access
- Docker on the container host
- macOS, Linux, or WSL on the development machine
- Network connectivity from dev machine to Portainer API

## Generated project structure

```
~/projects/<name>/
├── .claude/settings.json   # Scoped Portainer MCP configuration
├── CLAUDE.md               # Project-specific agent instructions
├── docker-compose.yml      # From template
├── .env.example            # Env var template
├── skills/ →               # Symlink to shared skills library
└── .git/
```

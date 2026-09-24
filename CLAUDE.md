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
- Adds middleware layers on top of upstream Portainer MCP handlers (`internal/middleware`, chained in `cmd/towline-mcp/main.go`, outermost first):
  - Compose security policy (`-compose-policy enforce|off`, `-allow-bind-mounts`) — rejects privileged mode, host namespaces, host bind mounts, devices, added capabilities, and host-file `secrets`/`configs`/`env_file` in create/update stack calls (dev and prod)
  - Tier-based access control (`-tier dev|prod`) with approval modes (`-approval-mode human|agent`): `human` sends prod changes to an approval server (`-approval-webhook` URL) and refuses them if none is configured; `agent` lets the agent confirm its own operations by re-calling with the token. Unset keeps the pre-approval-server behavior (human if a webhook is set, else agent) so existing configs keep working
  - Stack-name scoping (`-stack` flag) — filters all API responses to a single stack
  - Env filter — agents cannot set `_TOWLINE_*` vars; updates carry the stack's internal vars over and record deployment history
  - Container ownership assertion — normalizes Docker API paths (version prefix, rejects `?`/`%`/`..`), allows only lifecycle mutations on the stack's own containers, and validates compose project labels before Docker proxy calls
- The Portainer token is read from `TOWLINE_PORTAINER_TOKEN` (preferred; keeps it out of `ps`) or `-token`
- Fork additions are confined to new files (middleware package, config extensions) and modified entry point — no upstream handler changes
- Exposes high-level tools (`towline_service_health`, `towline_service_logs`, `towline_env_get/set`, `towline_domains_*`, `towline_scale`, `towline_deployments`, `towline_exec`) on top of upstream's stack CRUD and Docker proxy

**Approval server** (`towline approvals serve`, `internal/approval/server.go`): ships with the CLI. Implements the webhook contract for towline-mcp, plus a web UI (`/ui`) and approver API for humans, authenticated by an approver token whose SHA-256 is stored in `~/.towline/config.yaml`. The webhook token given to towline-mcp can submit and poll, never decide. Optional ntfy notifications.

**towline CLI** (scaffolding tool):
- `towline init <project>` creates: Portainer team + scoped API key + stack + `.claude/settings.json` + CLAUDE.md + compose files + git init
- Global config lives in `~/.towline/config.yaml` (portainer_url, admin key, env ID, projects dir, `skip_tls_verify`, `approval:` block)
- `towline refresh [--all|name...]` upgrades existing projects in place: merges the towline entry into MCP configs (keeping other servers and user flags), fixes permissions/.gitignore, moves teams to the Standard user role, and grandfathers deployed bind mounts into `towline.json` `compose_policy`
- `towline approvals setup|serve|list|approve|reject` configures and runs prod approvals
- Templates in `~/.towline/templates/` are interpolated with `{{PROJECT_NAME}}`, `{{STACK_NAME}}`, `{{TIER}}`

### Three-layer permissions model

1. **Portainer team-scoped API keys** — the hard security boundary. Each project gets a dedicated team with the "Standard user" environment role (RoleId 4, never Environment administrator), so Portainer rejects cross-project requests even if all other layers fail. The agent holds this key and can call Portainer directly, so the MCP layers below cannot be the only line of defense.
2. **MCP stack-name filtering** — agent experience layer. Filters Docker proxy responses by `com.docker.compose.project` label. Keeps agent context clean and provides specific error messages for out-of-scope requests.
3. **Tier-based approval gating** — deployment control layer. Dev tier: full autonomy. Prod tier: read/operational ops are immediate; configuration/deploy/destructive/exec operations require webhook approval. Holding an approval token never authorizes anything by itself; only the approval server's decision does.

### Approval webhook contract

```
POST /           — receive approval request (id, project, action, description, stackContent)
GET  /{id}       — poll status: {"status": "pending" | "approved" | "rejected"}
POST /{id}/approve — approve (human only)
POST /{id}/reject  — reject (human only)
```

towline-mcp only calls `POST /` and `GET /{id}` (with `Authorization: Bearer $TOWLINE_APPROVAL_WEBHOOK_TOKEN` if set). The first call to a gated tool submits the request and returns `approvalToken` (the request id); the agent re-calls with identical arguments plus `approvalToken`, and the call proceeds only once `GET /{id}` returns `approved`. Tokens are single-use, bound to tool + arguments, and expire after 30 minutes. The approval server must authenticate approve/reject; the agent must not be able to reach those endpoints.

### MCP tool categories

| Category | Tools | Side effects |
|----------|-------|--------------|
| Observation | `towline_service_health`, `towline_service_logs`, `towline_env_get`, `towline_domains_list`, `towline_deployments`, `listLocalStacks`, `getLocalStackFile`, `dockerProxy` (GET) | None |
| Action | `createLocalStack`, `updateLocalStack`, `towline_env_set`, `towline_domains_add/remove`, `towline_scale`, `startLocalStack`, `stopLocalStack`, `dockerProxy` (non-GET), `towline_exec`, `deleteLocalStack` | Mutations |

### Prod tier permission matrix

| Operation type | Approval required |
|---|---|
| Read (list, logs, health, inspect, env_get, deployments) | No |
| Operational (restart, start, stop, scale) | No |
| Configuration (env_set, domains_add/remove) | Yes |
| Deploy (create/update stack) | Yes |
| Destructive (delete/stop stack) | Yes |
| Exec, Docker proxy non-GET (except container start/stop/restart) | Yes |

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
- **Partial compose files are dangerous**: when updating a stack via `updateLocalStack`, the COMPLETE compose file must be submitted. Portainer removes any service not included.
- **Untrusted agent input**: treat every tool argument as attacker-controlled. Validate paths, names, and compose content before they reach Portainer, Docker, or a proxy backend, and fail closed.
- **Secrets on disk**: generated files containing the project token (`.mcp.json`, `.claude/`, `.cursor/`, `.gemini/`, `towline.json`) are written 0600 and gitignored. The CLI verifies TLS unless `skip_tls_verify` was chosen at setup.
- **`towline.json` is agent-writable**: never trust its IDs with the admin key without re-verifying names against Portainer (see `towline destroy`).
- **Never break existing installs**: new behavior must default to the previous behavior for configs that predate it (unset `skip_tls_verify` keeps skipping; unset approval mode keeps agent confirmation), and `towline refresh` is how projects opt in. Tool definition files carry a version and are auto-upgraded by towline-mcp; bump `version` in `internal/tooldef/*.yaml` when changing them.

## Prerequisites

- Portainer v2.28+ with API access
- Docker on the container host
- macOS, Linux, or WSL on the development machine
- Network connectivity from dev machine to Portainer API

## Generated project structure

```
~/projects/<name>/
├── .mcp.json               # MCP server config (token in env, 0600, gitignored)
├── .claude/settings.json   # Tool permissions for the scoped MCP server
├── CLAUDE.md               # Project-specific agent instructions
├── docker-compose.yml      # From template
├── .env.example            # Env var template
├── skills/ →               # Symlink to shared skills library
└── .git/
```

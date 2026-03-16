# Towline — Design Specification

Permission-aware deployment for AI agents on homelab infrastructure.

## Overview

Towline gives AI coding agents safe, project-scoped access to deploy and manage containers on self-hosted Portainer infrastructure. Three components:

1. **towline-mcp** — Go binary. Forked from Portainer MCP v0.7.0. Adds stack scoping, tier-based access control, and high-level operational tools.
2. **towline CLI** — Go binary. Scaffolds projects with Portainer teams, API keys, stacks, and agent configuration.
3. **DevOps skill** — Markdown file. Teaches agents how to think about operations and use the MCP effectively.

**Target compatibility**: Portainer CE 2.39.0 LTS, Go module `github.com/changethisusername/towline`. The upstream Portainer MCP v0.7.0 targets Portainer 2.31.2 (EE) with `client-api-go/v2 v2.31.2`. Towline updates the `SupportedPortainerVersion` constant and client SDK to target CE 2.39.0 LTS. The local stack and Docker proxy APIs are identical between CE and EE. The `-disable-version-check` flag defaults to `true` until the client SDK is verified against 2.39.0.

---

## 1. Repository structure

Single Go module producing two binaries:

```
towline/
├── cmd/
│   ├── towline-mcp/              # MCP server binary
│   │   └── main.go
│   └── towline/                   # CLI binary
│       └── main.go
├── internal/
│   ├── mcp/                       # Upstream Portainer MCP handlers (forked)
│   ├── middleware/                 # Stack scoping, tier gating, ownership assertion
│   │   ├── scoping.go
│   │   ├── tiergating.go
│   │   └── ownership.go
│   ├── towline/                   # Towline-specific tool handlers
│   │   ├── health.go
│   │   ├── logs.go
│   │   ├── env.go
│   │   ├── domains.go
│   │   ├── scale.go
│   │   ├── deployments.go
│   │   └── exec.go
│   ├── approval/                  # In-chat approval flow
│   │   └── approval.go
│   ├── proxy/                     # Reverse proxy management
│   │   ├── traefik.go
│   │   ├── caddy.go
│   │   └── cloudflare.go
│   ├── tooldef/                   # Upstream + towline tool definitions
│   └── cli/                       # CLI command implementations
│       ├── setup.go
│       ├── init.go
│       ├── list.go
│       ├── destroy.go
│       ├── promote.go
│       ├── rotate.go
│       └── status.go
├── pkg/
│   ├── portainer/                 # Upstream Portainer client, models, utils
│   ├── toolgen/                   # Upstream YAML tool loader + parameter parser
│   └── config/                    # Towline global/project config types
├── skills/
│   └── towline-devops.md          # DevOps skill (embedded + copied into projects)
├── templates/                     # Bundled templates (embedded via embed.FS)
│   ├── agent-md.tmpl
│   ├── claude-settings.tmpl
│   ├── cursor-mcp.tmpl
│   ├── gemini-settings.tmpl
│   ├── towline-json.tmpl
│   ├── gitignore.tmpl
│   └── compose/
│       ├── default.yml
│       └── web-app.yml
├── Makefile
├── go.mod
└── go.sum
```

### Build system

`make build` produces `dist/towline-mcp` and `dist/towline`. Cross-platform: linux/amd64, linux/arm64, darwin/arm64. Version, commit, and build date injected via ldflags.

```makefile
LDFLAGS = -s -w \
  -X main.Version=$(VERSION) \
  -X main.Commit=$(COMMIT) \
  -X main.BuildDate=$(BUILD_DATE)

build: build-mcp build-cli

build-mcp:
	CGO_ENABLED=0 go build --ldflags '$(LDFLAGS)' -o dist/towline-mcp ./cmd/towline-mcp

build-cli:
	CGO_ENABLED=0 go build --ldflags '$(LDFLAGS)' -o dist/towline ./cmd/towline
```

### Upstream tracking

Upstream Portainer MCP code lives in `internal/mcp/`, `internal/tooldef/`, `pkg/portainer/`, and `pkg/toolgen/`. An `upstream` branch tracks the unmodified Portainer MCP for clean merges.

**Upstream files modified** (minimal, merge-friendly):
- `cmd/towline-mcp/main.go` — renamed from `cmd/portainer-mcp/mcp.go`. Adds new CLI flags and wires middleware.
- `internal/mcp/server.go` — adds an exported `WrapTool` method and exports the `tools` map via a `Tools()` accessor, enabling middleware to wrap handlers at registration time without modifying handler code. The upstream `PortainerMCPServer` struct fields remain unexported; the new methods provide controlled access.

All other upstream files are unmodified. New towline code lives entirely in new files/packages (`internal/middleware/`, `internal/towline/`, `internal/approval/`, `internal/proxy/`, `internal/cli/`, `pkg/config/`).

---

## 2. towline-mcp — Middleware architecture

### CLI flags

In addition to upstream flags (`-server`, `-token`, `-tools`, `-read-only`, `-disable-version-check`):

| Flag | Required | Description |
|------|----------|-------------|
| `-stack <name>` | Yes | Lock MCP instance to a single Portainer stack |
| `-tier <dev\|prod>` | Yes | Deployment tier. Controls approval gating |
| `-proxy <traefik\|caddy\|cloudflare>` | No | Proxy backend. Auto-detected from compose if omitted |
| `-caddy-api <url>` | No | Caddy admin API URL (default `http://localhost:2019`) |

### Middleware chain

```
Tool call arrives (mcp-go dispatch)
  -> Tier gating middleware (blocks + approval flow if prod)
    -> Stack scoping middleware (filters requests/responses to stack)
      -> Container ownership middleware (validates Docker proxy targets)
        -> Original upstream handler
```

Each middleware is a handler wrapper function with signature (`server` is `github.com/mark3labs/mcp-go/server` v0.32.0, the upstream MCP dependency):

```go
type MiddlewareFunc func(server.ToolHandlerFunc) server.ToolHandlerFunc
```

Applied at tool registration time in `cmd/towline-mcp/main.go`:

```go
func wrapHandler(handler server.ToolHandlerFunc, middlewares ...MiddlewareFunc) server.ToolHandlerFunc {
    for i := len(middlewares) - 1; i >= 0; i-- {
        handler = middlewares[i](handler)
    }
    return handler
}
```

### 2.1 Stack scoping middleware

`-stack <name>` scopes all operations to a single Portainer stack.

**Stack name-to-ID resolution**: at startup, the middleware calls `GetLocalStacks()` to resolve the stack name to its integer ID. This mapping is cached and re-resolved on any "stack not found" error (handles stack recreation). If the named stack does not exist at startup (e.g., first deploy of a new project before `towline init` creates the stack), the middleware starts in "pending" mode — read operations return empty results, and the first `createLocalStack` call is allowed through unscoped. After creation, the mapping is established.

**List operations** (listLocalStacks, Docker proxy container listings): post-filter responses to include only resources matching the stack name. For Docker proxy, filter by `com.docker.compose.project=<stack-name>` label.

**Mutations** (updateLocalStack, deleteLocalStack, etc.): pre-validate that the target stack ID matches the resolved scoped stack ID. Reject with a clear error otherwise.

**Docker proxy non-list calls**: resolve the target container and check its `com.docker.compose.project` label before forwarding.

### 2.2 Tier gating middleware

In `dev` tier, all calls pass through immediately. In `prod` tier:

| Operation type | Approval required |
|---|---|
| Read (list, logs, health, inspect, env_get, deployments) | No |
| Operational (restart, start, stop, scale) | No |
| Configuration (env_set, domains_add, domains_remove) | Yes |
| Deploy (create/update stack) | Yes |
| Destructive (delete/stop stack) | Yes |
| Exec, Docker proxy non-GET | Yes |

**In-chat approval flow**:

1. Middleware generates a random 8-character approval token, stores it in an in-memory map with operation details and 5-minute TTL.
2. Returns a tool result:
   ```
   Production operation requires approval.
   Action: updateLocalStack
   Description: <description from tool call>
   To confirm, re-call this tool with the additional parameter approvalToken: "abc123"
   ```
3. Agent presents to user, user confirms, agent re-calls with the token.
4. Middleware validates the token, clears it, forwards to the real handler.
5. Expired or invalid tokens return a clear rejection.

**Approval token as schema parameter**: every tool that can be gated in prod tier includes an optional `approvalToken` parameter in its YAML schema definition. This ensures MCP clients (Claude Code, Cursor, Gemini CLI) can include the token in tool calls without schema validation failures. The middleware checks for this parameter on every gated call.

**Token store**: `sync.Map` of `token -> { toolName, args, createdAt }`. Background goroutine prunes expired tokens every 30 seconds.

### 2.3 Container ownership middleware

Applies only to Docker proxy calls targeting a specific container (container ID or name in the URL path).

1. Extract container identifier from the Docker API path (regex match on `/containers/{id}/...`)
2. Call Docker inspect API to get the container's labels
3. Check `com.docker.compose.project` matches the scoped stack name
4. Reject with `"container does not belong to this stack"` if mismatch

---

## 3. towline-mcp — New tool handlers

Towline tools are defined in `internal/tooldef/towline-tools.yaml`. At server construction, both YAML files are loaded separately via `toolgen.LoadToolsFromYAML` and the resulting maps are merged into a single `map[string]mcp.Tool` before the server is initialized. Towline tool names are prefixed with `towline_` to avoid collisions with upstream tool names. They operate against the scoped stack and accept service names (from docker-compose), not container IDs.

**Service name resolution**: query Docker API filtered by `com.docker.compose.project=<stack>` and `com.docker.compose.service=<service>` labels to resolve service name to container ID(s).

### towline_service_health

- **Input**: optional `service` (string). If omitted, returns all services in the stack.
- **Output**: JSON array:
  ```json
  [
    {
      "service": "app",
      "state": "running",
      "uptime": "2h34m",
      "restartCount": 0,
      "cpuPercent": 2.3,
      "memoryUsageMB": 128,
      "memoryLimitMB": 512,
      "exitCode": null
    }
  ]
  ```
- **Implementation**: Docker proxy `GET /containers/json` filtered by stack label, then `GET /containers/{id}/stats?stream=false` for each.

### towline_service_logs

- **Input**: `service` (required), optional `tail` (int, default 200), optional `since` (RFC3339 timestamp), optional `filter` (string, grep-like line filter)
- **Output**: Plain text log output, Docker binary framing headers stripped.
- **Implementation**: Docker proxy `GET /containers/{id}/logs?stdout=true&stderr=true&tail={n}&since={ts}`. Post-filter lines by `filter` string if provided.

### towline_env_get

- **Input**: optional `name` (string) to read a single variable
- **Output**: JSON object of env vars. Variables whose **names** contain `KEY`, `SECRET`, `PASSWORD`, or `TOKEN` (case-insensitive) have their values masked as `"****"`.
- **Implementation**: Portainer local stack API to read stack env vars.

### towline_env_set

- **Input**: `name` (string), `value` (string)
- **Output**: Confirmation message. Notes that a service restart may be needed.
- **Implementation**: Reads current env vars, updates the target, writes back via `UpdateLocalStack`. Does not trigger a redeploy.
- **Prod tier**: requires approval.

### towline_domains_list

- **Input**: none
- **Output**: JSON array of `{ service, domain, port, method }` where method is `traefik`, `caddy`, or `cloudflare`.
- **Implementation**: varies by proxy backend (see section 5).

### towline_domains_add

- **Input**: `service` (string), `domain` (string), optional `port` (int, default 80), optional `method` (string, `traefik|caddy|cloudflare`, defaults to detected proxy)
- **Output**: Confirmation or Cloudflare MCP delegation instructions.
- **Implementation**: varies by proxy backend (see section 5).
- **Prod tier**: requires approval.

### towline_domains_remove

- **Input**: `service` (string), `domain` (string)
- **Output**: Confirmation.
- **Implementation**: reverse of add.
- **Prod tier**: requires approval.

### towline_scale

- **Input**: `service` (string), `replicas` (int)
- **Output**: Confirmation. Warning if the service has volume mounts.
- **Implementation**: reads compose via `GetLocalStackFile`, parses with `gopkg.in/yaml.v3` (already an upstream dependency), sets `deploy.replicas` on the target service, serializes back, redeploys via `UpdateLocalStack`. Note: standard YAML marshal/unmarshal strips comments and may reorder keys — this is acceptable since compose files managed by towline are machine-generated.
- **Prod tier**: requires approval (classified as deploy).

### towline_deployments

- **Input**: optional `limit` (int, default 10)
- **Output**: JSON array:
  ```json
  [
    {
      "id": "d_abc123",
      "timestamp": "2026-03-16T14:30:00Z",
      "description": "Added redis service",
      "diff": "--- previous\n+++ new\n@@ ...",
      "outcome": "success"
    }
  ]
  ```
- **Implementation**: reads from `towline-{stack}-data:/towline/deployments.json` Docker volume.

### towline_exec

- **Input**: `service` (string), `command` (string or array)
- **Output**: Command stdout/stderr.
- **Implementation**: Docker exec via Portainer proxy is a two-step process. Step 1: `POST /containers/{id}/exec` with `AttachStdout: true, AttachStderr: true, Detach: false`. Step 2: `POST /exec/{execId}/start`. The exec start endpoint returns a hijacked TCP stream. Portainer's Docker proxy supports connection upgrades for exec. If hijack fails (some proxy configurations strip upgrade headers), fallback to `Detach: true` with `POST /exec/{execId}/json` to poll for completion and read output. The implementation should try the streaming approach first and fall back gracefully.
- **Prod tier**: requires approval.

---

## 4. Towline CLI

Go binary using standard library `flag` package. Subcommand dispatch.

**Portainer API client**: the CLI needs Portainer API endpoints not exposed by the upstream SDK — specifically `POST /api/auth` (authentication), `POST /api/users/{id}/tokens` (API key generation), and user creation. The CLI implements its own lightweight HTTP client in `pkg/config/portainer_api.go` that makes direct REST calls using the admin API key. This is separate from the MCP's `PortainerClient` interface, which is designed for MCP handler use.

### towline setup

Interactive first-run configuration.

1. Prompt for Portainer URL
2. Prompt for username/password
3. Authenticate via `POST /api/auth`, receive JWT
4. Generate API key via `POST /api/users/{id}/tokens`
5. List environments via API, user selects one
6. Prompt for projects directory (default `~/projects`)
7. Write `~/.towline/config.yaml` with `0600` permissions
8. Create `~/.towline/templates/` with bundled defaults
9. Create `~/.towline/skills/` with DevOps skill

**Config file** (`~/.towline/config.yaml`):
```yaml
portainer_url: "https://192.168.1.50:9443"
portainer_admin_key: "ptr_xxxx..."
portainer_env_id: 2
projects_dir: "~/projects"
```

### towline init \<project-name\>

Flags: `--tier dev|prod` (default dev), `--template <name>` (default "default"), `--with-prod` (creates both dev and prod stacks)

Sequence:
1. Validate global config exists (prompt to run `setup` if not)
2. Create project directory at `{projects_dir}/{project-name}/`
3. **Portainer provisioning** (via admin API key):
   - Create team `team-{project-name}` via `POST /api/teams`
   - Grant team access to target environment via `PUT /api/endpoints/{id}`
   - Create stack `{project-name}-dev` via local stack creation API with compose from template
   - Create user in team, generate team-scoped API token
4. **Local file generation** (Go `text/template` interpolation):
   - `AGENT.md` — project instructions with stack name, tier, tool reference
   - `.claude/settings.json` — Claude Code MCP config
   - `.cursor/mcp.json` — Cursor MCP config
   - `.gemini/settings.json` — Gemini CLI MCP config
   - `towline.json` — generic/portable MCP config
   - `docker-compose.yml` — from selected template
   - `.env.example` — placeholder env vars
   - `.gitignore` — excludes all settings files, `.env`, `*.key`
   - `skills/towline-devops.md` — copied from embedded or `~/.towline/skills/`
5. `git init` + initial commit

If `--with-prod`: repeat provisioning for `{project-name}-prod` with separate team, API key, `-tier prod`. Add second MCP entry to all agent config files.

**Generated project structure**:
```
~/projects/{project-name}/
├── AGENT.md
├── .claude/settings.json
├── .cursor/mcp.json
├── .gemini/settings.json
├── towline.json
├── docker-compose.yml
├── .env.example
├── .gitignore
├── skills/
│   └── towline-devops.md
└── .git/
```

### towline list

Scans `{projects_dir}/` for directories containing `towline.json`. Extracts stack name and tier. Optionally queries Portainer API for live health.

Output: `PROJECT | STACK | TIER | STATUS`

### towline destroy \<project-name\>

Requires `--confirm` or interactive confirmation.

1. Read project's `towline.json` for stack name and team
2. Delete Portainer stack via API
3. Delete Portainer team via API
4. Print message about removing local files manually

### towline promote \<project-name\>

1. Create `{project-name}-prod` stack with current dev compose content
2. Create separate team and API key
3. Add second MCP entry (with `-tier prod`) to all agent config files
4. Update `AGENT.md` with prod deployment notes

### towline rotate-keys \<project-name\>

1. Generate new API token for the project's team user
2. Revoke old token
3. Update all agent config files with new token

### towline status \<project-name\>

Calls Portainer API for the project's stack. Shows running/stopped containers, last deployment time, restart counts. CLI equivalent of `towline_service_health`.

---

## 5. Proxy management

### Detection

At `towline-mcp` startup:
1. `-proxy` flag if provided (highest priority)
2. Auto-detect from compose file:
   - Traefik labels on any service -> Traefik
   - Service named `caddy` or Caddy volumes -> Caddy
   - No match -> tools return "no proxy configured" with setup instructions

Cloudflare Tunnels are always explicit (specified via `method: "cloudflare"` in tool calls), never auto-detected.

### Traefik

Compose label manipulation. No external API calls.

**towline_domains_add** adds labels to the target service in the compose file:
```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.{stack}-{service}.rule=Host(`app.example.com`)"
  - "traefik.http.routers.{stack}-{service}.entrypoints=websecure"
  - "traefik.http.routers.{stack}-{service}.tls.certresolver=letsencrypt"
  - "traefik.http.services.{stack}-{service}.loadbalancer.server.port=8080"
```
Then redeploys via `UpdateLocalStack`.

**towline_domains_remove** strips the labels, redeploys.

**towline_domains_list** scans compose for `traefik.http.routers.*.rule` labels, extracts domains.

### Caddy

Uses Caddy admin API (configurable via `-caddy-api`, default `http://localhost:2019`).

**towline_domains_add**: POST route config to `/config/apps/http/servers/srv0/routes` mapping domain to `{service}:{port}` on the Docker network. Caddy handles TLS automatically.

**towline_domains_remove**: DELETE route by `@id`.

**towline_domains_list**: GET routes, extract domain matchers.

### Cloudflare Tunnels

Towline does NOT call Cloudflare APIs directly. `towline_domains_add` with `method: "cloudflare"` returns structured instructions for the agent to execute via the Cloudflare MCP:

```json
{
  "status": "requires_cloudflare_mcp",
  "instructions": "Execute these Cloudflare MCP calls in order:",
  "steps": [
    {
      "tool": "cloudflare_tunnel_create",
      "args": {"name": "{stack}-{service}"}
    },
    {
      "tool": "cloudflare_tunnel_route",
      "args": {"tunnel_id": "<from step 1>", "hostname": "app.example.com", "service": "http://{service}:{port}"}
    }
  ]
}
```

`towline_domains_list` with Cloudflare returns a note that tunnel routes are managed externally and suggests querying the Cloudflare MCP.

---

## 6. State management

### Deployment log

Stored as Portainer stack environment variables on the stack itself. The deployment log is serialized as a JSON string in a reserved env var `_TOWLINE_DEPLOYMENTS`. This avoids the complexity of reading/writing files inside Docker volumes from the dev machine (which would require creating temporary containers or exec calls through the Portainer proxy).

Every deploy/update through towline reads the current log from the stack env, appends an entry, and writes it back:
```json
[
  {
    "id": "d_abc123",
    "timestamp": "2026-03-16T14:30:00Z",
    "description": "Added redis service for session caching",
    "diff": "--- previous\n+++ new\n...",
    "outcome": "success"
  }
]
```

The log is capped at 50 entries (oldest dropped) to stay within reasonable env var size limits. The `previousCompose` and `newCompose` fields are NOT stored — only the diff. This keeps the log compact. Full compose content is always available via `getLocalStackFile` for the current state and can be reconstructed from diffs if needed.

Diff computed via line-by-line unified diff. Description is a required parameter on all deploy operations through towline.

### Approval token store

In-memory `sync.Map` of `token -> { toolName, args, createdAt }`. Background goroutine prunes expired tokens (>5 min) every 30 seconds. No persistence.

### What is NOT stored

- No database, no SQLite, no bolt
- No project registry — `towline list` scans the filesystem
- No API key registry — keys live in project config files
- `~/.towline/config.yaml` is the only persistent towline state on the dev machine

---

## 7. Template system

Templates are embedded in the Go binary via `embed.FS` from the `templates/` directory. Users can override by placing files in `~/.towline/templates/` — the CLI checks the user directory first, falls back to embedded.

### Interpolation variables (Go text/template)

| Variable | Example |
|---|---|
| `{{.ProjectName}}` | `receipt-scanner` |
| `{{.StackName}}` | `receipt-scanner-dev` |
| `{{.Tier}}` | `dev` |
| `{{.PortainerURL}}` | `https://192.168.1.50:9443` |
| `{{.EnvironmentID}}` | `2` |
| `{{.APIToken}}` | `ptr_xxxx...` |
| `{{.MCPBinaryPath}}` | `towline-mcp` |

### Bundled compose templates

**default.yml** — empty starter:
```yaml
services: {}
```

**web-app.yml** — app + Postgres + Redis:
```yaml
services:
  app:
    image: "${APP_IMAGE:-nginx:alpine}"
    ports:
      - "8080:80"
    depends_on:
      - db
      - redis
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: "${DB_NAME:-app}"
      POSTGRES_USER: "${DB_USER:-app}"
      POSTGRES_PASSWORD: "${DB_PASSWORD}"
    volumes:
      - db_data:/var/lib/postgresql/data
  redis:
    image: redis:7-alpine
volumes:
  db_data:
```

---

## 8. DevOps skill

The following is the production DevOps skill that ships with every Towline project. It reflects the actual tools, parameters, and behaviors of the real implementation.

```markdown
# Towline DevOps Skill

You have access to Towline MCP tools for deploying and managing containers on
self-hosted infrastructure via Portainer. This skill teaches you how to use
them effectively.

## Mental model

You are operating on an isolated container stack. You can see and control only
the services within your project's stack. Treat this stack as your entire
infrastructure.

Your stack has a tier: dev or prod. In dev, you have full autonomy. In prod,
destructive and configuration-changing actions require your human partner's
approval. When a prod operation needs approval, the tool will return an
approval token — present the operation details to your partner, and if they
confirm, re-call the tool with the approvalToken parameter.

## Tool inventory

### Observation tools (no side effects, use freely)

- towline_service_health — Health snapshot for one or all services: state,
  uptime, restart count, CPU/memory usage, exit code. Always start here when
  diagnosing issues.
  - Parameters: service (optional string)

- towline_service_logs — Recent log output for a service. Strips Docker
  binary framing.
  - Parameters: service (required), tail (int, default 200), since (RFC3339
    timestamp, optional), filter (string, optional grep-like line filter)

- towline_env_get — Read stack environment variables. Variables whose names
  contain KEY, SECRET, PASSWORD, or TOKEN have their values masked.
  - Parameters: name (optional, reads single var)

- towline_domains_list — List all domain/routing mappings for the stack.
  Returns service, domain, port, and proxy method (traefik/caddy/cloudflare).

- towline_deployments — Deployment history with diffs and outcomes.
  - Parameters: limit (int, default 10)

- listLocalStacks — Portainer stack listing (scoped to your stack).

- getLocalStackFile — Current docker-compose.yml content.

- dockerProxy — Low-level Docker API access (scoped to your stack's
  containers). Prefer the higher-level towline tools above.
  - Parameters: environmentId, method, dockerAPIPath, queryParams, headers,
    body

### Action tools (cause changes)

- updateLocalStack — Update the stack's compose definition. Always submit
  the COMPLETE compose file. Include a description of what changed.
  - Parameters: id, endpointId, file (full compose YAML), env (array),
    prune (bool), pullImage (bool)
  - Prod tier: requires approval

- towline_env_set — Update an environment variable. Service restart may be
  needed to pick up the change.
  - Parameters: name (string), value (string)
  - Prod tier: requires approval

- towline_domains_add — Add a domain routing to a service. Supports Traefik
  (labels), Caddy (admin API), or Cloudflare Tunnels (delegates to
  Cloudflare MCP).
  - Parameters: service (string), domain (string), port (int, default 80),
    method (optional: traefik|caddy|cloudflare)
  - Prod tier: requires approval

- towline_domains_remove — Remove a domain routing.
  - Parameters: service (string), domain (string)
  - Prod tier: requires approval

- towline_scale — Scale a service to N replicas. Warns if the service has
  persistent volumes.
  - Parameters: service (string), replicas (int)
  - Prod tier: requires approval

- towline_exec — Run a command inside a running container.
  - Parameters: service (string), command (string or array)
  - Prod tier: requires approval

- startLocalStack / stopLocalStack — Start or stop the entire stack.
  - Prod tier: stop requires approval

- deleteLocalStack — Delete the entire stack.
  - Prod tier: requires approval

## Decision framework

### "Something is broken"

Follow this sequence. Stop as soon as you identify the issue.

1. Start with health. Call towline_service_health (no service parameter) to
   get all services. Look for: containers not running, high restart counts
   (>3 in the last hour = crash loop), exit code 137 (OOM killed), services
   stuck in "restarting" state.

2. Check logs. Call towline_service_logs on the unhealthy service with
   tail: 200. Look for: stack traces, "connection refused" (dependency not
   ready), "permission denied" (volume/entrypoint issue), missing env var
   errors.

3. Check dependency health. If the failing service depends on a database,
   cache, or other service, check that service's health and logs. Most
   "app broken" problems are actually "database not ready."

4. Check environment variables. If logs mention missing config, call
   towline_env_get to verify expected variables are set.

5. Check the compose file. If the issue is structural (wrong image, missing
   volume, port conflict), call getLocalStackFile.

6. Check deployment history. If the service was working recently, call
   towline_deployments. The diff often points directly at the problem.

Do NOT restart without understanding the root cause. A restart fixes transient
issues but masks persistent ones. If a container is crash-looping, restarting
it just adds another crash.

### "Deploy this service"

1. Review current state. Call getLocalStackFile to see the existing compose.
   Understand what's there before modifying.

2. Set environment variables first. If the service needs env vars, call
   towline_env_set before deploying. The deploy picks up current env vars.

3. Deploy. Call updateLocalStack with the COMPLETE compose file. Always
   include a description of what changed.

4. Verify. Call towline_service_health to confirm all services started.
   Then towline_service_logs on the new/updated service.

5. Configure routing if needed. Call towline_domains_add after confirming
   the service is healthy.

### "Expose this service / add a domain"

1. Confirm the service is running with towline_service_health.
2. Check existing domains with towline_domains_list.
3. Add the domain with towline_domains_add. Specify traefik, caddy, or
   cloudflare as the method depending on your proxy setup. For external
   access through Cloudflare Tunnels, use method: "cloudflare" and follow
   the returned Cloudflare MCP instructions.
4. Verify with towline_domains_list.

### "Update a configuration value"

1. Read current value with towline_env_get.
2. Set new value with towline_env_set.
3. Restart the affected service if it requires a restart to pick up env
   changes (most do). Use dockerProxy to restart, or redeploy with
   updateLocalStack.
4. Verify with towline_service_logs.

### "Debug connectivity between services"

1. Check both services are running with towline_service_health.
2. Services in the same stack share a Docker network by default. The
   compose service name is the hostname. Verify: correct service names in
   compose, target service exposes the expected port, no network_mode
   override.
3. Test from inside the container with towline_exec:
   wget -qO- http://service-b:8080/health
   or: nc -zv service-b 5432
4. Check logs on both sides. Connection refusal shows in the client logs;
   the reason shows in the server logs.

### "Roll back a broken deployment"

1. Call towline_deployments to find the last good compose definition.
2. Call updateLocalStack with that compose content. Description:
   "rollback to deployment from [timestamp]"
3. Verify with towline_service_health and towline_service_logs.

## Common mistakes

Partial compose files: Always submit the COMPLETE compose file to
updateLocalStack. If you only include the changed service, Portainer removes
all other services. Always call getLocalStackFile first, modify the result,
and submit the full content.

Restarting without diagnosing: A restart is not a fix. Read logs first.

Setting env vars after deploy: If your compose references env vars that don't
exist yet, the service starts with empty values. Set vars before deploying.

Forgetting dependencies: Before concluding a service is broken, check its
dependencies. A service connecting to postgres on startup crashes immediately
if postgres isn't ready.

Scaling stateful services: Don't scale services with persistent volume mounts
to multiple replicas. You'll get data corruption or mount conflicts.

Ignoring exit codes: 137 = OOM killed (needs more memory). 1 = application
error (check logs). 143 = SIGTERM (graceful shutdown, usually fine).
126 = permission denied on entrypoint. 127 = entrypoint not found.

## Efficiency tips

Batch observations: Call towline_service_health for all services first rather
than inspecting one by one. The overview tells you where to focus.

Use service names: All towline tools accept compose service names. You don't
need to resolve container IDs.

Request enough log lines: Use tail: 200 minimum. A truncated log starting
mid-output is useless for diagnosis.

Describe your deployments: Always include a meaningful description when
deploying. It shows in deployment history and approval requests.

Check before you act: Before any mutation, verify current state. One extra
observation call prevents accidental overwrites.
```

---

## 9. Security considerations

- **Scoped credentials**: each project gets a team-scoped Portainer API key. The admin key in `~/.towline/config.yaml` is never passed to projects or agents.
- **No secrets in git**: `.gitignore` excludes all agent config files (containing API tokens) and `.env` files.
- **File permissions**: `~/.towline/config.yaml` written with `0600`.
- **Defence in depth**: three independent layers (Portainer team scoping, MCP stack filtering, tier-based gating).
- **Network boundary**: towline-mcp communicates with Portainer API over user-configured network. No ports exposed to the internet.
- **Approval tokens**: short-lived (5 min), random, in-memory only. One-time use.

# Getting Started with Towline

This guide walks you through installing Towline, connecting it to your Portainer instance, and creating your first project — a complete environment where your AI agent can build, deploy, and operate software on your Portainer infrastructure. About 5 minutes end to end.

## Prerequisites

Before you begin, you need:

- **Portainer CE 2.28+** running on any Docker host (homelab, VPS, cloud VM, local machine). The free Community Edition is sufficient. API access must be enabled (it is by default).
- **Docker** on the machine Portainer manages.
- **An AI coding agent** that supports MCP: [Claude Code](https://claude.ai/code), [Cursor](https://cursor.sh), [Gemini CLI](https://github.com/google-gemini/gemini-cli), [Windsurf](https://windsurf.ai), or [Codex](https://github.com/openai/codex).
- **Go 1.24+** for building from source (or download a pre-built release when available).
- **Network access** from your development machine to the Portainer API. Same LAN, Tailscale, SSH tunnel — any connectivity works.

## Step 1: Install Towline

The fastest way:

```bash
curl -fsSL https://towline.dev/install | sh
```

This detects your OS and architecture, downloads the correct binaries from GitHub Releases, verifies each archive's SHA-256 against the release's `checksums.txt` before extracting it, and installs to `/usr/local/bin` (or `~/.local/bin` if you don't have sudo). The install aborts on a checksum mismatch, a missing checksum entry, a missing `checksums.txt`, or if no `sha256sum`/`shasum` tool is available.

Or build from source:

```bash
git clone https://github.com/changethisusername/towline.git
cd towline
make build
sudo cp dist/towline dist/towline-mcp /usr/local/bin/
```

Verify:

```bash
towline
# Should print: Usage: towline <command> [args]
```

## Step 2: Connect to Portainer

Run the interactive setup wizard:

```bash
towline setup
```

It will prompt you for:

1. **Portainer URL** — the full URL including port, e.g. `https://192.168.1.50:9443`
2. **Self-signed certificate?** — Towline verifies Portainer's TLS certificate by default. Answer yes only if Portainer uses a self-signed cert; this stores `skip_tls_verify: true` and adds `-skip-tls-verify` to generated MCP configs.
3. **Admin username and password** — Towline authenticates once to generate a scoped admin API key. Your password is not stored.
4. **Environment** — if you have multiple Docker environments in Portainer, select which one Towline should use for new projects.
5. **Projects directory** — where new projects will be created (default: `~/projects`).
6. **Prod approval mode** — how prod-tier operations (deploys, config changes, exec) are approved:
   - **Human** (recommended) — you approve each one in Towline's built-in approval server. Setup prints your **approver token once**; store it in your password manager and keep it away from your agents. Start the server with `towline approvals serve`.
   - **Agent** — the agent confirms its own prod operations with an explicit second call. For autonomous/agentic setups.

   Re-running `towline setup` keeps your existing approval settings. Change them any time with `towline approvals setup` (see the [User Guide](user-guide.md#choosing-an-approval-mode)).

Towline saves the configuration to `~/.towline/config.yaml` with restricted file permissions (`0600`). The admin API key is used only by the CLI for provisioning — it is never passed to AI agents.

## Step 3: Create your first project

```bash
towline init my-first-app
```

Project names must match `^[a-z0-9][a-z0-9_-]{0,62}$` — lowercase letters, digits, `-` and `_`, starting with a letter or digit.

In under 60 seconds, this:

1. Creates a **Portainer team** (`team-my-first-app-dev`) with the "Standard user" role on one environment
2. Creates a **service user** in that team with its own API key
3. Creates a **Docker Compose stack** (`my-first-app-dev`) on your Portainer instance
4. Generates **agent configuration files** for Claude Code, Cursor, and Gemini CLI
5. Copies the **DevOps skill** that teaches agents how to use the tools
6. Initializes a **git repository** with an initial commit

Your project is now at `~/projects/my-first-app/` (or wherever you configured the projects directory).

### What's in the project?

```
my-first-app/
├── AGENT.md                    # Instructions for your AI agent
├── docker-compose.yml          # Starter compose (empty by default)
├── towline.json                # Project metadata (stack name, team, API key)
├── .env.example                # Environment variable template
├── .gitignore                  # Excludes secrets and agent configs
├── skills/
│   └── towline-devops.md       # Operational guide for agents
├── .mcp.json                   # Claude Code MCP config
├── .claude/settings.json       # Claude Code permissions
├── .cursor/mcp.json            # Cursor MCP config
└── .gemini/settings.json       # Gemini CLI MCP config
```

The MCP config files contain the project's API token (in an `env` block as `TOWLINE_PORTAINER_TOKEN`, not on the command line), so they're written with `0600` permissions and listed in `.gitignore`. If you run `towline init` inside an existing codebase, Towline appends any missing entries (`.mcp.json`, `.claude/`, `.cursor/`, `.gemini/`, `towline.json`) to its `.gitignore`.

### Using a compose template

If you want to start with a pre-configured stack instead of an empty one:

```bash
towline init my-web-app --template web-app
```

The `web-app` template sets up an Nginx app server, PostgreSQL 16, and Redis 7 with persistent storage — ready for your agent to customize.

Template packs work the same way — e.g. `--template ai-stack` (Ollama + Open WebUI + PostgreSQL) or `--template n8n` — and also add stack-specific skills.

## Step 4: Start an agent session

Open the project in your AI coding agent:

```bash
cd ~/projects/my-first-app

# Claude Code
claude

# Or Cursor, Gemini CLI, etc.
```

The agent automatically connects to the Towline MCP server. You can verify by asking:

> "Check the health of all services in the stack."

The agent will call `towline_service_health` and report the current state. If you used the default template, the stack is empty — the agent will tell you there are no services running.

## Step 5: Build something

This is where it gets interesting. Your agent doesn't just deploy containers — it builds and ships software. Try:

> "Build me a simple URL shortener. Use Go for the API, Postgres for storage. Deploy it and expose it on port 8080."

The agent will write the application code, create a Dockerfile, set up the database, deploy everything, and verify it works — all using the Towline MCP tools.

### More examples

> "Add PostgreSQL and Redis to the stack. Set the database password to 'devpass123'. Verify all services are healthy and can reach each other."

> "The app is crash-looping. Diagnose the issue and fix it."

> "Scale the worker service to 3 replicas."

> "Expose the API on api.mylab.local via Traefik."

The DevOps skill in `skills/towline-devops.md` teaches the agent operational decision-making — how to diagnose before restarting, how to check dependencies, how to deploy safely. It's not just tooling access; it's the knowledge to use it well.

## Step 6: Manage your projects

### List all projects

```bash
towline list
```

```
PROJECT              STACK                     TIER   STATUS
-------              -----                     ----   ------
my-first-app         my-first-app-dev          dev    -
my-web-app           my-web-app-dev            dev    -
```

### Destroy a project

When you're done with a project:

```bash
towline destroy my-first-app
```

This deletes the Portainer stack, service user, and team. Before deleting, Towline checks each ID in `towline.json` against Portainer (the stack must be named `<project>-<tier>`, the user `towline-<stack>`, the team `team-<stack>`) and skips anything that doesn't match. Your local project files remain — remove them manually if you want.

## What's next

### Use the web-app template for real projects

```bash
towline init receipt-scanner --template web-app
```

This gives you an app + PostgreSQL + Redis stack. Ask your agent to swap the Nginx image for your actual application image.

### Set up a production tier

```bash
towline init my-app --with-prod
```

This creates both `my-app-dev` and `my-app-prod` stacks with separate teams and API keys. In the prod tier, deploys, configuration changes, exec, and destructive operations need approval, depending on the mode you chose at setup:

- **Human mode**: keep `towline approvals serve` running. The agent sends the request, tells you what it wants to do, and waits. Approve it at `http://127.0.0.1:8787/ui` (log in with your approver token) or with `towline approvals approve <id>`; the agent then re-runs the call. Requests not decided within 30 minutes expire.
- **Agent mode**: the agent gets a confirmation token on the first call and confirms by calling again with it. It's a deliberate second step, not a human gate.

### Add domain routing

If you use Traefik as your reverse proxy (common in homelabs), the agent can manage domains directly:

> "Expose the app service on app.mylab.local"

Towline detects Traefik from your compose labels and adds the correct routing configuration automatically. Caddy and Cloudflare Tunnels are also supported.

### Customize templates

Copy the bundled templates to override them:

```bash
mkdir -p ~/.towline/templates/compose
cp templates/compose/web-app.yml ~/.towline/templates/compose/my-custom.yml
# Edit my-custom.yml to your needs
towline init new-project --template my-custom
```

### Read the DevOps skill

The file at `skills/towline-devops.md` in each project is worth reading yourself. It documents:

- Every available MCP tool and its parameters
- Decision frameworks for common scenarios (debugging, deploying, scaling, rollbacks)
- Common mistakes agents make and how to avoid them
- Exit code meanings (137 = OOM, 1 = app error, 143 = graceful shutdown, etc.)

## Upgrading

After `towline update`, bring existing projects up to date:

```bash
towline refresh --all        # or: towline refresh my-app  (or plain `towline refresh` inside a project)
```

This updates each project's MCP configs (keeping your other MCP servers and custom flags), moves the token into an env var, applies your approval mode and compose policy, fixes file permissions and `.gitignore`, and moves the project team to the Standard user role (`--keep-role` skips that). Restart your agent sessions afterwards. Projects you don't refresh keep working as before — they just miss the new hardening. See the [User Guide](user-guide.md#upgrading) for details.

---

## Troubleshooting

### "towline not configured. Run 'towline setup' first"

You haven't run `towline setup` yet, or the config file at `~/.towline/config.yaml` is missing.

### "authentication failed"

Check that your Portainer URL is correct and includes the port (typically `9443` for HTTPS). Verify your admin credentials work in the Portainer web UI.

### "failed to create team" or "failed to create stack"

Your Portainer admin API key may have insufficient permissions. Re-run `towline setup` to generate a new key.

### "failed to verify Portainer's TLS certificate"

Portainer is using a certificate your machine doesn't trust. If it's self-signed, re-run `towline setup` and answer yes when asked about a self-signed certificate.

### "your ~/.towline/config.yaml predates TLS verification"

Configs from earlier releases have no `skip_tls_verify` key and keep skipping certificate verification, as before. Add `skip_tls_verify: false` to `~/.towline/config.yaml` to verify Portainer's certificate, or `skip_tls_verify: true` to keep skipping and silence the warning (or re-run `towline setup`, which asks). Then run `towline refresh --all`.

### Agent says a compose file was rejected

Towline's compose security policy blocks settings that could escape the container sandbox (privileged mode, host bind mounts, host networking, etc.). Use named volumes instead of bind mounts. If a project genuinely needs a specific host path, you (not the agent) can allow it under `compose_policy` in the project's `towline.json` and run `towline refresh`. See the [User Guide](user-guide.md#compose-security-policy) for the full list.

### Agent says "no containers found for service"

The service may not be deployed yet, or the stack is empty. Check with `towline_service_health` first, then deploy using `updateLocalStack`.

### Agent can't connect to the MCP server

Verify `towline-mcp` is on your PATH:

```bash
which towline-mcp
```

Check the agent MCP config file (e.g., `.mcp.json`) points to the correct binary path and has the right Portainer URL, and that `TOWLINE_PORTAINER_TOKEN` is set in its `env` block.

### Portainer shows the stack but the agent can't see it

The stack name must exactly match what was created by `towline init`. Check `towline.json` in the project directory for the `stack_name` field, and verify it matches the stack name in Portainer.

---

## Security model

Towline uses three independent layers to ensure project isolation:

1. **Portainer team-scoped API keys** — the hard boundary. Each project's API key can only access its own team's resources. Project teams get Portainer's "Standard user" environment role, so one project's key can't manage other projects' stacks. Even if everything else fails, Portainer enforces this. Portainer's environment security settings for non-admin users (e.g. disabling bind mounts or privileged mode for regular users) also apply to project users — a good server-side backstop.

2. **MCP stack-name filtering** — the agent experience layer. Filters Docker API responses to show only the project's containers, rejects compose files that use privileged mode, host bind mounts, host networking and similar escapes, and limits `dockerProxy` writes to lifecycle actions on the stack's own containers. Provides clear error messages if an agent references something outside its scope.

3. **Tier-based approval gating** — the deployment control layer. In prod tier, deploys, configuration changes, exec, and destructive operations wait for a human to approve them in the approval server (human mode), or for the agent's explicit confirmation (agent mode).

### What agents CAN'T do

- See or modify containers from other projects
- Access the Portainer admin API (team management, user creation, etc.)
- Operate on containers not created by their stack's compose file
- Deploy privileged containers, host bind mounts, or host networking
- Create containers or exec sessions, or touch networks, volumes, or images, through the raw Docker proxy
- Execute gated prod operations without a human approving them in your approval server (in human mode)

### What stays on your machine

- The admin API key (`~/.towline/config.yaml`) is never shared with agents
- Agent config files (`.mcp.json`, `.claude/`, `.cursor/`, `.gemini/`, `towline.json`) are gitignored by default, and MCP configs are written with `0600` permissions
- The project API token is passed to `towline-mcp` via an environment variable, so it doesn't appear in the process list
- The MCP server runs locally as a child process of the agent — no external network exposure

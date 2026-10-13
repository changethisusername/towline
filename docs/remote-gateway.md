# Remote gateway: use Towline from Claude, ChatGPT/Codex and other apps

The remote gateway lets apps with no terminal, such as Claude (web, desktop, mobile) or ChatGPT/Codex, use your Towline projects as a **connector**. It runs as one Docker stack next to Portainer. A Cloudflare Tunnel publishes it on your own domain, so it works behind NAT, CGNAT or double NAT with no port forwarding and no VPN.

```
App (Claude, ChatGPT, ...) ──HTTPS──▶ Cloudflare ◀──outbound tunnel── cloudflared ──▶ gateway ──▶ Portainer
                                       mcp.example.com                     (your server)
```

What you get:

- **Connections you create in a web page.** Each one names the projects it can use and whether it can only read or can also deploy. Revoke it any time; the next request fails.
- **The same safety layers as local Towline.** Each project keeps its own team-scoped Portainer key, stack scoping, compose security policy and container ownership checks.
- **Your approval for changes.** By default every deploy, configuration change, exec or destructive action made through the gateway waits for you on the gateway page, on dev projects too. Pass `--ntfy-url https://ntfy.sh/<your-topic>` to `towline remote setup` to get a push notification.
- **Guidance for agents without files.** Apps get a `towline_guide` tool and server instructions, because they can't read `CLAUDE.md` or skills from disk.
- **An audit log.** Every tool call and sign-in is logged as JSON to the gateway's output. Tool arguments are never logged.

## 1. Create a Cloudflare Tunnel

You need a domain on Cloudflare (the free plan is enough).

1. In the Cloudflare dashboard, open **Zero Trust → Networks → Tunnels → Create a tunnel**, choose **Cloudflared**, and name it (e.g. `towline`).
2. Copy the tunnel token from the install command (the long string after `--token`). You don't need to run that command.
3. Under **Public hostnames**, add `mcp.example.com` with service type **HTTP** and URL **`gateway:8080`**.

Claude and ChatGPT call `/mcp`, `/p/*`, `/.well-known/*` and `/oauth/*` from their own servers and can't solve challenges:

- Turn **Bot Fight Mode** off for the zone. On the free plan, skip rules can't bypass it.
- If you use "I'm Under Attack" or strict WAF rules, add a skip rule for those paths.
- Don't put Cloudflare Access in front of those paths. You can put Access in front of the two pages you open in your own browser, `/ui` and `/oauth/authorize`, for an extra login factor. Leave `/mcp`, `/p/*`, `/.well-known/*`, `/oauth/register`, `/oauth/token` and `/oauth/revoke` open: apps call them from their servers, and the gateway authenticates every one of them itself.

Cloudflare ends a request that gets no answer within 100 seconds. A tool call that takes longer, such as a deploy that pulls large images, answers "still running" after 80 seconds and finishes in the background; the app can check the result with `towline_deployments`.

## 2. Deploy the gateway

On the machine where you use the `towline` CLI:

```bash
export TUNNEL_TOKEN=<token>   # or --tunnel-token, which shows up in shell history
towline remote setup --url https://mcp.example.com shop blog
#   or --all to serve every project
```

Projects are named by their directory in your projects folder (`shop`, `blog`). On the gateway they appear by stack name (`shop-prod`, `blog-dev`): that is the name in `/p/<project>/mcp` and in tool names like `shop-prod__towline_service_health`.

This:

- reads each project's `towline.json` (its scoped Portainer key, stack, tier and compose policy),
- generates an **owner token** and prints it once (keep it in your password manager),
- deploys a `towline-gateway` stack through Portainer: the gateway and `cloudflared`. The configuration and tunnel token go in as stack environment variables, never in the compose file.

Already run a reverse proxy with TLS? `--no-tunnel --port 8080` skips `cloudflared` and publishes the gateway on that host port instead; point your proxy at it and keep the same path rules as above.

Don't use Portainer? `--compose-dir ./gateway` writes `docker-compose.yml` and a private `.env` instead; run `docker compose up -d` there.

The gateway reaches Portainer at your configured `portainer_url`. A `localhost` URL is mapped to the Docker host (`host.docker.internal`), which needs Portainer to listen on more than `127.0.0.1`. Use `--portainer-url` if containers reach Portainer by another name, for example when your machine uses a `.local`, Tailscale or Cloudflare Access hostname.

Projects served remotely need a stack name of at most 24 lowercase letters, digits and dashes, so that tool names stay within the 64-character limit apps enforce.

## 3. Connect an app

Open `https://mcp.example.com/ui` and sign in with the owner token. Under **Connect an app**, pick:

- a name,
- the projects,
- **Read only** or **Deploy** access,
- how the app connects.

The three ways to connect:

| Method | For | What happens |
|---|---|---|
| **App sign-in** (default) | Claude, ChatGPT/Codex, any app that adds a connector by URL | Opens a 10-minute window. Add `https://mcp.example.com/mcp` as a connector in the app. The app opens a sign-in page on your gateway; check it's the app you just added and approve. |
| **Client ID and secret** | Claude custom connector → Advanced settings, ChatGPT "User-defined OAuth client" | Shows a client ID and secret once. Paste them with the URL. The secret only works for signing in. Claude's and ChatGPT's callback URLs are allowed by default. |
| **Header token** | Claude Code, Cursor, scripts | Shows a bearer token once, plus a ready `claude mcp add` command. |

Apps that sign in with app sign-in register again each time you remove and re-add the connector, so open a new sign-in window first.

**URLs.** `/mcp` serves every project of the connection, with tools named `<project>__<tool>`. `/p/<project>/mcp` serves one project with plain tool names; use it for clients that limit tool count or name length (Cursor).

## 4. Approvals

When an app asks for a gated change, nothing runs yet. It shows up under **Waiting for your approval** with the full arguments and compose file. The app tells you what it wants to do, with a link to the page, and waits. After you approve or reject, tell the app; it re-calls with the same arguments, and the change runs once.

Approval tokens are bound to the connection that asked, the tool and the exact arguments. They are single use and expire after 30 minutes. Saying "yes" in the chat approves nothing.

`towline remote setup --approval prod` limits approvals to prod projects, so dev projects run changes immediately. Prod projects always need approval through the gateway; agent self-confirmation is never used remotely.

## 5. Auto-deploy on merge

The gateway can watch a GitHub branch and deploy each new commit's compose file to a project's existing stack. Merge a pull request, and the stack follows, with your approval on the gateway page by default.

Add an `auto_deploy` block to the project's `towline.json`:

```json
"auto_deploy": {
  "repo": "acme/shop",
  "branch": "main",
  "compose_path": "docker-compose.yml",
  "require_checks": true,
  "commit_env": "GIT_SHA"
}
```

Only `repo` is required. `branch` defaults to `main`, `compose_path` to `docker-compose.yml`, and `require_checks` to `true`. `commit_env` is optional: it sets that stack env var to the deployed commit SHA, so the compose file can pin `image: ghcr.io/acme/shop:${GIT_SHA}`.

`towline.json` is writable by the project's agent, so the gateway doesn't trust a new or changed block on its own. Accept it on your machine:

```bash
towline remote deploy --accept-auto-deploy
```

The CLI remembers what you accepted in `~/.towline/remote.yaml`. If the repo, branch, path, `require_checks` or `commit_env` changes, auto-deploy for that project stays off at the next deploy, and the CLI tells you to accept it again. Each deploy prints what auto-deploy will do and warns about settings that skip your approval or the checks.

### GitHub access

Public repositories work without a token, but GitHub allows only 60 unauthenticated requests an hour, so the gateway checks every 5 minutes. A token raises the limit (checks every minute) and is needed for private repositories:

```bash
# A fine-grained token, read-only: Contents, Commit statuses, Checks and Metadata.
towline remote github --token-stdin < token.txt
```

The token is read from stdin so it never shows up in shell history or `ps`. `--clear-token` removes it.

For deploys within seconds of a merge, add a webhook:

```bash
towline remote github --webhook
```

This prints a secret once. In the repository, open **Settings → Webhooks → Add webhook** and set:

- Payload URL: `https://mcp.example.com/hooks/github`
- Content type: `application/json`
- Secret: the printed secret
- Events: **Just the push event**

If Cloudflare challenges or blocks automated requests (see step 1), let `/hooks/github` through as well. `--clear-webhook` turns the webhook off. Both commands redeploy the gateway.

### How it behaves

- **First enable is a baseline.** When auto-deploy is turned on, or the repo, branch or path changes, the current head counts as deployed and nothing runs. The next new commit deploys. Use **Deploy latest now** on the gateway page to deploy the current head anyway.
- **Checks.** With `require_checks`, a commit deploys only when all its check runs passed (success, neutral or skipped) and any commit statuses are green. A commit with no checks at all waits, so `require_checks` needs CI on the branch. The commit must also have been the branch head for 2 minutes, so checks that register late aren't missed.
- **Approvals.** The deploy goes through the project's own `updateLocalStack`, with the same compose policy, approval gating, stack scoping and env handling as an app's call. It shows up under **Waiting for your approval** as asked by "auto-deploy (acme/shop)", with the repo, branch, full SHA, commit subject and check summary. With `--approval prod`, dev projects deploy merges without asking you.
- **Superseding.** If a newer commit lands while one is waiting, the old request is marked superseded and can no longer be approved; the newer commit is asked for instead.
- **Rejected or failed commits** are not asked for or retried again. A rejected request, or one nobody decided within 30 minutes, waits for the next commit. A deploy that fails, including a compose file the policy refuses, is recorded on the page with its error. **Deploy latest now** clears both and tries the current head again.
- **It never creates stacks.** If the project's stack doesn't exist, auto-deploy waits and says so. Create the stack first.
- **Merges and direct pushes look the same.** The gateway sees only the branch head. Protect the branch on GitHub (pull requests and status checks required) so only reviewed code reaches it.

Every step is in the audit log as `"event":"autodeploy"`, and each deploy as a `tool_call` with `"source":"auto-deploy"` and the commit SHA. The deployment history (`towline_deployments`) shows `Auto-deploy acme/shop@<sha>: <subject>`.

The gateway image and CLI move together. An older gateway image refuses a configuration with auto-deploy settings, so after `towline update`, run `towline remote deploy` before turning auto-deploy on.

## Managing it

```bash
towline remote status          # configuration and a health check
towline remote add api         # serve another project (redeploys)
towline remote remove blog     # stop serving a project (redeploys)
towline remote deploy          # redeploy, e.g. after changing a project's key or compose_policy
towline remote owner-token     # issue a new owner token (redeploys)
towline remote deploy --accept-auto-deploy   # accept the projects' auto_deploy blocks as they are now
towline remote github --token-stdin | --webhook   # GitHub token and webhook for auto-deploy (redeploys)
```

The gateway image follows your CLI version. After `towline update`, run `towline remote deploy` to move the gateway to the new release.

Connections and OAuth grants live in the gateway's `gateway-data` volume and survive redeploys. Pending approvals, open sign-in windows and page logins don't survive a restart; the app asks again. Revoking a connection on the page ends its access immediately. Removing a project from the gateway also removes it from every connection, and adding it back later doesn't restore that access.

`towline destroy` removes the project from the gateway's configuration; run `towline remote deploy` afterwards. If you deleted a project's directory first, `towline remote remove <name>` still works.

## Security notes

- **Blast radius.** The gateway holds the Portainer keys of the projects it serves, never the admin key. A gateway compromise reaches those projects only.
- **Prompt injection.** An app that can deploy and also reads untrusted content (web pages, email, logs) can be steered into asking for changes. Prefer read-only connections, give deploy access to as few projects as possible, and read approval requests before approving. Multi-project deploy connections are flagged on the page for this reason.
- **Credentials.** Secrets are shown once and stored only as SHA-256 hashes. Every credential kind has its own prefix (`twl_ct_` header token, `twl_cs_` client secret, `twl_at_`/`twl_rt_` OAuth tokens), and each is accepted only where it belongs. Access tokens last 1 hour. A token issued for `/mcp` works there and on `/p/<project>/mcp` for the connection's own projects; a token issued for one project's URL works only there. Refresh tokens last 30 days.
- **Registration** of new apps is only open while you have a sign-in window open. A registered app gets nothing until you approve it with the owner token.
- **The container** runs as a non-root user on a read-only filesystem with all capabilities dropped and no published port; only `cloudflared` talks to the outside.
- **Auto-deploy.** GitHub never gets Portainer credentials, and the gateway only reads from GitHub with an optional read-only token. Everything GitHub returns is treated as untrusted: SHAs are validated, sizes capped, redirects refused, and commit subjects stripped of control and formatting characters. The webhook only makes the gateway look sooner; its payload never chooses what to deploy, so the gateway only reads the configured repository's branch and never a fork's commits. Deploys run as the watcher's own identity, which can only list the project's stack and update it, through the full middleware chain.
- **Host checks.** Requests must be addressed to your public hostname, and browser requests from other origins are refused. That includes MCP calls from browser-based tools such as MCP Inspector in browser mode. Use the header-token method from a non-browser client instead.

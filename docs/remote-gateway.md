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
- Don't put Cloudflare Access in front of those paths. You can put Access in front of `/ui` for an extra login factor.

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

## Managing it

```bash
towline remote status          # configuration and a health check
towline remote add api         # serve another project (redeploys)
towline remote remove blog     # stop serving a project (redeploys)
towline remote deploy          # redeploy, e.g. after changing a project's key or compose_policy
towline remote owner-token     # issue a new owner token (redeploys)
```

Connections and OAuth grants live in the gateway's `gateway-data` volume and survive redeploys. Pending approvals, open sign-in windows and page logins don't survive a restart; the app asks again. Revoking a connection on the page ends its access immediately. Removing a project from the gateway also removes it from every connection, and adding it back later doesn't restore that access.

`towline destroy` removes the project from the gateway's configuration; run `towline remote deploy` afterwards. If you deleted a project's directory first, `towline remote remove <name>` still works.

## Security notes

- **Blast radius.** The gateway holds the Portainer keys of the projects it serves, never the admin key. A gateway compromise reaches those projects only.
- **Prompt injection.** An app that can deploy and also reads untrusted content (web pages, email, logs) can be steered into asking for changes. Prefer read-only connections, give deploy access to as few projects as possible, and read approval requests before approving. Multi-project deploy connections are flagged on the page for this reason.
- **Credentials.** Secrets are shown once and stored only as SHA-256 hashes. Every credential kind has its own prefix (`twl_ct_` header token, `twl_cs_` client secret, `twl_at_`/`twl_rt_` OAuth tokens), and each is accepted only where it belongs. Access tokens last 1 hour. A token issued for `/mcp` works there and on `/p/<project>/mcp` for the connection's own projects; a token issued for one project's URL works only there. Refresh tokens last 30 days.
- **Registration** of new apps is only open while you have a sign-in window open. A registered app gets nothing until you approve it with the owner token.
- **The container** runs as a non-root user on a read-only filesystem with all capabilities dropped and no published port; only `cloudflared` talks to the outside.
- **Host checks.** Requests must be addressed to your public hostname, and browser requests from other origins are refused. That includes MCP calls from browser-based tools such as MCP Inspector in browser mode. Use the header-token method from a non-browser client instead.

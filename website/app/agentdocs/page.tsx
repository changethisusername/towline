import Link from "next/link";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Agent Documentation — Towline",
  description:
    "Complete reference for AI agents to understand and use Towline MCP tools, CLI, and deployment workflows.",
};

export default function AgentDocs() {
  return (
    <>
      {/* Nav */}
      <nav className="mx-auto flex h-14 max-w-3xl items-center justify-between px-6 text-[13px]">
        <Link href="/" className="font-medium tracking-tight text-text">
          Towline
        </Link>
        <span className="text-text-muted">Agent Documentation</span>
      </nav>

      <main className="mx-auto max-w-3xl px-6 pt-8 pb-20 prose-agent">
        <h1>Towline Agent Documentation</h1>
        <p>
          This document is designed for AI agents (Claude Code, Cursor, Codex, Gemini CLI, Windsurf, etc.)
          that need to understand what Towline is, how it works, and how to use its tools to build, deploy,
          and operate software on Portainer-managed infrastructure.
        </p>

        <blockquote>
          <strong>TL;DR:</strong> Towline gives you an isolated container stack on Portainer with 10 high-level
          MCP tools for deployment, health checks, logs, domain routing, scaling, and exec. You can go from
          &quot;write code&quot; to &quot;running in production&quot; without leaving your MCP session.
        </blockquote>

        <h2 id="what-is-towline">What is Towline?</h2>
        <p>
          Towline is the agentic engineering SDK for <a href="https://portainer.io" target="_blank" rel="noopener noreferrer">Portainer</a>.
          It has three components:
        </p>
        <ol>
          <li><strong>towline-mcp</strong> — An MCP server (Go binary) that runs per-project, providing your agent with
          scoped access to a single Portainer stack. It adds stack isolation, tier-based approval gating, and 10
          high-level operational tools on top of the upstream Portainer MCP.</li>
          <li><strong>towline CLI</strong> — A scaffolding tool. <code>towline init &lt;project&gt;</code> creates
          everything: Portainer team, scoped API key, container stack, agent configuration, compose files, and git repo.</li>
          <li><strong>Template packs</strong> — Compose files + agent skills + MCP configs bundled as starter kits
          (api, fullstack, worker, static-site, databases, etc.).</li>
        </ol>

        <h2 id="how-it-works">How Your MCP Session Works</h2>
        <p>
          When your user runs <code>towline init my-project</code>, it creates a <code>.mcp.json</code> (or
          equivalent for your agent) that tells your MCP client to launch <code>towline-mcp</code> with these flags:
        </p>
        <pre><code>{`TOWLINE_PORTAINER_TOKEN=<scoped-api-key> \\
towline-mcp \\
  -server https://portainer.example.com \\
  -stack my-project-dev \\
  -tier dev`}</code></pre>
        <p>
          The token is passed via the <code>TOWLINE_PORTAINER_TOKEN</code> environment variable (the config&apos;s{" "}
          <code>env</code> block), not on the command line. Prod-tier configs also pass{" "}
          <code>-approval-webhook &lt;url&gt;</code>, and <code>-skip-tls-verify</code> is added only when the user
          said Portainer uses a self-signed certificate.
        </p>
        <p>
          This MCP server process is your gateway to the Portainer environment. Every tool call you make goes
          through it, and it enforces:
        </p>
        <ul>
          <li><strong>Stack scoping</strong> — You can only see and modify the <code>my-project</code> stack. Other stacks are invisible.</li>
          <li><strong>Container ownership</strong> — Docker proxy calls are filtered to containers belonging to your stack,
          and only a small set of container lifecycle mutations is allowed (see <a href="#docker-proxy">Using the Docker Proxy</a>).</li>
          <li><strong>Compose security policy</strong> — compose files that could reach the host (privileged mode, host bind
          mounts, host networking, etc.) are rejected in every tier (see <a href="#compose-policy">Compose Security Policy</a>).</li>
          <li><strong>Tier gating</strong> — In <code>dev</code> tier, you have full autonomy. In <code>prod</code> tier,
          deploys, configuration changes, exec, and destructive operations require approval from a human via an external approval server.</li>
        </ul>

        <h2 id="tools-reference">MCP Tools Reference</h2>
        <p>You have access to two categories of tools: <strong>Towline tools</strong> (high-level, one-call answers)
        and <strong>upstream Portainer tools</strong> (stack CRUD and Docker proxy).</p>

        <h3 id="towline-tools">Towline Tools</h3>

        <h4><code>towline_service_health</code></h4>
        <p>Get health snapshot for one or all services in the stack.</p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>service</code></td><td>string</td><td>No</td><td>Service name from docker-compose. Omit for all services.</td></tr>
          </tbody>
        </table>
        <p>Returns: state, uptime, restart count, CPU/memory usage, exit code. If stats or inspect
        calls fail, <code>stats_error</code> or <code>inspect_error</code> fields will contain the error message.</p>

        <h4><code>towline_service_logs</code></h4>
        <p>Fetch recent log output for a named service.</p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>service</code></td><td>string</td><td>Yes</td><td>Service name from docker-compose</td></tr>
            <tr><td><code>tail</code></td><td>number</td><td>No</td><td>Lines to return (default 200)</td></tr>
            <tr><td><code>since</code></td><td>string</td><td>No</td><td>RFC3339 timestamp filter</td></tr>
            <tr><td><code>filter</code></td><td>string</td><td>No</td><td>Filter lines containing this string</td></tr>
          </tbody>
        </table>

        <h4><code>towline_env_get</code></h4>
        <p>Read stack environment variables. Sensitive values (names containing KEY, SECRET, PASSWORD, TOKEN) are masked.</p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>name</code></td><td>string</td><td>No</td><td>Specific variable name. Omit for all.</td></tr>
          </tbody>
        </table>

        <h4><code>towline_env_set</code></h4>
        <p>Set a stack environment variable. <strong>Requires approval in prod tier.</strong></p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>name</code></td><td>string</td><td>Yes</td><td>Variable name</td></tr>
            <tr><td><code>value</code></td><td>string</td><td>Yes</td><td>Variable value</td></tr>
            <tr><td><code>approvalToken</code></td><td>string</td><td>No</td><td>Token for prod tier approval</td></tr>
          </tbody>
        </table>

        <h4><code>towline_domains_list</code></h4>
        <p>List all domain/routing mappings for services in the stack. No parameters.</p>

        <h4><code>towline_domains_add</code></h4>
        <p>Add domain routing to a service. <strong>Requires approval in prod tier.</strong></p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>service</code></td><td>string</td><td>Yes</td><td>Service name</td></tr>
            <tr><td><code>domain</code></td><td>string</td><td>Yes</td><td>Domain name (e.g. app.example.com)</td></tr>
            <tr><td><code>port</code></td><td>number</td><td>No</td><td>Service port (default 80)</td></tr>
            <tr><td><code>method</code></td><td>string</td><td>No</td><td>traefik, caddy, or cloudflare (auto-detected if omitted)</td></tr>
            <tr><td><code>approvalToken</code></td><td>string</td><td>No</td><td>Token for prod tier approval</td></tr>
          </tbody>
        </table>

        <h4><code>towline_domains_remove</code></h4>
        <p>Remove domain routing from a service. <strong>Requires approval in prod tier.</strong></p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>service</code></td><td>string</td><td>Yes</td><td>Service name</td></tr>
            <tr><td><code>domain</code></td><td>string</td><td>Yes</td><td>Domain to remove</td></tr>
            <tr><td><code>method</code></td><td>string</td><td>No</td><td>traefik, caddy, or cloudflare (auto-detected if omitted)</td></tr>
            <tr><td><code>approvalToken</code></td><td>string</td><td>No</td><td>Token for prod tier approval</td></tr>
          </tbody>
        </table>

        <h4><code>towline_scale</code></h4>
        <p>Scale a service to N replicas. Warns if the service has persistent volumes. <strong>No approval required</strong> (operational).</p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>service</code></td><td>string</td><td>Yes</td><td>Service name</td></tr>
            <tr><td><code>replicas</code></td><td>number</td><td>Yes</td><td>Number of replicas</td></tr>
          </tbody>
        </table>

        <h4><code>towline_deployments</code></h4>
        <p>View deployment history with diffs and outcomes.</p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>limit</code></td><td>number</td><td>No</td><td>Max entries (default 10)</td></tr>
          </tbody>
        </table>

        <h4><code>towline_exec</code></h4>
        <p>Execute a command inside a running container and capture stdout/stderr. <strong>Requires approval in prod tier.</strong></p>
        <table>
          <thead><tr><th>Parameter</th><th>Type</th><th>Required</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>service</code></td><td>string</td><td>Yes</td><td>Service name</td></tr>
            <tr><td><code>command</code></td><td>string</td><td>Yes</td><td>Command to run (space-separated args)</td></tr>
            <tr><td><code>approvalToken</code></td><td>string</td><td>No</td><td>Token for prod tier approval</td></tr>
          </tbody>
        </table>

        <h3 id="upstream-tools">Upstream Portainer Tools</h3>
        <p>These are the standard Portainer MCP tools, scoped to your stack:</p>
        <table>
          <thead><tr><th>Tool</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>listLocalStacks</code></td><td>List stacks (filtered to yours)</td></tr>
            <tr><td><code>getLocalStackFile</code></td><td>Get compose file content</td></tr>
            <tr><td><code>createLocalStack</code></td><td>Deploy a new stack (compose security policy applies)</td></tr>
            <tr><td><code>updateLocalStack</code></td><td>Update stack (full compose required!). Omitting <code>env</code> keeps the current env vars. Recorded in deployment history.</td></tr>
            <tr><td><code>startLocalStack</code></td><td>Start a stopped stack</td></tr>
            <tr><td><code>stopLocalStack</code></td><td>Stop a running stack</td></tr>
            <tr><td><code>deleteLocalStack</code></td><td>Delete a stack. You can <code>createLocalStack</code> again afterwards without restarting the MCP server.</td></tr>
            <tr><td><code>dockerProxy</code></td><td>Raw Docker API proxy: reads plus limited container lifecycle mutations</td></tr>
          </tbody>
        </table>

        <h2 id="critical-warning">Critical: Partial Compose Files</h2>
        <p>
          <strong>When calling <code>updateLocalStack</code>, you MUST submit the COMPLETE compose file.</strong>{" "}
          Portainer removes any service not included in the update. If you only include the service you changed,
          all other services will be destroyed.
        </p>
        <p>Safe workflow:</p>
        <ol>
          <li>Call <code>getLocalStackFile</code> to get the current compose content</li>
          <li>Modify the YAML as needed</li>
          <li>Submit the entire modified file to <code>updateLocalStack</code></li>
        </ol>

        <h2 id="compose-policy">Compose Security Policy</h2>
        <p>
          In both dev and prod, <code>createLocalStack</code> and <code>updateLocalStack</code> reject compose files that use:
        </p>
        <ul>
          <li><code>privileged</code>, <code>cap_add</code>, <code>devices</code></li>
          <li><code>network_mode</code> host or <code>container:...</code>, <code>pid</code>, <code>ipc</code>, <code>uts</code>, <code>userns_mode</code>, <code>cgroup</code></li>
          <li><code>security_opt</code> with <code>unconfined</code> or <code>disable</code></li>
          <li>Host bind mounts: absolute, relative (<code>./</code>), <code>~</code>, or <code>{"${VAR}"}</code> sources, and long-syntax <code>type: bind</code></li>
          <li><code>volumes_from</code></li>
          <li>Volumes with <code>driver_opts</code> or a non-local driver</li>
          <li><code>secrets</code> / <code>configs</code> with <code>file:</code></li>
          <li><code>env_file</code> outside the stack directory</li>
          <li>Top-level <code>include</code> / <code>extends</code></li>
        </ul>
        <p>Named volumes and <code>tmpfs</code> are fine. If a compose file is rejected, fix it rather than working around the policy.</p>

        <h2 id="tier-system">Tier System &amp; Approval Flow</h2>
        <p>The <code>-tier</code> flag controls what you can do without human approval:</p>

        <h3>Dev Tier (<code>-tier dev</code>)</h3>
        <p>Full autonomy. Every tool call executes immediately. Use this for development and testing.</p>

        <h3>Prod Tier (<code>-tier prod</code>)</h3>
        <table>
          <thead><tr><th>Operation</th><th>Approval?</th><th>Examples</th></tr></thead>
          <tbody>
            <tr><td>Read</td><td>No</td><td>health, logs, env_get, domains_list, deployments, listLocalStacks</td></tr>
            <tr><td>Operational</td><td>No</td><td>startLocalStack, scale, dockerProxy container start/stop/restart</td></tr>
            <tr><td>Configuration</td><td><strong>Yes</strong></td><td>env_set, domains_add, domains_remove</td></tr>
            <tr><td>Deploy</td><td><strong>Yes</strong></td><td>createLocalStack, updateLocalStack</td></tr>
            <tr><td>Destructive</td><td><strong>Yes</strong></td><td>stopLocalStack, deleteLocalStack</td></tr>
            <tr><td>Exec / Docker write</td><td><strong>Yes</strong></td><td>towline_exec, other allowed dockerProxy mutations</td></tr>
          </tbody>
        </table>

        <h3>How Approval Works</h3>
        <p>
          Approval comes from a <strong>human</strong> via an external approval server configured with{" "}
          <code>-approval-webhook</code>. You cannot approve your own requests, and a &quot;yes&quot; in the chat does not approve anything.
        </p>
        <ol>
          <li>You call a tool that requires approval (e.g. <code>updateLocalStack</code> in prod)</li>
          <li><code>towline-mcp</code> sends the request to the approval server; the response contains an <code>approvalToken</code> (the request ID). Nothing has run yet.</li>
          <li>Tell the human what the change does, then wait until they tell you they have approved or rejected it in their approval system</li>
          <li>Re-call the same tool with identical arguments plus the <code>approvalToken</code> parameter</li>
          <li>If approved, the call executes. If still pending, nothing runs — wait for the human again; do not re-call in a loop. If rejected, the call is refused — do not retry without discussing it.</li>
        </ol>
        <p>
          Tokens are single-use, expire after 30 minutes, and are bound to the specific tool and arguments.
          You cannot reuse a token with different arguments. If no approval webhook is configured, prod operations
          that need approval are refused — ask the human to configure one or make the change themselves.
        </p>

        <h2 id="common-workflows">Common Agent Workflows</h2>

        <h3>Deploy a new service</h3>
        <pre><code>{`1. getLocalStackFile → get current compose
2. Add your new service to the YAML
3. updateLocalStack → deploy (submit FULL compose)
4. towline_service_health → verify it started
5. towline_service_logs → check for errors`}</code></pre>

        <h3>Debug a failing service</h3>
        <pre><code>{`1. towline_service_health → check state, restarts, exit code
2. towline_service_logs service=failing-svc tail=100 → read logs
3. towline_exec service=failing-svc command="cat /app/config.json" → inspect files
4. towline_env_get → check environment variables
5. Fix the issue, redeploy via updateLocalStack`}</code></pre>

        <h3>Expose a service on a domain</h3>
        <pre><code>{`1. towline_domains_add service=web domain=app.example.com port=3000
   (auto-detects Traefik/Caddy/Cloudflare)
2. towline_domains_list → verify routing
3. towline_service_health → ensure service is healthy`}</code></pre>

        <h3>Scale a service</h3>
        <pre><code>{`1. towline_scale service=worker replicas=3
2. towline_service_health → verify all replicas running`}</code></pre>

        <h3>Roll back a broken deploy</h3>
        <pre><code>{`1. towline_deployments → see history, find last working version
2. getLocalStackFile → get current (broken) compose
3. Revert to previous compose content
4. updateLocalStack → redeploy
5. towline_service_health → verify rollback`}</code></pre>

        <h2 id="cli-reference">CLI Reference</h2>
        <p>These are commands the <strong>human user</strong> runs (not MCP tools). You may need to instruct them to run these:</p>

        <h3><code>towline init &lt;project&gt;</code></h3>
        <p>Create a new project with full Portainer scaffolding.</p>
        <pre><code>{`towline init my-project --template api --tier dev

Flags:
  --template  Compose template or pack (default, web-app, api, ai-stack, n8n, or a custom pack)
  --tier      Deployment tier (dev or prod, default: dev)`}</code></pre>
        <p>Project names must match <code>{"^[a-z0-9][a-z0-9_-]{0,62}$"}</code>.</p>

        <h3><code>towline list</code></h3>
        <p>List all Towline projects on this machine.</p>

        <h3><code>towline destroy &lt;project&gt;</code></h3>
        <p>
          Remove a project: deletes the Portainer stack, service user, and team. Each ID in <code>towline.json</code> is
          verified against Portainer first and skipped on mismatch. Local files are kept.
        </p>

        <h3><code>towline setup</code></h3>
        <p>
          Interactive setup: connects to a Portainer instance (verifying its TLS certificate unless the user says it is
          self-signed), asks for an optional approval webhook URL for prod tier, and saves credentials.
        </p>

        <h2 id="project-structure">Generated Project Structure</h2>
        <pre><code>{`~/projects/<name>/
├── .mcp.json               # MCP server configuration (0600, gitignored)
├── .claude/settings.json   # Claude Code permissions
├── CLAUDE.md               # Agent instructions for this project
├── docker-compose.yml      # From template
├── .env.example            # Env var template
├── skills/                 # Agent skills library
└── .git/`}</code></pre>
        <p>
          The <code>.mcp.json</code> file tells your MCP client how to launch <code>towline-mcp</code>.
          The <code>CLAUDE.md</code> file contains project-specific instructions including available tools,
          the stack name, and operational guidelines.
        </p>

        <h2 id="docker-proxy">Using the Docker Proxy</h2>
        <p>
          The <code>dockerProxy</code> tool gives you raw Docker API access, scoped to your stack&apos;s containers.
          Use it for operations not covered by the Towline tools.
        </p>
        <ul>
          <li><code>dockerAPIPath</code> must be a plain path: query strings, fragments, <code>%</code>-encoding, backslashes,
          and <code>.</code>/<code>..</code> or empty segments are rejected. Pass query parameters via <code>queryParams</code>.
          API version prefixes like <code>/v1.43/</code> are normalized before scoping.</li>
          <li>The only mutating requests allowed are{" "}
          <code>{"POST /containers/{id}/{start|stop|restart|kill|pause|unpause|wait|resize|rename|update}"}</code> and{" "}
          <code>{"DELETE /containers/{id}"}</code>, on your stack&apos;s own containers.</li>
          <li>Container create, exec, archive upload, and anything on networks, volumes, images, system, etc. are rejected.
          Use <code>updateLocalStack</code> and <code>towline_exec</code> instead.</li>
        </ul>
        <pre><code>{`// List containers
dockerProxy method=GET dockerAPIPath=/containers/json queryParams=[{key: "all", value: "true"}]

// Inspect a container
dockerProxy method=GET dockerAPIPath=/containers/<id>/json

// Restart a container (no approval in prod — operational)
dockerProxy method=POST dockerAPIPath=/containers/<id>/restart

// Other allowed mutations (kill, pause, rename, update, DELETE, ...) require approval in prod`}</code></pre>

        <h2 id="environment-variables">Internal Environment Variables</h2>
        <p>
          Variables prefixed with <code>_TOWLINE_</code> are managed internally by Towline (e.g. deployment history).
          You cannot set or override them — they are silently filtered from <code>createLocalStack</code> and{" "}
          <code>updateLocalStack</code> requests, and the stack&apos;s own internal variables and deployment history are
          preserved on update. Omitting <code>env</code> from <code>updateLocalStack</code> keeps the stack&apos;s current
          variables. Every <code>updateLocalStack</code> and <code>towline_env_set</code> is recorded in{" "}
          <code>towline_deployments</code> (env changes record the variable name only, never the value).
        </p>

        <h2 id="prerequisites">Prerequisites</h2>
        <ul>
          <li>Portainer v2.28+ with API access</li>
          <li>Docker on the container host</li>
          <li>Network connectivity from the dev machine to the Portainer API</li>
          <li>An MCP-compatible agent (Claude Code, Cursor, Codex, Gemini CLI, Windsurf)</li>
        </ul>

        <h2 id="links">Links</h2>
        <ul>
          <li><a href="https://github.com/changethisusername/towline" target="_blank" rel="noopener noreferrer">GitHub Repository</a></li>
          <li><a href="https://github.com/changethisusername/towline/blob/main/docs/getting-started.md" target="_blank" rel="noopener noreferrer">Getting Started Guide</a></li>
          <li><a href="https://github.com/changethisusername/towline/blob/main/docs/user-guide.md" target="_blank" rel="noopener noreferrer">User Guide</a></li>
          <li><a href="https://portainer.io" target="_blank" rel="noopener noreferrer">Portainer</a></li>
        </ul>

        <div className="mt-16 rounded-xl border border-border bg-bg-card p-6 text-center">
          <p className="text-text-muted text-sm">
            This documentation is designed to be consumed by AI agents.
            For human-readable docs, see the{" "}
            <a href="https://github.com/changethisusername/towline/blob/main/docs/getting-started.md" target="_blank" rel="noopener noreferrer">
              Getting Started guide
            </a>.
          </p>
        </div>
      </main>
    </>
  );
}

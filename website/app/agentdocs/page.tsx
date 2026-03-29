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
          When your user runs <code>towline init my-project</code>, it creates a <code>.claude/settings.json</code> (or
          equivalent for your agent) that tells your MCP client to launch <code>towline-mcp</code> with these flags:
        </p>
        <pre><code>{`towline-mcp \\
  -server https://portainer.example.com \\
  -token <scoped-api-key> \\
  -stack my-project \\
  -tier dev`}</code></pre>
        <p>
          This MCP server process is your gateway to the Portainer environment. Every tool call you make goes
          through it, and it enforces:
        </p>
        <ul>
          <li><strong>Stack scoping</strong> — You can only see and modify the <code>my-project</code> stack. Other stacks are invisible.</li>
          <li><strong>Container ownership</strong> — Docker proxy calls are filtered to containers belonging to your stack.</li>
          <li><strong>Tier gating</strong> — In <code>dev</code> tier, you have full autonomy. In <code>prod</code> tier,
          mutations require an approval token.</li>
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
            <tr><td><code>createLocalStack</code></td><td>Deploy a new stack</td></tr>
            <tr><td><code>updateLocalStack</code></td><td>Update stack (full compose required!)</td></tr>
            <tr><td><code>startLocalStack</code></td><td>Start a stopped stack</td></tr>
            <tr><td><code>stopLocalStack</code></td><td>Stop a running stack</td></tr>
            <tr><td><code>deleteLocalStack</code></td><td>Delete a stack</td></tr>
            <tr><td><code>dockerProxy</code></td><td>Raw Docker API proxy (GET/POST/PUT/DELETE)</td></tr>
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

        <h2 id="tier-system">Tier System &amp; Approval Flow</h2>
        <p>The <code>-tier</code> flag controls what you can do without human approval:</p>

        <h3>Dev Tier (<code>-tier dev</code>)</h3>
        <p>Full autonomy. Every tool call executes immediately. Use this for development and testing.</p>

        <h3>Prod Tier (<code>-tier prod</code>)</h3>
        <table>
          <thead><tr><th>Operation</th><th>Approval?</th><th>Examples</th></tr></thead>
          <tbody>
            <tr><td>Read</td><td>No</td><td>health, logs, env_get, domains_list, deployments, listLocalStacks</td></tr>
            <tr><td>Operational</td><td>No</td><td>startLocalStack, scale</td></tr>
            <tr><td>Configuration</td><td><strong>Yes</strong></td><td>env_set, domains_add, domains_remove</td></tr>
            <tr><td>Deploy</td><td><strong>Yes</strong></td><td>createLocalStack, updateLocalStack</td></tr>
            <tr><td>Destructive</td><td><strong>Yes</strong></td><td>stopLocalStack, deleteLocalStack</td></tr>
            <tr><td>Exec / Docker write</td><td><strong>Yes</strong></td><td>towline_exec, dockerProxy non-GET</td></tr>
          </tbody>
        </table>

        <h3>How Approval Works</h3>
        <ol>
          <li>You call a tool that requires approval (e.g. <code>updateLocalStack</code> in prod)</li>
          <li>The response contains an <code>approvalToken</code> string and a message describing the action</li>
          <li>The human reviews and approves (the token is shown to them)</li>
          <li>You re-call the same tool with the same arguments plus the <code>approvalToken</code> parameter</li>
          <li>The call executes</li>
        </ol>
        <p>
          Tokens are single-use, expire after 5 minutes, and are bound to the specific tool and arguments.
          You cannot reuse a token with different arguments.
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
  --template  Template pack (api, fullstack, worker, static-site, etc.)
  --tier      Deployment tier (dev or prod, default: dev)`}</code></pre>

        <h3><code>towline list</code></h3>
        <p>List all Towline projects on this machine.</p>

        <h3><code>towline destroy &lt;project&gt;</code></h3>
        <p>Remove a project: deletes Portainer team, API key, stack, and local files.</p>

        <h3><code>towline setup</code></h3>
        <p>Interactive setup: connects to a Portainer instance and saves credentials.</p>

        <h2 id="project-structure">Generated Project Structure</h2>
        <pre><code>{`~/projects/<name>/
├── .claude/settings.json   # MCP server configuration
├── CLAUDE.md               # Agent instructions for this project
├── docker-compose.yml      # From template
├── .env.example            # Env var template
├── skills/                 # Agent skills library
└── .git/`}</code></pre>
        <p>
          The <code>.claude/settings.json</code> file tells your MCP client how to launch <code>towline-mcp</code>.
          The <code>CLAUDE.md</code> file contains project-specific instructions including available tools,
          the stack name, and operational guidelines.
        </p>

        <h2 id="docker-proxy">Using the Docker Proxy</h2>
        <p>
          The <code>dockerProxy</code> tool gives you raw Docker API access, scoped to your stack&apos;s containers.
          Use it for operations not covered by the Towline tools.
        </p>
        <pre><code>{`// List containers
dockerProxy method=GET dockerAPIPath=/containers/json

// Inspect a container
dockerProxy method=GET dockerAPIPath=/containers/<id>/json

// Restart a container (no approval in prod — operational)
dockerProxy method=POST dockerAPIPath=/containers/<id>/restart

// Non-GET requests to non-operational paths require approval in prod`}</code></pre>

        <h2 id="environment-variables">Internal Environment Variables</h2>
        <p>
          Variables prefixed with <code>_TOWLINE_</code> are managed internally by Towline (e.g. deployment history).
          You cannot set or override them — they are silently filtered from <code>createLocalStack</code> and{" "}
          <code>updateLocalStack</code> requests.
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

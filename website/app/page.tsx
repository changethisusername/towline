import Link from "next/link";

const REPO = "https://github.com/changethisusername/towline";

export default function Home() {
  return (
    <>
      {/* Nav */}
      <nav className="mx-auto flex h-14 max-w-2xl items-center justify-between px-6 text-[13px]">
        <span className="font-medium tracking-tight text-text">Towline</span>
        <div className="flex items-center gap-5 text-text-muted">
          <Link href="/agentdocs" className="hover:text-text-secondary transition-colors">
            Docs
          </Link>
          <a href={REPO} target="_blank" rel="noopener noreferrer" className="hover:text-text-secondary transition-colors">
            GitHub
          </a>
        </div>
      </nav>

      <main className="mx-auto max-w-2xl px-6 pt-16 pb-24 sm:pt-24">
        {/* What it is */}
        <h1 className="text-[1.75rem] font-semibold leading-[1.2] tracking-[-0.02em] text-text sm:text-[2rem]">
          Towline
        </h1>
        <p className="mt-3 text-[15px] leading-relaxed text-text-secondary">
          An open-source CLI and MCP server that lets AI coding agents (Claude Code, Cursor, Codex, etc.)
          deploy and operate Docker containers on{" "}
          <a href="https://portainer.io" target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-text transition-colors">Portainer</a>-managed infrastructure.
          Each project gets an isolated stack, scoped API credentials, and a set of MCP tools
          for health checks, logs, env vars, domain routing, scaling, and exec.
        </p>

        {/* Install */}
        <div className="mt-8 rounded-md border border-border bg-code-bg px-4 py-3">
          <code className="text-[13px] text-text-secondary">
            <span className="select-none text-text-muted">$ </span>
            <span className="text-text">curl -fsSL towline.dev/install | sh</span>
          </code>
        </div>

        {/* Who it's for */}
        <h2 className="mt-12 text-[15px] font-semibold text-text">Who this is for</h2>
        <p className="mt-2 text-[14px] leading-relaxed text-text-secondary">
          You run Portainer (self-hosted or cloud) and want AI agents to deploy containers
          without giving them admin access to your entire infrastructure.
          Towline creates a per-project boundary: dedicated Portainer team, scoped API key,
          single-stack MCP server. Even if the agent misbehaves, Portainer rejects
          anything outside its assigned scope.
        </p>

        {/* What you get */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">What a project includes</h2>
        <p className="mt-2 text-[14px] leading-relaxed text-text-secondary">
          Running <code className="text-[12px] bg-code-bg border border-border px-1.5 py-0.5 rounded">towline init my-app</code> creates:
        </p>
        <ul className="mt-2 space-y-1.5 text-[14px] leading-relaxed text-text-secondary list-disc pl-5">
          <li>A Portainer team and API key scoped to one stack</li>
          <li>A Docker Compose stack deployed on your chosen environment</li>
          <li>An MCP server config (<code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">.claude/settings.json</code>) pointing to that stack</li>
          <li>A project CLAUDE.md with tool documentation for the agent</li>
          <li>A git repo with compose file and env template</li>
        </ul>
        <p className="mt-3 text-[14px] leading-relaxed text-text-secondary">
          The agent then has 10 MCP tools: <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_service_health</code>,{" "}
          <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_service_logs</code>,{" "}
          <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_env_get/set</code>,{" "}
          <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_domains_add/remove/list</code>,{" "}
          <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_scale</code>,{" "}
          <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_deployments</code>,{" "}
          <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline_exec</code>{" "}
          — plus the upstream Portainer MCP tools for stack CRUD and Docker proxy.
          Each tool answers one operational question in a single call.
        </p>

        {/* Tier system */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">Dev and prod tiers</h2>
        <p className="mt-2 text-[14px] leading-relaxed text-text-secondary">
          In <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">dev</code> tier,
          the agent has full autonomy — every tool call executes immediately.
          In <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">prod</code> tier,
          reads and operational actions (health, logs, start, scale) are instant,
          but mutations (deploy, env changes, delete, exec) require a single-use approval token.
          The token is bound to the specific tool and arguments — it can&apos;t be reused with different parameters.
        </p>

        {/* Quick start */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">Quick start</h2>
        <div className="mt-3 rounded-md border border-border bg-code-bg px-4 py-3.5">
          <pre className="text-[13px] leading-7 text-text-secondary overflow-x-auto"><code>{`curl -fsSL towline.dev/install | sh
towline setup                        # connect to Portainer
towline init my-app --template api   # create project
cd my-app && claude                  # agent deploys`}</code></pre>
        </div>

        {/* Requirements */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">Requirements</h2>
        <ul className="mt-2 space-y-1.5 text-[14px] leading-relaxed text-text-secondary list-disc pl-5">
          <li>Portainer v2.28+ with API access</li>
          <li>Docker on the container host</li>
          <li>An MCP-compatible agent (Claude Code, Cursor, Codex, Gemini CLI, Windsurf)</li>
        </ul>

        {/* Links */}
        <div className="mt-12 flex flex-wrap gap-x-6 gap-y-2 text-[13px] text-text-muted">
          <Link href="/agentdocs" className="underline underline-offset-2 hover:text-text-secondary transition-colors">
            Agent documentation
          </Link>
          <a href={`${REPO}/blob/main/docs/getting-started.md`} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-text-secondary transition-colors">
            Getting started guide
          </a>
          <a href={`${REPO}/blob/main/docs/user-guide.md`} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-text-secondary transition-colors">
            User guide
          </a>
          <a href={REPO} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-text-secondary transition-colors">
            Source code
          </a>
        </div>
      </main>

      {/* Footer */}
      <footer className="mx-auto max-w-2xl border-t border-border px-6 py-5">
        <div className="flex items-center justify-between text-[12px] text-text-muted">
          <span>Apache 2.0</span>
          <a href={REPO} target="_blank" rel="noopener noreferrer" className="hover:text-text-secondary transition-colors">
            changethisusername/towline
          </a>
        </div>
      </footer>
    </>
  );
}

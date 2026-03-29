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
          An open-source toolkit that lets AI coding agents (Claude Code, Cursor, Codex, etc.)
          deploy and operate Docker containers on{" "}
          <a href="https://portainer.io" target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-text transition-colors">Portainer</a>-managed infrastructure.
          It consists of four parts:
        </p>
        <ol className="mt-3 space-y-1.5 text-[14px] leading-relaxed text-text-secondary list-decimal pl-5">
          <li><strong className="text-text">towline CLI</strong> — scaffolds a new project: creates a Portainer team, scoped API key, container stack, agent config, compose files, and git repo.</li>
          <li><strong className="text-text">towline-mcp</strong> — an MCP server (Go binary) that runs per-project, giving the agent 10 operational tools (health, logs, env vars, domains, scale, exec) plus upstream Portainer stack CRUD and Docker proxy, all scoped to a single stack.</li>
          <li><strong className="text-text">DevOps skill</strong> — a markdown file (<code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">skills/towline-devops.md</code>) installed into every project that teaches the agent how to use the tools: deployment workflows, debugging patterns, rollback procedures, and the tier/approval model.</li>
          <li><strong className="text-text">Template packs</strong> — bundles of compose files, domain-specific agent skills, and additional MCP configs. The <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">ai-stack</code> pack ships Ollama + Open WebUI with an AI operations skill; the <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">n8n</code> pack ships n8n + PostgreSQL with a workflow operations skill. Compose templates (<code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">api</code>, <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">web-app</code>) are also available without pack-level skills.</li>
        </ol>

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

        {/* What a project looks like */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">What a project looks like</h2>
        <div className="mt-3 rounded-md border border-border bg-code-bg px-4 py-3.5">
          <pre className="text-[13px] leading-6 text-text-secondary overflow-x-auto"><code>{`~/projects/my-app/
├── .claude/settings.json   # MCP config → launches towline-mcp
├── CLAUDE.md               # Agent instructions for this project
├── docker-compose.yml      # From template or pack
├── .env.example
├── skills/
│   ├── towline-devops.md   # Base operational skill
│   └── ai-ops.md           # Pack skill (if using ai-stack)
└── .git/`}</code></pre>
        </div>
        <p className="mt-3 text-[14px] leading-relaxed text-text-secondary">
          The skills are not documentation — they are agent training. The DevOps skill teaches
          deployment workflows, debugging procedures, and how to use the approval system.
          Pack skills add domain knowledge on top (e.g., the ai-stack skill teaches how to
          pull Ollama models and manage Open WebUI).
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

        {/* Step by step */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">Step by step</h2>

        <h3 className="mt-4 text-[14px] font-semibold text-text">1. Install</h3>
        <div className="mt-2 rounded-md border border-border bg-code-bg px-4 py-3">
          <code className="text-[13px] text-text-secondary">
            <span className="select-none text-text-muted">$ </span>
            <span className="text-text">curl -fsSL towline.dev/install | sh</span>
          </code>
        </div>
        <p className="mt-2 text-[13px] text-text-muted">
          Installs two binaries: <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline</code> (CLI) and <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline-mcp</code> (MCP server) to /usr/local/bin.
          Run <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline help</code> to see all commands.
        </p>

        <h3 className="mt-6 text-[14px] font-semibold text-text">2. Connect to Portainer</h3>
        <div className="mt-2 rounded-md border border-border bg-code-bg px-4 py-3">
          <code className="text-[13px] text-text-secondary">
            <span className="select-none text-text-muted">$ </span>
            <span className="text-text">towline setup</span>
          </code>
        </div>
        <p className="mt-2 text-[13px] text-text-muted">
          Interactive wizard. Asks for your Portainer URL, admin API key, and default environment ID.
          Saves to <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">~/.towline/config.yaml</code>. Only needed once per machine.
        </p>

        <h3 className="mt-6 text-[14px] font-semibold text-text">3. Create a project</h3>
        <div className="mt-2 rounded-md border border-border bg-code-bg px-4 py-3.5">
          <pre className="text-[13px] leading-7 text-text-secondary overflow-x-auto"><code>{`# Simple API project
towline init my-api --template api

# AI stack with Ollama + Open WebUI (includes AI ops skill)
towline init my-ai --pack ai-stack

# n8n automation (includes workflow ops skill)
towline init my-workflows --pack n8n`}</code></pre>
        </div>
        <p className="mt-2 text-[13px] text-text-muted">
          This creates the Portainer team, API key, and stack, writes the agent configuration,
          installs skills, and initializes git. The project directory is ready for your agent.
        </p>

        <h3 className="mt-6 text-[14px] font-semibold text-text">4. Start your agent</h3>
        <div className="mt-2 rounded-md border border-border bg-code-bg px-4 py-3">
          <code className="text-[13px] text-text-secondary">
            <span className="select-none text-text-muted">$ </span>
            <span className="text-text">cd my-api && claude</span>
          </code>
        </div>
        <p className="mt-2 text-[13px] text-text-muted">
          The agent reads <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">.claude/settings.json</code> which
          launches <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">towline-mcp</code> automatically.
          It picks up the DevOps skill from <code className="text-[12px] bg-code-bg border border-border px-1 py-0.5 rounded">CLAUDE.md</code>,
          gets access to the MCP tools, and can deploy, debug, scale, and operate within the isolated stack.
          For non-Claude agents, the MCP config is also generated for Cursor, Gemini CLI, etc.
        </p>

        <h3 className="mt-6 text-[14px] font-semibold text-text">5. Manage projects</h3>
        <div className="mt-2 rounded-md border border-border bg-code-bg px-4 py-3.5">
          <pre className="text-[13px] leading-7 text-text-secondary overflow-x-auto"><code>{`towline list                 # see all projects
towline status my-api        # stack state, tier, endpoint
towline promote my-api       # move from dev to prod tier
towline rotate-keys my-api   # rotate API key
towline destroy my-api       # tear down everything`}</code></pre>
        </div>

        {/* Requirements */}
        <h2 className="mt-10 text-[15px] font-semibold text-text">Requirements</h2>
        <ul className="mt-2 space-y-1.5 text-[14px] leading-relaxed text-text-secondary list-disc pl-5">
          <li>Portainer v2.28+ with API access (self-hosted or Portainer Cloud)</li>
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

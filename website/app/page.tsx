import Link from "next/link";

const REPO_URL = "https://github.com/changethisusername/towline";

function Nav() {
  return (
    <nav className="fixed top-0 left-0 right-0 z-50 border-b border-border bg-bg/80 backdrop-blur-xl">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-6">
        <Link href="/" className="flex items-center gap-2 font-semibold tracking-tight text-text">
          <span className="text-accent">&#9650;</span> Towline
        </Link>
        <div className="flex items-center gap-6 text-sm">
          <Link href="#features" className="text-text-secondary hover:text-text transition-colors">
            Features
          </Link>
          <Link href="#how-it-works" className="text-text-secondary hover:text-text transition-colors">
            How it works
          </Link>
          <Link href="/agentdocs" className="text-text-secondary hover:text-text transition-colors">
            Agent Docs
          </Link>
          <a
            href={REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-text-secondary hover:text-text hover:border-border-bright transition-all"
          >
            <svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" /></svg>
            GitHub
          </a>
        </div>
      </div>
    </nav>
  );
}

function Hero() {
  return (
    <section className="relative overflow-hidden pt-32 pb-24">
      {/* Glow */}
      <div className="pointer-events-none absolute top-0 left-1/2 -translate-x-1/2 w-[800px] h-[500px] rounded-full bg-accent/5 blur-[120px]" />

      <div className="relative mx-auto max-w-4xl px-6 text-center">
        <div className="mb-6 inline-flex items-center gap-2 rounded-full border border-border px-4 py-1.5 text-xs tracking-wide text-text-secondary">
          <span className="inline-block h-1.5 w-1.5 rounded-full bg-accent" />
          Built on Portainer &middot; Open Source &middot; Apache 2.0
        </div>

        <h1 className="text-5xl font-bold leading-[1.08] tracking-[-0.03em] sm:text-6xl md:text-7xl">
          One command.{" "}
          <span className="bg-gradient-to-r from-accent to-emerald-300 bg-clip-text text-transparent">
            Your AI agent deploys.
          </span>
        </h1>

        <p className="mx-auto mt-6 max-w-2xl text-lg leading-relaxed text-text-secondary">
          Towline gives your AI agent a complete, isolated environment to build,
          deploy, and operate software on Portainer — in under 60 seconds.
        </p>

        {/* Install command */}
        <div className="mx-auto mt-10 max-w-lg">
          <div className="terminal">
            <div className="terminal-header">
              <div className="terminal-dot bg-[#ff5f57]" />
              <div className="terminal-dot bg-[#febc2e]" />
              <div className="terminal-dot bg-[#28c840]" />
              <span className="ml-2 text-xs text-text-muted">Terminal</span>
            </div>
            <div className="terminal-body">
              <span className="text-text-muted">$</span>{" "}
              <span className="text-accent">curl</span>{" "}
              <span className="text-text-secondary">-fsSL</span>{" "}
              <span className="text-text">towline.dev/install</span>{" "}
              <span className="text-text-secondary">|</span>{" "}
              <span className="text-accent">sh</span>
            </div>
          </div>
          <p className="mt-3 text-xs text-text-muted">
            Installs <code className="text-accent/80">towline</code> and{" "}
            <code className="text-accent/80">towline-mcp</code> to /usr/local/bin
          </p>
        </div>

        {/* CTA buttons */}
        <div className="mt-8 flex items-center justify-center gap-4">
          <a
            href="#how-it-works"
            className="rounded-lg bg-accent px-5 py-2.5 text-sm font-medium text-black transition-colors hover:bg-accent-hover"
          >
            Get started
          </a>
          <Link
            href="/agentdocs"
            className="rounded-lg border border-border px-5 py-2.5 text-sm font-medium text-text-secondary transition-all hover:text-text hover:border-border-bright"
          >
            Agent documentation
          </Link>
        </div>
      </div>
    </section>
  );
}

function DemoTerminal() {
  return (
    <section className="relative py-20">
      <div className="mx-auto max-w-3xl px-6">
        <div className="terminal shadow-2xl shadow-accent/5">
          <div className="terminal-header">
            <div className="terminal-dot bg-[#ff5f57]" />
            <div className="terminal-dot bg-[#febc2e]" />
            <div className="terminal-dot bg-[#28c840]" />
            <span className="ml-2 text-xs text-text-muted">~/projects</span>
          </div>
          <div className="terminal-body text-[13px] leading-7">
            <p><span className="text-text-muted">$</span> <span className="text-accent">towline init</span> my-saas <span className="text-text-secondary">--template api</span></p>
            <p className="text-text-muted mt-1">&nbsp; Creating Portainer team &quot;my-saas&quot;...</p>
            <p className="text-text-muted">&nbsp; Generating scoped API key...</p>
            <p className="text-text-muted">&nbsp; Deploying stack &quot;my-saas&quot;...</p>
            <p className="text-text-muted">&nbsp; Writing .claude/settings.json...</p>
            <p className="text-text-muted">&nbsp; Writing CLAUDE.md...</p>
            <p className="text-text-muted">&nbsp; Initializing git repo...</p>
            <p className="text-emerald-400 mt-1">&nbsp; &#10003; Project ready in 12s</p>
            <p className="text-text-muted mt-3"><span className="text-text-muted">$</span> <span className="text-text">cd my-saas && claude</span></p>
            <p className="mt-1 text-text-secondary">&nbsp; You: &quot;Build a REST API with auth, deploy it, and expose it on api.mylab.local&quot;</p>
            <p className="mt-1 text-text-muted">&nbsp; Agent: writes code → deploys services → configures routing</p>
            <p className="text-text-muted">&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp; → checks health → iterates on errors → ships it</p>
          </div>
        </div>
      </div>
    </section>
  );
}

const features = [
  {
    title: "Stack isolation",
    description:
      "Every project gets its own Portainer team, scoped API key, and isolated container stack. Even if middleware fails, Portainer rejects cross-project requests.",
    icon: (
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="11" width="18" height="11" rx="2" ry="2" /><path d="M7 11V7a5 5 0 0110 0v4" /></svg>
    ),
  },
  {
    title: "10 high-level tools",
    description:
      "Health checks, logs, env vars, domain routing, scaling, deployments, exec — each answers a developer question in one MCP call. No raw Docker API needed.",
    icon: (
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><polyline points="16 18 22 12 16 6" /><polyline points="8 6 2 12 8 18" /></svg>
    ),
  },
  {
    title: "Tier-based gating",
    description:
      "Dev tier: full autonomy. Prod tier: reads are instant, mutations require approval. Configurable via webhook for Telegram, Slack, or ntfy.sh.",
    icon: (
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></svg>
    ),
  },
  {
    title: "Agent-agnostic",
    description:
      "Works with any MCP-compatible tool: Claude Code, Cursor, Codex, Gemini CLI, Windsurf. Standard MCP protocol, no vendor lock-in.",
    icon: (
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10" /><line x1="2" y1="12" x2="22" y2="12" /><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z" /></svg>
    ),
  },
  {
    title: "Self-hosted",
    description:
      "No cloud dependencies. Runs wherever Portainer runs — your infrastructure, your data, your rules. Apache 2.0 licensed.",
    icon: (
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="2" width="20" height="8" rx="2" ry="2" /><rect x="2" y="14" width="20" height="8" rx="2" ry="2" /><line x1="6" y1="6" x2="6.01" y2="6" /><line x1="6" y1="18" x2="6.01" y2="18" /></svg>
    ),
  },
  {
    title: "Template packs",
    description:
      "Start with api, fullstack, worker, static-site, or database templates. Each bundles compose files, agent skills, and MCP configs.",
    icon: (
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z" /></svg>
    ),
  },
];

function Features() {
  return (
    <section id="features" className="py-24">
      <div className="mx-auto max-w-6xl px-6">
        <div className="text-center">
          <h2 className="text-3xl font-bold tracking-[-0.02em] sm:text-4xl">
            Everything an agent needs to ship
          </h2>
          <p className="mx-auto mt-4 max-w-xl text-text-secondary">
            Towline bridges the gap between &quot;agent wrote the code&quot; and
            &quot;it&apos;s running in production.&quot;
          </p>
        </div>

        <div className="mt-16 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((f) => (
            <div
              key={f.title}
              className="group rounded-xl border border-border bg-bg-card p-6 transition-all hover:border-border-bright hover:bg-bg-card-hover"
            >
              <div className="mb-4 flex h-10 w-10 items-center justify-center rounded-lg bg-accent-dim text-accent">
                {f.icon}
              </div>
              <h3 className="text-base font-semibold">{f.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-text-secondary">
                {f.description}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function HowItWorks() {
  const steps = [
    {
      num: "01",
      title: "Install Towline",
      code: "curl -fsSL towline.dev/install | sh",
      desc: "Two binaries: towline (CLI) and towline-mcp (MCP server). ~10MB total.",
    },
    {
      num: "02",
      title: "Connect to Portainer",
      code: "towline setup",
      desc: "Point to your Portainer instance. Saves config to ~/.towline/config.yaml.",
    },
    {
      num: "03",
      title: "Init a project",
      code: "towline init my-project --template api",
      desc: "Creates team, API key, stack, agent config, compose file, and git repo.",
    },
    {
      num: "04",
      title: "Let your agent ship",
      code: "cd my-project && claude",
      desc: "Your agent can now deploy, debug, scale, and operate — fully isolated.",
    },
  ];

  return (
    <section id="how-it-works" className="py-24 border-t border-border">
      <div className="mx-auto max-w-3xl px-6">
        <h2 className="text-center text-3xl font-bold tracking-[-0.02em] sm:text-4xl">
          Idea to agent-deployed in 60 seconds
        </h2>
        <p className="mx-auto mt-4 max-w-lg text-center text-text-secondary">
          Four commands. That&apos;s it.
        </p>

        <div className="mt-16 space-y-10">
          {steps.map((s) => (
            <div key={s.num} className="flex gap-6">
              <div className="flex-shrink-0 pt-1">
                <span className="inline-block text-xs font-mono font-medium text-accent tracking-wider">
                  {s.num}
                </span>
              </div>
              <div className="flex-1">
                <h3 className="text-lg font-semibold">{s.title}</h3>
                <div className="mt-2 rounded-lg bg-code-bg border border-border px-4 py-2.5 font-mono text-sm text-text-secondary">
                  <span className="text-text-muted">$ </span>
                  {s.code}
                </div>
                <p className="mt-2 text-sm text-text-muted">{s.desc}</p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function ToolsTable() {
  const tools = [
    { name: "towline_service_health", desc: "Container state, CPU, memory, uptime, restarts", cat: "Read" },
    { name: "towline_service_logs", desc: "Fetch and filter service log output", cat: "Read" },
    { name: "towline_env_get", desc: "Read stack env vars (sensitive values masked)", cat: "Read" },
    { name: "towline_env_set", desc: "Set stack env vars (approval in prod)", cat: "Config" },
    { name: "towline_domains_list", desc: "List domain/routing mappings", cat: "Read" },
    { name: "towline_domains_add", desc: "Add domain routing via Traefik/Caddy/Cloudflare", cat: "Config" },
    { name: "towline_domains_remove", desc: "Remove domain routing", cat: "Config" },
    { name: "towline_scale", desc: "Scale service replicas", cat: "Ops" },
    { name: "towline_deployments", desc: "View deployment history with diffs", cat: "Read" },
    { name: "towline_exec", desc: "Run command in container, capture output", cat: "Exec" },
  ];

  const catColors: Record<string, string> = {
    Read: "text-emerald-400 bg-emerald-400/10",
    Config: "text-amber-400 bg-amber-400/10",
    Ops: "text-sky-400 bg-sky-400/10",
    Exec: "text-rose-400 bg-rose-400/10",
  };

  return (
    <section className="py-24 border-t border-border">
      <div className="mx-auto max-w-4xl px-6">
        <h2 className="text-center text-3xl font-bold tracking-[-0.02em] sm:text-4xl">
          MCP tools your agent gets
        </h2>
        <p className="mx-auto mt-4 max-w-lg text-center text-text-secondary">
          Plus upstream Portainer tools for stack CRUD and Docker proxy.
        </p>

        <div className="mt-12 overflow-hidden rounded-xl border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-bg-card">
                <th className="px-4 py-3 text-left font-medium text-text-secondary">Tool</th>
                <th className="px-4 py-3 text-left font-medium text-text-secondary hidden sm:table-cell">Description</th>
                <th className="px-4 py-3 text-right font-medium text-text-secondary">Category</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((t) => (
                <tr key={t.name} className="border-b border-border last:border-0 hover:bg-bg-card/50 transition-colors">
                  <td className="px-4 py-3 font-mono text-xs text-accent">{t.name}</td>
                  <td className="px-4 py-3 text-text-secondary hidden sm:table-cell">{t.desc}</td>
                  <td className="px-4 py-3 text-right">
                    <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${catColors[t.cat]}`}>
                      {t.cat}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function Footer() {
  return (
    <footer className="border-t border-border py-12">
      <div className="mx-auto max-w-6xl px-6">
        <div className="flex flex-col items-center justify-between gap-6 sm:flex-row">
          <div className="flex items-center gap-2 text-sm text-text-muted">
            <span className="text-accent">&#9650;</span> Towline &mdash; Agentic
            Engineering SDK for Portainer
          </div>
          <div className="flex items-center gap-6 text-sm text-text-muted">
            <a href={REPO_URL} target="_blank" rel="noopener noreferrer" className="hover:text-text-secondary transition-colors">
              GitHub
            </a>
            <Link href="/agentdocs" className="hover:text-text-secondary transition-colors">
              Agent Docs
            </Link>
            <a href={`${REPO_URL}/blob/main/LICENSE`} target="_blank" rel="noopener noreferrer" className="hover:text-text-secondary transition-colors">
              Apache 2.0
            </a>
          </div>
        </div>
      </div>
    </footer>
  );
}

export default function Home() {
  return (
    <>
      <Nav />
      <main>
        <Hero />
        <DemoTerminal />
        <Features />
        <HowItWorks />
        <ToolsTable />
      </main>
      <Footer />
    </>
  );
}

package main

import (
	"fmt"
	"os"

	"github.com/changethisusername/towline/internal/cli"
)

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	if os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		printHelp()
		return
	}

	if os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Printf("towline %s (%s, %s)\n", Version, Commit, BuildDate)
		return
	}

	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	v := Version
	if v == "" {
		v = "dev"
	}
	fmt.Printf(`towline %s — agentic engineering SDK for Portainer

Usage: towline <command> [flags]

Getting started:
  1. towline setup             Connect to your Portainer instance
  2. towline init <project>    Create a new project (team, API key, stack, agent config)
  3. cd <project> && claude     Start your agent — it can now deploy

Commands:
  setup          Connect to a Portainer instance (saves to ~/.towline/config.yaml)
  init <name>    Create a project: Portainer team, scoped API key, container stack,
                 MCP config, DevOps skill, compose file, and git repo
                 Flags: --template <name>  Compose template (api, web-app, default)
                        --tier <dev|prod>  Deployment tier (default: dev)
                        --dir <path>       Add Towline to an existing codebase
  list           List all Towline projects on this machine
  destroy <name> Remove a project: deletes team, API key, stack, and local files
  promote <name> Promote a project from dev to prod tier
  rotate-keys    Rotate the Portainer API key for a project
  status <name>  Show project status (stack state, tier, endpoint)
  help           Show this help message
  version        Show version information

What your agent gets:
  After 'towline init', the project directory contains:
  - .claude/settings.json    MCP config that launches towline-mcp
  - CLAUDE.md                Agent instructions with tool reference
  - skills/towline-devops.md Operational skill (deployment, debugging, rollback)
  - docker-compose.yml       From template or pack

  The MCP server provides 10 tools: service_health, service_logs,
  env_get, env_set, domains_list, domains_add, domains_remove,
  scale, deployments, exec — plus Portainer stack CRUD and Docker proxy.

  Dev tier: full agent autonomy. Prod tier: mutations need approval.

Docs: https://towline.dev/agentdocs
Code: https://github.com/changethisusername/towline
`, v)
}

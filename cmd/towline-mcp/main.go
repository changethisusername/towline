package main

import (
	"flag"
	"os"
	"strings"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/gateway"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/internal/towlinemcp"
	"github.com/rs/zerolog/log"
)

const (
	defaultToolsPath        = "tools.yaml"
	defaultTowlineToolsPath = "towline-tools.yaml"

	// tokenEnvVar supplies the Portainer API token without exposing it in
	// the process argument list (visible to every local user via ps).
	tokenEnvVar = "TOWLINE_PORTAINER_TOKEN"
	// approvalAuthEnvVar supplies an optional bearer token for the approval webhook.
	approvalAuthEnvVar = "TOWLINE_APPROVAL_WEBHOOK_TOKEN"
)

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	// "towline-mcp gateway" runs the remote MCP gateway; anything else is
	// the stdio server an agent spawns.
	if len(os.Args) > 1 && os.Args[1] == "gateway" {
		gateway.Version = Version
		if err := gateway.Main(os.Args[2:]); err != nil {
			log.Fatal().Err(err).Msg("gateway failed")
		}
		return
	}

	log.Info().
		Str("version", Version).
		Str("build-date", BuildDate).
		Str("commit", Commit).
		Msg("Towline MCP server")

	// Upstream flags
	serverFlag := flag.String("server", "", "The Portainer server URL")
	tokenFlag := flag.String("token", "", "The authentication token for the Portainer server (prefer the "+tokenEnvVar+" environment variable)")
	toolsFlag := flag.String("tools", "", "The path to the tools YAML file")
	readOnlyFlag := flag.Bool("read-only", false, "Run in read-only mode")
	disableVersionCheckFlag := flag.Bool("disable-version-check", true, "Disable Portainer server version check")

	// Towline flags
	stackFlag := flag.String("stack", "", "Stack name to scope this MCP instance to")
	tierFlag := flag.String("tier", "", "Deployment tier: dev or prod")
	proxyFlag := flag.String("proxy", "", "Proxy backend: traefik, caddy, or cloudflare (auto-detected if omitted)")
	caddyAPIFlag := flag.String("caddy-api", "http://localhost:2019", "Caddy admin API URL")
	skipTLSVerifyFlag := flag.Bool("skip-tls-verify", false, "Skip TLS certificate verification (for self-signed certs)")
	approvalWebhookFlag := flag.String("approval-webhook", "", "Approval server URL used in human approval mode")
	approvalModeFlag := flag.String("approval-mode", "", "Who approves prod operations: human (approval server) or agent (the agent confirms by re-calling). Default: human if -approval-webhook is set, else agent")
	composePolicyFlag := flag.String("compose-policy", "enforce", "Compose security policy for stack create/update: enforce or off")
	allowBindMountsFlag := flag.String("allow-bind-mounts", "", "Comma-separated host paths that stacks may bind mount (\".\" allows paths inside the stack directory)")
	allowNetworksFlag := flag.String("allow-networks", "", "Comma-separated external network names that stacks may join (host, none and bridge are never allowed)")
	allowVolumesFlag := flag.String("allow-volumes", "", "Comma-separated external or named volumes that stacks may mount")

	flag.Parse()

	token := *tokenFlag
	if token == "" {
		token = os.Getenv(tokenEnvVar)
	}
	if *serverFlag == "" || token == "" {
		log.Fatal().Msg("-server and a token (" + tokenEnvVar + " or -token) are required")
	}
	if *stackFlag == "" || *tierFlag == "" {
		log.Fatal().Msg("Both -stack and -tier flags are required")
	}

	tier := middleware.Tier(*tierFlag)
	if tier != middleware.TierDev && tier != middleware.TierProd {
		log.Fatal().Msg("-tier must be 'dev' or 'prod'")
	}

	approvalMode, err := middleware.ResolveApprovalMode(*approvalModeFlag, *approvalWebhookFlag != "")
	if err != nil {
		log.Fatal().Err(err).Msg("invalid -approval-mode")
	}

	composePolicy := middleware.ComposePolicy{StackName: *stackFlag}
	switch *composePolicyFlag {
	case "enforce":
	case "off":
		composePolicy.Disabled = true
		log.Warn().Msg("compose security policy is disabled for this stack")
	default:
		log.Fatal().Msg("-compose-policy must be 'enforce' or 'off'")
	}
	composePolicy.AllowBindMounts = splitList(*allowBindMountsFlag)
	composePolicy.AllowNetworks = splitList(*allowNetworksFlag)
	composePolicy.AllowVolumes = splitList(*allowVolumesFlag)

	toolsPath := *toolsFlag
	if toolsPath == "" {
		toolsPath = defaultToolsPath
	}

	var approver approval.Approver
	if approvalMode == middleware.ApprovalHuman {
		if *approvalWebhookFlag != "" {
			webhook, err := approval.NewWebhook(*approvalWebhookFlag, os.Getenv(approvalAuthEnvVar))
			if err != nil {
				log.Fatal().Err(err).Msg("invalid -approval-webhook")
			}
			approver = webhook
		} else if tier == middleware.TierProd {
			log.Warn().Msg("human approval mode without -approval-webhook: prod operations that need approval will be refused")
		}
	} else if tier == middleware.TierProd {
		log.Warn().Msg("agent approval mode: the agent confirms its own prod operations (run 'towline approvals setup' for human approval)")
	}

	log.Info().
		Str("portainer-host", *serverFlag).
		Str("stack", *stackFlag).
		Str("tier", *tierFlag).
		Msg("starting Towline MCP server")

	inst, err := towlinemcp.Build(towlinemcp.Options{
		ServerURL:           *serverFlag,
		Token:               token,
		ToolsPath:           toolsPath,
		TowlineToolsPath:    defaultTowlineToolsPath,
		ReadOnly:            *readOnlyFlag,
		DisableVersionCheck: *disableVersionCheckFlag,
		SkipTLSVerify:       *skipTLSVerifyFlag,
		Stack:               *stackFlag,
		Tier:                tier,
		Proxy:               *proxyFlag,
		CaddyAPI:            *caddyAPIFlag,
		ApprovalMode:        approvalMode,
		Approver:            approver,
		ComposePolicy:       composePolicy,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create server")
	}

	err = inst.Server.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}

	inst.Close()
}

// splitList splits a comma-separated flag value, dropping empty items.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

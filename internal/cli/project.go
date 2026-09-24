package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/changethisusername/towline/pkg/config"
)

// newAdminAPI returns a Portainer client authenticated with the admin key.
func newAdminAPI(cfg *config.GlobalConfig) *config.PortainerAPI {
	skip, legacy := cfg.InsecureTLS()
	if legacy {
		fmt.Fprintln(os.Stderr, "Warning: your ~/.towline/config.yaml predates TLS verification, so Portainer's certificate is not verified (as in earlier releases).")
		fmt.Fprintln(os.Stderr, "         Add 'skip_tls_verify: false' to verify it, or 'skip_tls_verify: true' to keep skipping and silence this warning.")
	}
	api := config.NewPortainerAPI(cfg.PortainerURL, skip)
	api.Token = cfg.PortainerAPIKey
	return api
}

// projectTemplateData builds the template data for a project's agent
// configs from the global and project configuration.
func projectTemplateData(cfg *config.GlobalConfig, projectName string, pc *config.ProjectConfig, apiToken, mcpBinaryPath string) TemplateData {
	skip, _ := cfg.InsecureTLS()
	data := TemplateData{
		ProjectName:   projectName,
		StackName:     pc.StackName,
		Tier:          pc.Tier,
		PortainerURL:  cfg.PortainerURL,
		EnvironmentID: cfg.PortainerEnvID,
		APIToken:      apiToken,
		MCPBinaryPath: mcpBinaryPath,
		TeamID:        pc.TeamID,
		UserID:        pc.UserID,
		SkipTLSVerify: skip,
		ApprovalMode:  cfg.Approval.EffectiveMode(),
	}
	if data.ApprovalMode == config.ApprovalModeHuman {
		data.ApprovalWebhook = cfg.Approval.URL
		data.ApprovalWebhookToken = cfg.Approval.WebhookToken
	}
	if cp := pc.ComposePolicy; cp != nil {
		data.ComposePolicyOff = cp.Mode == "off"
		data.AllowBindMounts = strings.Join(cp.AllowBindMounts, ",")
	}
	return data
}

// resolveMCPBinary prefers a towline-mcp next to the running towline binary.
func resolveMCPBinary() string {
	if exePath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exePath), "towline-mcp")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "towline-mcp"
}

// projectNameFromStack derives the project name from a <project>-<tier> stack name.
func projectNameFromStack(stackName, tier string) string {
	return strings.TrimSuffix(stackName, "-"+tier)
}

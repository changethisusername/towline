package middleware

import (
	"github.com/mark3labs/mcp-go/server"
)

type Tier string

const (
	TierDev  Tier = "dev"
	TierProd Tier = "prod"
)

type ToolCategory int

const (
	CategoryRead          ToolCategory = iota
	CategoryOperational
	CategoryConfiguration
	CategoryDeploy
	CategoryDestructive
	CategoryExec
)

func (c ToolCategory) NeedsApproval() bool {
	return c >= CategoryConfiguration
}

type MiddlewareFunc func(server.ToolHandlerFunc) server.ToolHandlerFunc

func Chain(handler server.ToolHandlerFunc, middlewares ...MiddlewareFunc) server.ToolHandlerFunc {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// toolCategories maps tool names to their approval category.
var toolCategories = map[string]ToolCategory{
	// Read tools - no approval
	"listLocalStacks":        CategoryRead,
	"getLocalStackFile":      CategoryRead,
	"listStacks":             CategoryRead,
	"getStackFile":           CategoryRead,
	"listEnvironments":       CategoryRead,
	"listTeams":              CategoryRead,
	"listUsers":              CategoryRead,
	"getSettings":            CategoryRead,
	"listEnvironmentTags":    CategoryRead,
	"listEnvironmentGroups":  CategoryRead,
	"listAccessGroups":       CategoryRead,
	"towline_service_health": CategoryRead,
	"towline_service_logs":   CategoryRead,
	"towline_env_get":        CategoryRead,
	"towline_domains_list":   CategoryRead,
	"towline_deployments":    CategoryRead,

	// Operational - no approval
	"startLocalStack": CategoryOperational,

	// Configuration - approval in prod
	"towline_env_set":        CategoryConfiguration,
	"towline_domains_add":    CategoryConfiguration,
	"towline_domains_remove": CategoryConfiguration,

	// Deploy - approval in prod
	"createLocalStack": CategoryDeploy,
	"updateLocalStack": CategoryDeploy,
	"towline_scale":    CategoryDeploy,

	// Destructive - approval in prod
	"stopLocalStack":   CategoryDestructive,
	"deleteLocalStack": CategoryDestructive,

	// Exec - approval in prod
	"towline_exec": CategoryExec,

	// Docker proxy - read by default, exec for non-GET
	"dockerProxy": CategoryRead,
}

// CategoryForTool returns the approval category for a tool.
func CategoryForTool(toolName string) ToolCategory {
	if cat, ok := toolCategories[toolName]; ok {
		return cat
	}
	return CategoryConfiguration
}

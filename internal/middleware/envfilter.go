package middleware

import (
	"context"
	"regexp"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// StackUpdatePreparer returns, for an update to newCompose, the stack's
// current user env and the internal _TOWLINE_* env that must be carried
// over (including a deployment history entry for this update, recorded
// with description).
type StackUpdatePreparer func(newCompose, description string) (userEnv, internalEnv []models.LocalStackEnvVar, err error)

// defaultUpdateDescription is the history entry for an agent's update.
const defaultUpdateDescription = "Stack updated via updateLocalStack"

var (
	commitSHAPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	commitEnvNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

// NewEnvFilter returns a middleware that strips _TOWLINE_ prefixed environment
// variables from createLocalStack and updateLocalStack requests. This prevents
// agents from overriding internal towline state stored in stack env vars.
//
// For updateLocalStack, Portainer replaces the whole env list, so when prepare
// is set the stack's internal variables are re-appended (and the update is
// recorded in deployment history), and an omitted env keeps the current
// user env instead of wiping it.
//
// This filtering is implemented as middleware (rather than in the upstream
// handler) to keep the upstream local_stack.go handler file unmodified for
// clean mergeability with future Portainer MCP releases.
func NewEnvFilter(toolName string, prepare StackUpdatePreparer) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		if toolName != "createLocalStack" && toolName != "updateLocalStack" {
			return next
		}
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := request.GetArguments()
			if args == nil {
				return next(ctx, request)
			}

			env, envProvided := args["env"].([]interface{})

			filtered := make([]interface{}, 0, len(env))
			for _, item := range env {
				obj, ok := item.(map[string]interface{})
				if !ok {
					filtered = append(filtered, item)
					continue
				}
				name, _ := obj["name"].(string)
				if strings.HasPrefix(name, "_TOWLINE_") {
					continue
				}
				filtered = append(filtered, item)
			}

			if toolName == "updateLocalStack" && prepare != nil {
				origin := callerOrigin(ctx)
				description := defaultUpdateDescription
				if origin != nil && origin.Summary != "" {
					description = origin.Summary
				}
				file, _ := toolgen.NewParameterParser(request).GetString("file", false)
				userEnv, internalEnv, err := prepare(file, description)
				if err != nil {
					return mcp.NewToolResultErrorFromErr("failed to read current stack environment", err), nil
				}
				if !envProvided {
					filtered = appendEnv(filtered, userEnv)
				}
				if origin != nil && origin.CommitEnv != "" {
					// Set only in process by the auto-deploy watcher; checked
					// again here so a bad value can never reach the stack.
					if !commitEnvNamePattern.MatchString(origin.CommitEnv) || strings.HasPrefix(origin.CommitEnv, "_TOWLINE_") || !commitSHAPattern.MatchString(origin.Commit) {
						return mcp.NewToolResultError("invalid commit env for this deploy; refused"), nil
					}
					filtered = setEnv(filtered, origin.CommitEnv, origin.Commit)
				}
				filtered = appendEnv(filtered, internalEnv)
			} else if !envProvided {
				return next(ctx, request)
			}

			args["env"] = filtered
			return next(ctx, request)
		}
	}
}

// setEnv replaces every entry named name with one holding value.
func setEnv(env []interface{}, name, value string) []interface{} {
	out := make([]interface{}, 0, len(env)+1)
	for _, item := range env {
		if obj, ok := item.(map[string]interface{}); ok {
			if n, _ := obj["name"].(string); n == name {
				continue
			}
		}
		out = append(out, item)
	}
	return append(out, map[string]interface{}{"name": name, "value": value})
}

func appendEnv(dst []interface{}, env []models.LocalStackEnvVar) []interface{} {
	for _, e := range env {
		dst = append(dst, map[string]interface{}{"name": e.Name, "value": e.Value})
	}
	return dst
}

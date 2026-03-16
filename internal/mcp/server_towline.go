package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Tools returns the loaded tool definitions map.
func (s *PortainerMCPServer) Tools() map[string]mcp.Tool {
	return s.tools
}

// Client returns the Portainer client used by this server.
func (s *PortainerMCPServer) Client() PortainerClient {
	return s.cli
}

// IsReadOnly returns whether the server is in read-only mode.
func (s *PortainerMCPServer) IsReadOnly() bool {
	return s.readOnly
}

// AddToolWithHandler registers a tool with a handler on the underlying MCP server.
func (s *PortainerMCPServer) AddToolWithHandler(tool mcp.Tool, handler server.ToolHandlerFunc) {
	s.srv.AddTool(tool, handler)
}

// AddToolWrapped registers a tool with a handler, applying middleware wrappers.
// Towline tools must be merged via MergeTools() before calling this for towline tool names.
func (s *PortainerMCPServer) AddToolWrapped(toolName string, handler server.ToolHandlerFunc, wrappers ...func(server.ToolHandlerFunc) server.ToolHandlerFunc) {
	tool, exists := s.tools[toolName]
	if !exists {
		return
	}

	wrapped := handler
	for i := len(wrappers) - 1; i >= 0; i-- {
		wrapped = wrappers[i](wrapped)
	}

	s.srv.AddTool(tool, wrapped)
}

// MergeTools adds additional tool definitions to the server's tools map.
// Used to load towline-specific tools from towline-tools.yaml.
func (s *PortainerMCPServer) MergeTools(additional map[string]mcp.Tool) {
	for name, tool := range additional {
		s.tools[name] = tool
	}
}

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

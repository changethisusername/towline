package towline

import (
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/proxy"
	"github.com/changethisusername/towline/pkg/portainer/models"
)

type ProxyFunc func(opts models.DockerProxyRequestOptions) ([]byte, error)

type Handlers struct {
	Server       *mcp.PortainerMCPServer
	StackName    string
	EnvID        int
	proxyFn      ProxyFunc
	ProxyManager proxy.Manager
	CaddyManager *proxy.CaddyManager
}

func NewHandlers(server *mcp.PortainerMCPServer, stackName string, envID int, proxyFn ProxyFunc) *Handlers {
	return &Handlers{
		Server:    server,
		StackName: stackName,
		EnvID:     envID,
		proxyFn:   proxyFn,
	}
}

package towline

import (
	"sync"

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

	// stackMu serializes read-modify-write cycles on the stack (compose
	// file and env, including deployment history). Tool calls can run
	// concurrently, and each cycle re-submits the whole stack, so without
	// it parallel calls silently overwrite each other's changes.
	stackMu sync.Mutex
}

func NewHandlers(server *mcp.PortainerMCPServer, stackName string, envID int, proxyFn ProxyFunc) *Handlers {
	return &Handlers{
		Server:    server,
		StackName: stackName,
		EnvID:     envID,
		proxyFn:   proxyFn,
	}
}

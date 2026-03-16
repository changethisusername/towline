# Towline Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Towline — permission-aware deployment for AI agents on homelab infrastructure via Portainer.

**Architecture:** Fork Portainer MCP v0.7.0 and add middleware (stack scoping, tier gating, container ownership) plus high-level towline tools. Separate CLI binary for project scaffolding. DevOps skill markdown embedded and copied into projects.

**Tech Stack:** Go 1.24+, mcp-go v0.32.0, gopkg.in/yaml.v3, Portainer CE 2.39.0 LTS API

**Spec:** `docs/superpowers/specs/2026-03-16-towline-design.md`

---

## Chunk 1: Repository Setup & Fork Foundation

This chunk copies upstream Portainer MCP v0.7.0 into our repo structure, renames module paths, sets up the build system, adds the minimal server.go modifications needed for middleware support, and verifies the fork compiles.

### Task 1: Initialize Go module and copy upstream code

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `.gitignore`
- Copy from `/tmp/portainer-mcp-upstream/`: all `internal/mcp/`, `internal/tooldef/`, `pkg/portainer/`, `pkg/toolgen/` files

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/tie/Documents/GitHub/towline
go mod init github.com/changethisusername/towline
```

- [ ] **Step 2: Copy upstream source files preserving directory structure**

Copy these directories from `/tmp/portainer-mcp-upstream/` into the towline repo:
- `internal/mcp/` (all `.go` files including tests)
- `internal/mcp/testdata/` (test fixtures)
- `internal/tooldef/tooldef.go` and `internal/tooldef/tools.yaml`
- `internal/k8sutil/` (required by `kubernetes.go`)
- `pkg/portainer/` (all subdirectories: `client/`, `models/`, `utils/`)
- `pkg/toolgen/` (all `.go` files including tests)

```bash
mkdir -p internal/mcp internal/mcp/testdata internal/tooldef internal/k8sutil pkg/portainer pkg/toolgen
cp /tmp/portainer-mcp-upstream/internal/mcp/*.go internal/mcp/
cp -r /tmp/portainer-mcp-upstream/internal/mcp/testdata/* internal/mcp/testdata/
cp /tmp/portainer-mcp-upstream/internal/tooldef/tooldef.go internal/tooldef/
cp /tmp/portainer-mcp-upstream/internal/tooldef/tools.yaml internal/tooldef/
cp /tmp/portainer-mcp-upstream/internal/k8sutil/*.go internal/k8sutil/
cp -r /tmp/portainer-mcp-upstream/pkg/portainer/* pkg/portainer/
cp /tmp/portainer-mcp-upstream/pkg/toolgen/*.go pkg/toolgen/
```

- [ ] **Step 3: Rename all import paths from upstream to towline module**

In every copied `.go` file, replace:
- `github.com/portainer/portainer-mcp/` → `github.com/changethisusername/towline/`

```bash
find internal/ pkg/ -name '*.go' -exec sed -i '' 's|github.com/portainer/portainer-mcp/|github.com/changethisusername/towline/|g' {} +
```

- [ ] **Step 4: Create the towline-mcp entry point**

Create `cmd/towline-mcp/main.go` — initially a copy of the upstream entry point with updated imports. This will be modified in later tasks.

```go
// cmd/towline-mcp/main.go
package main

import (
	"flag"

	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/tooldef"
	"github.com/rs/zerolog/log"
)

const defaultToolsPath = "tools.yaml"

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	log.Info().
		Str("version", Version).
		Str("build-date", BuildDate).
		Str("commit", Commit).
		Msg("Towline MCP server")

	serverFlag := flag.String("server", "", "The Portainer server URL")
	tokenFlag := flag.String("token", "", "The authentication token for the Portainer server")
	toolsFlag := flag.String("tools", "", "The path to the tools YAML file")
	readOnlyFlag := flag.Bool("read-only", false, "Run in read-only mode")
	disableVersionCheckFlag := flag.Bool("disable-version-check", true, "Disable Portainer server version check")

	flag.Parse()

	if *serverFlag == "" || *tokenFlag == "" {
		log.Fatal().Msg("Both -server and -token flags are required")
	}

	toolsPath := *toolsFlag
	if toolsPath == "" {
		toolsPath = defaultToolsPath
	}

	exists, err := tooldef.CreateToolsFileIfNotExists(toolsPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create tools.yaml file")
	}

	if exists {
		log.Info().Msg("using existing tools.yaml file")
	} else {
		log.Info().Msg("created tools.yaml file")
	}

	log.Info().
		Str("portainer-host", *serverFlag).
		Str("tools-path", toolsPath).
		Bool("read-only", *readOnlyFlag).
		Bool("disable-version-check", *disableVersionCheckFlag).
		Msg("starting MCP server")

	server, err := mcp.NewPortainerMCPServer(
		*serverFlag, *tokenFlag, toolsPath,
		mcp.WithReadOnly(*readOnlyFlag),
		mcp.WithDisableVersionCheck(*disableVersionCheckFlag),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create server")
	}

	server.AddEnvironmentFeatures()
	server.AddEnvironmentGroupFeatures()
	server.AddTagFeatures()
	server.AddStackFeatures()
	server.AddLocalStackFeatures()
	server.AddSettingsFeatures()
	server.AddUserFeatures()
	server.AddTeamFeatures()
	server.AddAccessGroupFeatures()
	server.AddDockerProxyFeatures()
	server.AddKubernetesProxyFeatures()

	err = server.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}
}
```

Note: `-disable-version-check` defaults to `true` (changed from upstream's `false`) since we target Portainer CE 2.39.0 while the upstream SDK targets 2.31.2.

- [ ] **Step 5: Create placeholder CLI entry point**

```go
// cmd/towline/main.go
package main

import (
	"fmt"
	"os"
)

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: towline <command> [args]\n")
		fmt.Fprintf(os.Stderr, "Commands: setup, init, list, destroy, promote, rotate-keys, status\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Create Makefile**

```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

LDFLAGS = -s -w \
  -X main.Version=$(VERSION) \
  -X main.Commit=$(COMMIT) \
  -X main.BuildDate=$(BUILD_DATE)

PLATFORM ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)

.PHONY: build build-mcp build-cli test test-coverage fmt vet clean

build: build-mcp build-cli

build-mcp:
	@mkdir -p dist
	GOOS=$(PLATFORM) GOARCH=$(ARCH) CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o dist/towline-mcp ./cmd/towline-mcp

build-cli:
	@mkdir -p dist
	GOOS=$(PLATFORM) GOARCH=$(ARCH) CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o dist/towline ./cmd/towline

test:
	go test -v ./internal/... ./pkg/...

test-coverage:
	go test -v -coverprofile=coverage.out ./internal/... ./pkg/...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

clean:
	rm -rf dist/
```

- [ ] **Step 7: Create .gitignore**

```
dist/
coverage.out
tools.yaml
*.key
.env
.env.*
```

- [ ] **Step 8: Install dependencies and verify compilation**

```bash
go mod tidy
make build
```

Expected: both `dist/towline-mcp` and `dist/towline` binaries compile successfully.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat: initialize towline repo with upstream Portainer MCP v0.7.0 fork"
```

### Task 2: Add server.go accessor methods for middleware support

**Files:**
- Modify: `internal/mcp/server.go`
- Create: `internal/mcp/server_towline.go`

The upstream `PortainerMCPServer` has unexported fields (`srv`, `cli`, `tools`, `readOnly`). Rather than modifying `server.go` directly (which would complicate merges), we add a new file `server_towline.go` in the same package with accessor methods. This is cleaner for upstream tracking — only one new file, zero upstream modifications.

- [ ] **Step 1: Create server_towline.go with accessor and wrapper methods**

```go
// internal/mcp/server_towline.go
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
// This allows external packages (like middleware) to register tools directly.
func (s *PortainerMCPServer) AddToolWithHandler(tool mcp.Tool, handler server.ToolHandlerFunc) {
	s.srv.AddTool(tool, handler)
}

// AddToolWrapped registers a tool with a handler, applying middleware wrappers.
// This is the primary way middleware integrates: call this instead of addToolIfExists.
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
```

- [ ] **Step 2: Verify compilation**

```bash
make build
```

Expected: compiles successfully with new accessor methods.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server_towline.go
git commit -m "feat: add server accessor methods for middleware support"
```

### Task 3: Create towline-tools.yaml with tool definitions

**Files:**
- Create: `internal/tooldef/towline-tools.yaml`
- Modify: `internal/tooldef/tooldef.go` (add embed for towline tools)

- [ ] **Step 1: Create towline-tools.yaml**

```yaml
---
version: v1.0.0
tools:
  ## Towline Service Tools
  ## High-level tools for managing services in a scoped stack
  ## ------------------------------------------------------------
  - name: towline_service_health
    description: >
      Get health snapshot for one or all services in the stack: state, uptime,
      restart count, CPU/memory usage, and exit code.
    parameters:
      - name: service
        description: Service name from docker-compose. If omitted, returns all services.
        type: string
    annotations:
      title: Service Health
      readOnlyHint: true
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_service_logs
    description: >
      Fetch recent log output for a named service. Strips Docker binary framing headers.
    parameters:
      - name: service
        description: Service name from docker-compose
        type: string
        required: true
      - name: tail
        description: Number of lines to return (default 200)
        type: number
      - name: since
        description: Only return logs after this RFC3339 timestamp
        type: string
      - name: filter
        description: Filter log lines containing this string
        type: string
    annotations:
      title: Service Logs
      readOnlyHint: true
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_env_get
    description: >
      Read stack environment variables. Sensitive values (names containing KEY,
      SECRET, PASSWORD, TOKEN) are masked.
    parameters:
      - name: name
        description: Specific variable name to read. If omitted, returns all variables.
        type: string
    annotations:
      title: Get Environment Variables
      readOnlyHint: true
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_env_set
    description: >
      Set a stack environment variable. Service restart may be needed to pick up
      the change. In prod tier, requires approval.
    parameters:
      - name: name
        description: Variable name
        type: string
        required: true
      - name: value
        description: Variable value
        type: string
        required: true
      - name: approvalToken
        description: Approval token for prod tier operations
        type: string
    annotations:
      title: Set Environment Variable
      readOnlyHint: false
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_domains_list
    description: >
      List all domain/routing mappings for services in the stack.
    annotations:
      title: List Domains
      readOnlyHint: true
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_domains_add
    description: >
      Add domain routing to a service. Supports Traefik (labels), Caddy (admin API),
      or Cloudflare Tunnels (returns instructions for Cloudflare MCP).
      In prod tier, requires approval.
    parameters:
      - name: service
        description: Service name from docker-compose
        type: string
        required: true
      - name: domain
        description: Domain name to route (e.g. app.example.com)
        type: string
        required: true
      - name: port
        description: Service port (default 80)
        type: number
      - name: method
        description: Proxy method. Auto-detected if omitted.
        type: string
        enum:
          - traefik
          - caddy
          - cloudflare
      - name: approvalToken
        description: Approval token for prod tier operations
        type: string
    annotations:
      title: Add Domain
      readOnlyHint: false
      destructiveHint: false
      idempotentHint: true
      openWorldHint: true

  - name: towline_domains_remove
    description: >
      Remove domain routing from a service. In prod tier, requires approval.
    parameters:
      - name: service
        description: Service name from docker-compose
        type: string
        required: true
      - name: domain
        description: Domain name to remove
        type: string
        required: true
      - name: approvalToken
        description: Approval token for prod tier operations
        type: string
    annotations:
      title: Remove Domain
      readOnlyHint: false
      destructiveHint: true
      idempotentHint: true
      openWorldHint: true

  - name: towline_scale
    description: >
      Scale a service to N replicas. Warns if the service has persistent volumes.
      In prod tier, requires approval.
    parameters:
      - name: service
        description: Service name from docker-compose
        type: string
        required: true
      - name: replicas
        description: Number of replicas
        type: number
        required: true
      - name: approvalToken
        description: Approval token for prod tier operations
        type: string
    annotations:
      title: Scale Service
      readOnlyHint: false
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_deployments
    description: >
      View deployment history with diffs and outcomes.
    parameters:
      - name: limit
        description: Maximum number of entries to return (default 10)
        type: number
    annotations:
      title: Deployment History
      readOnlyHint: true
      destructiveHint: false
      idempotentHint: true
      openWorldHint: false

  - name: towline_exec
    description: >
      Execute a command inside a running container. In prod tier, requires approval.
    parameters:
      - name: service
        description: Service name from docker-compose
        type: string
        required: true
      - name: command
        description: Command to execute (string or space-separated)
        type: string
        required: true
      - name: approvalToken
        description: Approval token for prod tier operations
        type: string
    annotations:
      title: Execute Command
      readOnlyHint: false
      destructiveHint: false
      idempotentHint: false
      openWorldHint: false
```

- [ ] **Step 2: Replace tooldef.go with version that also embeds towline-tools.yaml**

This fully replaces the `tooldef.go` copied from upstream in Task 1. The only addition is the `TowlineToolsFile` embed and `CreateTowlineToolsFileIfNotExists` function.

```go
// internal/tooldef/tooldef.go
package tooldef

import (
	_ "embed"
	"os"
)

//go:embed tools.yaml
var ToolsFile []byte

//go:embed towline-tools.yaml
var TowlineToolsFile []byte

// CreateToolsFileIfNotExists creates the tools.yaml file if it doesn't exist
// It returns true if the file already exists, false if it was created or an error occurred
func CreateToolsFileIfNotExists(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.WriteFile(path, ToolsFile, 0644)
		if err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// CreateTowlineToolsFileIfNotExists creates the towline-tools.yaml file if it doesn't exist
func CreateTowlineToolsFileIfNotExists(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.WriteFile(path, TowlineToolsFile, 0644)
		if err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}
```

- [ ] **Step 3: Verify compilation**

```bash
make build
```

- [ ] **Step 4: Commit**

```bash
git add internal/tooldef/towline-tools.yaml internal/tooldef/tooldef.go
git commit -m "feat: add towline tool definitions YAML and embed support"
```

### Task 4: Create skeleton packages for new towline code

**Files:**
- Create: `internal/middleware/middleware.go`
- Create: `internal/approval/approval.go`
- Create: `internal/towline/towline.go`
- Create: `internal/proxy/proxy.go`
- Create: `internal/cli/cli.go`
- Create: `pkg/config/config.go`

These are package-level skeletons that establish the package structure and core types. Implementation comes in later chunks.

- [ ] **Step 1: Create middleware package skeleton**

```go
// internal/middleware/middleware.go
package middleware

import (
	"github.com/mark3labs/mcp-go/server"
)

// MiddlewareFunc wraps a tool handler with additional behavior.
type MiddlewareFunc func(server.ToolHandlerFunc) server.ToolHandlerFunc

// Chain applies multiple middleware wrappers to a handler.
// Middlewares are applied in order: first middleware is outermost.
func Chain(handler server.ToolHandlerFunc, middlewares ...MiddlewareFunc) server.ToolHandlerFunc {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
```

- [ ] **Step 2: Create approval package skeleton**

```go
// internal/approval/approval.go
package approval

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// TokenTTL is how long an approval token is valid.
	TokenTTL = 5 * time.Minute
	// CleanupInterval is how often expired tokens are pruned.
	CleanupInterval = 30 * time.Second
)

// PendingApproval represents a gated operation waiting for approval.
type PendingApproval struct {
	ToolName  string
	Args      map[string]any
	CreatedAt time.Time
}

// Store manages approval tokens for prod-tier operations.
type Store struct {
	mu      sync.Mutex
	pending map[string]*PendingApproval
	stopCh  chan struct{}
}

// NewStore creates a new approval store and starts the cleanup goroutine.
func NewStore() *Store {
	s := &Store{
		pending: make(map[string]*PendingApproval),
		stopCh:  make(chan struct{}),
	}
	go s.cleanup()
	return s
}

// Request creates a new approval token for a pending operation.
func (s *Store) Request(toolName string, args map[string]any) string {
	token := generateToken()
	s.mu.Lock()
	s.pending[token] = &PendingApproval{
		ToolName:  toolName,
		Args:      args,
		CreatedAt: time.Now(),
	}
	s.mu.Unlock()
	return token
}

// Validate checks if a token is valid and consumes it.
// Returns the pending approval if valid, nil if not.
func (s *Store) Validate(token string) *PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[token]
	if !ok {
		return nil
	}

	if time.Since(p.CreatedAt) > TokenTTL {
		delete(s.pending, token)
		return nil
	}

	delete(s.pending, token)
	return p
}

// Stop halts the cleanup goroutine.
func (s *Store) Stop() {
	close(s.stopCh)
}

func (s *Store) cleanup() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for token, p := range s.pending {
				if now.Sub(p.CreatedAt) > TokenTTL {
					delete(s.pending, token)
				}
			}
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}

func generateToken() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 3: Create towline handlers package skeleton**

```go
// internal/towline/towline.go
package towline

import (
	"github.com/changethisusername/towline/internal/mcp"
)

// Handlers holds dependencies for towline-specific tool handlers.
type Handlers struct {
	Server    *mcp.PortainerMCPServer
	StackName string
	EnvID     int
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(server *mcp.PortainerMCPServer, stackName string, envID int) *Handlers {
	return &Handlers{
		Server:    server,
		StackName: stackName,
		EnvID:     envID,
	}
}
```

- [ ] **Step 4: Create proxy package skeleton**

```go
// internal/proxy/proxy.go
package proxy

// Backend represents a reverse proxy type.
type Backend string

const (
	BackendTraefik    Backend = "traefik"
	BackendCaddy      Backend = "caddy"
	BackendCloudflare Backend = "cloudflare"
	BackendNone       Backend = ""
)

// DomainMapping represents a domain routed to a service.
type DomainMapping struct {
	Service string  `json:"service"`
	Domain  string  `json:"domain"`
	Port    int     `json:"port"`
	Method  Backend `json:"method"`
}

// Manager handles domain routing for a specific proxy backend.
type Manager interface {
	List(composeContent string) ([]DomainMapping, error)
	Add(composeContent, service, domain string, port int) (string, error) // returns updated compose or API result
	Remove(composeContent, service, domain string) (string, error)
}
```

- [ ] **Step 5: Create config package skeleton**

```go
// pkg/config/config.go
package config

// GlobalConfig represents ~/.towline/config.yaml
type GlobalConfig struct {
	PortainerURL    string `yaml:"portainer_url"`
	PortainerAPIKey string `yaml:"portainer_admin_key"`
	PortainerEnvID  int    `yaml:"portainer_env_id"`
	ProjectsDir     string `yaml:"projects_dir"`
}

// ProjectConfig represents a project's towline.json
type ProjectConfig struct {
	StackName string `json:"stack_name"`
	Tier      string `json:"tier"`
	TeamID    int    `json:"team_id"`
	UserID    int    `json:"user_id"`
	EnvID     int    `json:"environment_id"`
	MCPBinary string `json:"mcp_binary"`
	MCPArgs   MCPArgs `json:"mcp_args"`
}

// MCPArgs holds the arguments for the towline-mcp binary.
type MCPArgs struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	Stack  string `json:"stack"`
	Tier   string `json:"tier"`
}
```

- [ ] **Step 6: Create CLI package skeleton**

```go
// internal/cli/cli.go
package cli

import (
	"fmt"
	"os"
)

// Run dispatches to the appropriate subcommand.
func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: towline <command> [args]\nCommands: setup, init, list, destroy, promote, rotate-keys, status")
	}

	switch args[0] {
	case "setup":
		return fmt.Errorf("not yet implemented")
	case "init":
		return fmt.Errorf("not yet implemented")
	case "list":
		return fmt.Errorf("not yet implemented")
	case "destroy":
		return fmt.Errorf("not yet implemented")
	case "promote":
		return fmt.Errorf("not yet implemented")
	case "rotate-keys":
		return fmt.Errorf("not yet implemented")
	case "status":
		return fmt.Errorf("not yet implemented")
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}
```

- [ ] **Step 7: Update CLI entry point to use the cli package**

```go
// cmd/towline/main.go
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
		fmt.Fprintf(os.Stderr, "towline %s (%s)\n", Version, Commit)
		fmt.Fprintf(os.Stderr, "Usage: towline <command> [args]\n")
		fmt.Fprintf(os.Stderr, "Commands: setup, init, list, destroy, promote, rotate-keys, status\n")
		os.Exit(1)
	}

	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 8: Run go mod tidy and verify compilation**

```bash
go mod tidy
make build
```

Expected: both binaries compile. The towline-mcp binary is a working Portainer MCP server. The towline CLI prints usage and exits.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat: add skeleton packages for middleware, approval, towline handlers, proxy, config, and CLI"
```

---

## Chunk 2: Middleware Layer

Implements the three middleware layers (tier gating, stack scoping, container ownership) and wires them into the MCP entry point. After this chunk, `towline-mcp` enforces stack isolation and prod-tier approval gating on all upstream tools.

### Task 5: Implement tier gating middleware

**Files:**
- Modify: `internal/middleware/middleware.go` (add tier classification)
- Create: `internal/middleware/tiergating.go`
- Create: `internal/middleware/tiergating_test.go`

- [ ] **Step 1: Add tool classification to middleware package**

Add tier classification constants and a helper to `middleware.go`:

```go
// internal/middleware/middleware.go
package middleware

import (
	"github.com/mark3labs/mcp-go/server"
)

// Tier represents a deployment tier.
type Tier string

const (
	TierDev  Tier = "dev"
	TierProd Tier = "prod"
)

// ToolCategory classifies tools by their approval requirements in prod.
type ToolCategory int

const (
	CategoryRead          ToolCategory = iota // No approval needed
	CategoryOperational                       // No approval needed
	CategoryConfiguration                     // Approval in prod
	CategoryDeploy                            // Approval in prod
	CategoryDestructive                       // Approval in prod
	CategoryExec                              // Approval in prod
)

// NeedsApproval returns true if this category requires approval in prod tier.
func (c ToolCategory) NeedsApproval() bool {
	return c >= CategoryConfiguration
}

// MiddlewareFunc wraps a tool handler with additional behavior.
type MiddlewareFunc func(server.ToolHandlerFunc) server.ToolHandlerFunc

// Chain applies multiple middleware wrappers to a handler.
// Middlewares are applied in order: first middleware is outermost.
func Chain(handler server.ToolHandlerFunc, middlewares ...MiddlewareFunc) server.ToolHandlerFunc {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// toolCategories maps tool names to their approval category.
var toolCategories = map[string]ToolCategory{
	// Read tools - no approval
	"listLocalStacks":               CategoryRead,
	"getLocalStackFile":             CategoryRead,
	"listStacks":                    CategoryRead,
	"getStackFile":                  CategoryRead,
	"listEnvironments":              CategoryRead,
	"listTeams":                     CategoryRead,
	"listUsers":                     CategoryRead,
	"getSettings":                   CategoryRead,
	"listEnvironmentTags":           CategoryRead,
	"listEnvironmentGroups":         CategoryRead,
	"listAccessGroups":              CategoryRead,
	"towline_service_health":        CategoryRead,
	"towline_service_logs":          CategoryRead,
	"towline_env_get":               CategoryRead,
	"towline_domains_list":          CategoryRead,
	"towline_deployments":           CategoryRead,

	// Operational tools - no approval
	"startLocalStack":               CategoryOperational,

	// Configuration tools - approval in prod
	"towline_env_set":               CategoryConfiguration,
	"towline_domains_add":           CategoryConfiguration,
	"towline_domains_remove":        CategoryConfiguration,

	// Deploy tools - approval in prod
	"createLocalStack":              CategoryDeploy,
	"updateLocalStack":              CategoryDeploy,
	"towline_scale":                 CategoryDeploy,

	// Destructive tools - approval in prod
	"stopLocalStack":                CategoryDestructive,
	"deleteLocalStack":              CategoryDestructive,

	// Exec tools - approval in prod
	"towline_exec":                  CategoryExec,

	// Docker proxy is special - handled by method (GET=read, others=exec)
	"dockerProxy":                   CategoryRead, // overridden at runtime for non-GET
}

// CategoryForTool returns the approval category for a tool.
// For dockerProxy, the caller must check the HTTP method separately.
func CategoryForTool(toolName string) ToolCategory {
	if cat, ok := toolCategories[toolName]; ok {
		return cat
	}
	// Unknown tools default to requiring approval in prod
	return CategoryConfiguration
}
```

- [ ] **Step 2: Write failing test for tier gating**

```go
// internal/middleware/tiergating_test.go
package middleware

import (
	"context"
	"testing"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestTierGating_DevPassesThrough(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	called := false
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	gated := NewTierGating(TierDev, store, "updateLocalStack")(inner)
	result, err := gated(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("inner handler was not called in dev tier")
	}
	if result.Content[0].(mcp.TextContent).Text != "ok" {
		t.Fatal("unexpected result")
	}
}

func TestTierGating_ProdBlocksWithoutToken(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	called := false
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	gated := NewTierGating(TierProd, store, "updateLocalStack")(inner)
	result, err := gated(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("inner handler should NOT be called without approval")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if len(text) == 0 {
		t.Fatal("expected approval request message")
	}
}

func TestTierGating_ProdPassesWithValidToken(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	called := false
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	// First call: get the token
	gated := NewTierGating(TierProd, store, "updateLocalStack")(inner)
	_, _ = gated(context.Background(), mcp.CallToolRequest{})

	// Extract token from store (we stored one)
	// Since we can't easily extract from result text, test via store directly
	token := store.Request("updateLocalStack", nil)

	// Second call: with valid token
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{"approvalToken": token},
		},
	}
	result, err := gated(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("inner handler should be called with valid token")
	}
	if result.Content[0].(mcp.TextContent).Text != "ok" {
		t.Fatal("unexpected result")
	}
}

func TestTierGating_ProdReadToolPassesThrough(t *testing.T) {
	store := approval.NewStore()
	defer store.Stop()

	called := false
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	gated := NewTierGating(TierProd, store, "listLocalStacks")(inner)
	_, err := gated(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("read tools should pass through in prod without approval")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test -v ./internal/middleware/ -run TestTierGating
```

Expected: FAIL — `NewTierGating` not defined.

- [ ] **Step 4: Implement tier gating middleware**

```go
// internal/middleware/tiergating.go
package middleware

import (
	"context"
	"fmt"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/toolgen"
)

// NewTierGating returns a middleware that gates tool calls based on tier.
// In dev tier, all calls pass through. In prod tier, tools that need approval
// require an approvalToken parameter.
func NewTierGating(tier Tier, store *approval.Store, toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Dev tier: always pass through
			if tier == TierDev {
				return next(ctx, request)
			}

			// Check if this tool needs approval
			category := CategoryForTool(toolName)

			// Special case: dockerProxy non-GET needs approval
			if toolName == "dockerProxy" {
				parser := toolgen.NewParameterParser(request)
				method, _ := parser.GetString("method", false)
				if method != "" && method != "GET" {
					category = CategoryExec
				}
			}

			if !category.NeedsApproval() {
				return next(ctx, request)
			}

			// Check for approval token
			parser := toolgen.NewParameterParser(request)
			token, _ := parser.GetString("approvalToken", false)

			if token != "" {
				// Validate the token
				pending := store.Validate(token)
				if pending == nil {
					return mcp.NewToolResultError("Invalid or expired approval token. Request a new one by calling this tool without the approvalToken parameter."), nil
				}
				// Token valid — proceed
				return next(ctx, request)
			}

			// No token — generate one and return approval request
			newToken := store.Request(toolName, request.GetArguments())

			msg := fmt.Sprintf(
				"Production operation requires approval.\n"+
					"Action: %s\n"+
					"To confirm, re-call this tool with the additional parameter approvalToken: \"%s\"",
				toolName, newToken,
			)

			return mcp.NewToolResultText(msg), nil
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test -v ./internal/middleware/ -run TestTierGating
```

Expected: all 4 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/middleware/
git commit -m "feat: implement tier gating middleware with in-chat approval flow"
```

### Task 6: Implement stack scoping middleware

**Files:**
- Create: `internal/middleware/scoping.go`
- Create: `internal/middleware/scoping_test.go`

- [ ] **Step 1: Write failing test for stack scoping**

```go
// internal/middleware/scoping_test.go
package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestStackScoping_FiltersListResults(t *testing.T) {
	// Simulate a listLocalStacks response with multiple stacks
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		stacks := []map[string]any{
			{"name": "myapp-dev", "id": 1},
			{"name": "other-stack", "id": 2},
			{"name": "myapp-dev", "id": 3}, // duplicate name edge case
		}
		data, _ := json.Marshal(stacks)
		return mcp.NewToolResultText(string(data)), nil
	}

	scoper := NewStackScoping("myapp-dev")
	scoped := scoper.ForTool("listLocalStacks")(inner)

	result, err := scoped(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var filtered []map[string]any
	if err := json.Unmarshal([]byte(text), &filtered); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	for _, s := range filtered {
		if s["name"] != "myapp-dev" {
			t.Errorf("expected only myapp-dev stacks, got: %s", s["name"])
		}
	}
}

func TestStackScoping_RejectsMutationOnWrongStack(t *testing.T) {
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	scoper := NewStackScoping("myapp-dev")
	scoper.SetStackID(1) // myapp-dev has ID 1

	scoped := scoper.ForTool("updateLocalStack")(inner)

	// Call with wrong stack ID
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{"id": float64(999)},
		},
	}

	result, err := scoped(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	if text == "ok" {
		t.Fatal("should have rejected mutation on wrong stack ID")
	}
}

func TestStackScoping_AllowsMutationOnCorrectStack(t *testing.T) {
	called := false
	inner := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	scoper := NewStackScoping("myapp-dev")
	scoper.SetStackID(1)

	scoped := scoper.ForTool("updateLocalStack")(inner)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{"id": float64(1)},
		},
	}

	_, err := scoped(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("should have allowed mutation on correct stack ID")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/middleware/ -run TestStackScoping
```

Expected: FAIL — `NewStackScoping` not defined.

- [ ] **Step 3: Implement stack scoping middleware**

```go
// internal/middleware/scoping.go
package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/toolgen"
)

// stackMutationTools are tools that take an "id" parameter referencing a stack.
var stackMutationTools = map[string]bool{
	"updateLocalStack": true,
	"startLocalStack":  true,
	"stopLocalStack":   true,
	"deleteLocalStack": true,
	"getLocalStackFile": true,
}

// stackListTools return arrays of stacks that need filtering.
var stackListTools = map[string]bool{
	"listLocalStacks": true,
}

// StackScoping enforces that all operations target a single named stack.
type StackScoping struct {
	stackName string
	stackID   int
	resolved  bool
	mu        sync.RWMutex
}

// NewStackScoping creates a new stack scoping middleware for the given stack name.
func NewStackScoping(stackName string) *StackScoping {
	return &StackScoping{
		stackName: stackName,
	}
}

// SetStackID sets the resolved stack ID. Called after resolving name to ID.
func (s *StackScoping) SetStackID(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stackID = id
	s.resolved = true
}

// StackID returns the current resolved stack ID.
func (s *StackScoping) StackID() (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stackID, s.resolved
}

// ForTool returns a middleware function configured for a specific tool name.
func (s *StackScoping) ForTool(toolName string) MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// List tools: post-filter results
			if stackListTools[toolName] {
				return s.filterListResult(ctx, request, next)
			}

			// Stack mutation tools: validate stack ID
			if stackMutationTools[toolName] {
				return s.validateStackID(ctx, request, next, toolName)
			}

			// createLocalStack: allow if we're in pending mode (no stack yet)
			if toolName == "createLocalStack" {
				return s.handleCreate(ctx, request, next)
			}

			// All other tools (towline_* tools, dockerProxy, etc): pass through
			// Docker proxy container filtering is handled by ownership middleware
			return next(ctx, request)
		}
	}
}

func (s *StackScoping) filterListResult(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	result, err := next(ctx, request)
	if err != nil {
		return result, err
	}

	// Parse the JSON array result
	text := result.Content[0].(mcp.TextContent).Text
	var stacks []map[string]any
	if err := json.Unmarshal([]byte(text), &stacks); err != nil {
		return result, nil // Can't parse, return as-is
	}

	// Filter to only our stack
	var filtered []map[string]any
	for _, stack := range stacks {
		name, _ := stack["name"].(string)
		if name == s.stackName {
			filtered = append(filtered, stack)
		}
	}

	data, _ := json.Marshal(filtered)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *StackScoping) validateStackID(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc, toolName string) (*mcp.CallToolResult, error) {
	id, resolved := s.StackID()
	if !resolved {
		return mcp.NewToolResultError(fmt.Sprintf("Stack '%s' has not been created yet. Create it first with createLocalStack.", s.stackName)), nil
	}

	parser := toolgen.NewParameterParser(request)
	reqID, err := parser.GetInt("id", true)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid id parameter", err), nil
	}

	if reqID != id {
		return mcp.NewToolResultError(fmt.Sprintf("Operation rejected: stack ID %d does not match scoped stack '%s' (ID %d)", reqID, s.stackName, id)), nil
	}

	return next(ctx, request)
}

func (s *StackScoping) handleCreate(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	_, resolved := s.StackID()
	if resolved {
		return mcp.NewToolResultError(fmt.Sprintf("Stack '%s' already exists. Use updateLocalStack to modify it.", s.stackName)), nil
	}

	// Verify the name matches
	parser := toolgen.NewParameterParser(request)
	name, _ := parser.GetString("name", true)
	if name != s.stackName {
		return mcp.NewToolResultError(fmt.Sprintf("Cannot create stack '%s': this MCP instance is scoped to stack '%s'", name, s.stackName)), nil
	}

	// Allow through, then resolve the ID from the result
	result, err := next(ctx, request)
	if err != nil {
		return result, err
	}

	// Parse the created stack ID from the result text
	// Format: "Local stack created successfully with ID: N"
	text := result.Content[0].(mcp.TextContent).Text
	var createdID int
	if _, scanErr := fmt.Sscanf(text, "Local stack created successfully with ID: %d", &createdID); scanErr == nil && createdID > 0 {
		s.SetStackID(createdID)
	}

	return result, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/middleware/ -run TestStackScoping
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/scoping.go internal/middleware/scoping_test.go
git commit -m "feat: implement stack scoping middleware"
```

### Task 7: Implement container ownership middleware

**Files:**
- Create: `internal/middleware/ownership.go`
- Create: `internal/middleware/ownership_test.go`

- [ ] **Step 1: Write failing test for ownership check**

```go
// internal/middleware/ownership_test.go
package middleware

import (
	"testing"
)

func TestExtractContainerID(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/containers/abc123/logs", "abc123"},
		{"/containers/abc123/stats", "abc123"},
		{"/containers/abc123/start", "abc123"},
		{"/containers/json", ""},        // list endpoint, no specific container
		{"/images/json", ""},             // not a container path
		{"/exec/abc123/start", ""},       // exec path, different pattern
		{"/containers/abc123", "abc123"}, // inspect
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := extractContainerID(tt.path)
			if got != tt.expected {
				t.Errorf("extractContainerID(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/middleware/ -run TestExtractContainerID
```

Expected: FAIL — `extractContainerID` not defined.

- [ ] **Step 3: Implement container ownership middleware**

```go
// internal/middleware/ownership.go
package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
)

// containerPathRegex matches /containers/{id}/... or /containers/{id}
var containerPathRegex = regexp.MustCompile(`^/containers/([^/]+)(?:/|$)`)

// extractContainerID extracts a container ID from a Docker API path.
// Returns empty string if the path doesn't target a specific container.
func extractContainerID(path string) string {
	matches := containerPathRegex.FindStringSubmatch(path)
	if len(matches) < 2 {
		return ""
	}
	id := matches[1]
	// "json" is the list endpoint, not a container ID
	if id == "json" || id == "create" {
		return ""
	}
	return id
}

// ContainerOwnership is a type that holds the proxy client and stack name for ownership checks.
type ContainerOwnership struct {
	stackName string
	envID     int
	proxyFn   func(opts models.DockerProxyRequestOptions) ([]byte, error)
}

// NewContainerOwnership creates a new ownership checker.
// proxyFn should call the Portainer Docker proxy and return the response body.
func NewContainerOwnership(stackName string, envID int, proxyFn func(opts models.DockerProxyRequestOptions) ([]byte, error)) *ContainerOwnership {
	return &ContainerOwnership{
		stackName: stackName,
		envID:     envID,
		proxyFn:   proxyFn,
	}
}

// ForDockerProxy returns a middleware that validates container ownership for dockerProxy calls.
func (o *ContainerOwnership) ForDockerProxy() MiddlewareFunc {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			parser := toolgen.NewParameterParser(request)
			path, _ := parser.GetString("dockerAPIPath", false)

			containerID := extractContainerID(path)
			if containerID == "" {
				// Not targeting a specific container (list call, etc)
				// For container list calls, we add label filtering
				if strings.HasPrefix(path, "/containers/json") {
					return o.filterContainerList(ctx, request, next)
				}
				return next(ctx, request)
			}

			// Verify container belongs to our stack
			owned, err := o.checkOwnership(containerID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to verify container ownership: %v", err)), nil
			}
			if !owned {
				return mcp.NewToolResultError(fmt.Sprintf("container %s does not belong to stack '%s'", containerID, o.stackName)), nil
			}

			return next(ctx, request)
		}
	}
}

func (o *ContainerOwnership) checkOwnership(containerID string) (bool, error) {
	body, err := o.proxyFn(models.DockerProxyRequestOptions{
		EnvironmentID: o.envID,
		Method:        "GET",
		Path:          fmt.Sprintf("/containers/%s/json", containerID),
	})
	if err != nil {
		return false, err
	}

	var inspect struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(body, &inspect); err != nil {
		return false, fmt.Errorf("failed to parse container inspect: %w", err)
	}

	project := inspect.Config.Labels["com.docker.compose.project"]
	return project == o.stackName, nil
}

func (o *ContainerOwnership) filterContainerList(ctx context.Context, request mcp.CallToolRequest, next server.ToolHandlerFunc) (*mcp.CallToolResult, error) {
	// Call the original handler
	result, err := next(ctx, request)
	if err != nil {
		return result, err
	}

	// Parse and filter the container list
	text := result.Content[0].(mcp.TextContent).Text
	var containers []map[string]any
	if err := json.Unmarshal([]byte(text), &containers); err != nil {
		return result, nil // Can't parse, return as-is
	}

	var filtered []map[string]any
	for _, c := range containers {
		labels, _ := c["Labels"].(map[string]any)
		if labels != nil {
			project, _ := labels["com.docker.compose.project"].(string)
			if project == o.stackName {
				filtered = append(filtered, c)
			}
		}
	}

	data, _ := json.Marshal(filtered)
	return mcp.NewToolResultText(string(data)), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test -v ./internal/middleware/ -run TestExtractContainerID
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/ownership.go internal/middleware/ownership_test.go
git commit -m "feat: implement container ownership middleware for Docker proxy"
```

### Task 8: Wire middleware into towline-mcp entry point

**Files:**
- Modify: `cmd/towline-mcp/main.go`

This is the critical integration task. The entry point adds `-stack`, `-tier`, `-proxy`, `-caddy-api` flags, loads towline tools, creates middleware instances, and wraps all tool registrations.

- [ ] **Step 1: Rewrite main.go to wire middleware**

```go
// cmd/towline-mcp/main.go
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/internal/mcp"
	"github.com/changethisusername/towline/internal/middleware"
	"github.com/changethisusername/towline/internal/tooldef"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
	"github.com/rs/zerolog/log"
)

const (
	defaultToolsPath        = "tools.yaml"
	defaultTowlineToolsPath = "towline-tools.yaml"
)

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	log.Info().
		Str("version", Version).
		Str("build-date", BuildDate).
		Str("commit", Commit).
		Msg("Towline MCP server")

	// Upstream flags
	serverFlag := flag.String("server", "", "The Portainer server URL")
	tokenFlag := flag.String("token", "", "The authentication token for the Portainer server")
	toolsFlag := flag.String("tools", "", "The path to the tools YAML file")
	readOnlyFlag := flag.Bool("read-only", false, "Run in read-only mode")
	disableVersionCheckFlag := flag.Bool("disable-version-check", true, "Disable Portainer server version check")

	// Towline flags
	stackFlag := flag.String("stack", "", "Stack name to scope this MCP instance to")
	tierFlag := flag.String("tier", "", "Deployment tier: dev or prod")
	proxyFlag := flag.String("proxy", "", "Proxy backend: traefik, caddy, or cloudflare (auto-detected if omitted)")
	caddyAPIFlag := flag.String("caddy-api", "http://localhost:2019", "Caddy admin API URL")

	flag.Parse()

	if *serverFlag == "" || *tokenFlag == "" {
		log.Fatal().Msg("Both -server and -token flags are required")
	}
	if *stackFlag == "" || *tierFlag == "" {
		log.Fatal().Msg("Both -stack and -tier flags are required")
	}

	tier := middleware.Tier(*tierFlag)
	if tier != middleware.TierDev && tier != middleware.TierProd {
		log.Fatal().Msg("-tier must be 'dev' or 'prod'")
	}

	// Handle tools.yaml files
	toolsPath := *toolsFlag
	if toolsPath == "" {
		toolsPath = defaultToolsPath
	}
	exists, err := tooldef.CreateToolsFileIfNotExists(toolsPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create tools.yaml file")
	}
	if exists {
		log.Info().Msg("using existing tools.yaml file")
	} else {
		log.Info().Msg("created tools.yaml file")
	}

	towlineToolsPath := defaultTowlineToolsPath
	_, err = tooldef.CreateTowlineToolsFileIfNotExists(towlineToolsPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create towline-tools.yaml file")
	}

	log.Info().
		Str("portainer-host", *serverFlag).
		Str("stack", *stackFlag).
		Str("tier", *tierFlag).
		Msg("starting Towline MCP server")

	// Create the upstream server
	server, err := mcp.NewPortainerMCPServer(
		*serverFlag, *tokenFlag, toolsPath,
		mcp.WithReadOnly(*readOnlyFlag),
		mcp.WithDisableVersionCheck(*disableVersionCheckFlag),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create server")
	}

	// Load and merge towline tools
	towlineTools, err := toolgen.LoadToolsFromYAML(towlineToolsPath, "v1.0.0")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load towline tools")
	}
	server.MergeTools(towlineTools)

	// Create middleware instances
	approvalStore := approval.NewStore()
	stackScoping := middleware.NewStackScoping(*stackFlag)

	// Resolve stack ID at startup
	resolveStackID(server, stackScoping, *stackFlag)

	// Helper to create proxy function for ownership checks
	proxyFn := func(opts models.DockerProxyRequestOptions) ([]byte, error) {
		resp, err := server.Client().ProxyDockerRequest(opts)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}

	// Get environment ID from first stack or default to 0
	envID := getEnvironmentID(server, *stackFlag)
	ownership := middleware.NewContainerOwnership(*stackFlag, envID, proxyFn)

	// Build middleware chain for a given tool
	wrappers := func(toolName string) []func(server.ToolHandlerFunc) server.ToolHandlerFunc {
		var mws []func(server.ToolHandlerFunc) server.ToolHandlerFunc

		// Outermost: tier gating
		mws = append(mws, middleware.NewTierGating(tier, approvalStore, toolName))

		// Stack scoping
		mws = append(mws, stackScoping.ForTool(toolName))

		// Container ownership (only for dockerProxy)
		if toolName == "dockerProxy" {
			mws = append(mws, ownership.ForDockerProxy())
		}

		return mws
	}

	// Register upstream features with middleware wrapping
	// We use AddToolWrapped instead of the upstream Add*Features methods
	registerWrappedUpstreamTools(server, wrappers)

	// Register towline-specific tools (implemented in later chunks, stubs for now)
	// TODO: registerTowlineTools(server, wrappers, ...)

	_ = proxyFlag   // Used in Chunk 3 (proxy detection)
	_ = caddyAPIFlag // Used in Chunk 3 (Caddy backend)

	err = server.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}

	approvalStore.Stop()
}

// resolveStackID attempts to resolve the stack name to an ID at startup.
func resolveStackID(server *mcp.PortainerMCPServer, scoping *middleware.StackScoping, stackName string) {
	stacks, err := server.Client().GetLocalStacks()
	if err != nil {
		log.Warn().Err(err).Msg("failed to resolve stack ID at startup, entering pending mode")
		return
	}

	for _, s := range stacks {
		if s.Name == stackName {
			scoping.SetStackID(s.ID)
			log.Info().Int("stack-id", s.ID).Str("stack-name", stackName).Msg("resolved stack ID")
			return
		}
	}

	log.Warn().Str("stack-name", stackName).Msg("stack not found, entering pending mode")
}

// getEnvironmentID extracts the environment ID from a local stack.
func getEnvironmentID(server *mcp.PortainerMCPServer, stackName string) int {
	stacks, err := server.Client().GetLocalStacks()
	if err != nil {
		return 0
	}
	for _, s := range stacks {
		if s.Name == stackName {
			return s.EndpointID
		}
	}
	return 0
}

// registerWrappedUpstreamTools registers all upstream tools with middleware wrappers.
func registerWrappedUpstreamTools(server *mcp.PortainerMCPServer, wrappers func(string) []func(server.ToolHandlerFunc) server.ToolHandlerFunc) {
	// Local stack tools
	server.AddToolWrapped("listLocalStacks", server.HandleGetLocalStacks(), wrappers("listLocalStacks")...)
	server.AddToolWrapped("getLocalStackFile", server.HandleGetLocalStackFile(), wrappers("getLocalStackFile")...)

	if !server.IsReadOnly() {
		server.AddToolWrapped("createLocalStack", server.HandleCreateLocalStack(), wrappers("createLocalStack")...)
		server.AddToolWrapped("updateLocalStack", server.HandleUpdateLocalStack(), wrappers("updateLocalStack")...)
		server.AddToolWrapped("startLocalStack", server.HandleStartLocalStack(), wrappers("startLocalStack")...)
		server.AddToolWrapped("stopLocalStack", server.HandleStopLocalStack(), wrappers("stopLocalStack")...)
		server.AddToolWrapped("deleteLocalStack", server.HandleDeleteLocalStack(), wrappers("deleteLocalStack")...)
	}

	// Docker proxy
	server.AddToolWrapped("dockerProxy", server.HandleDockerProxy(), wrappers("dockerProxy")...)

	// We intentionally do NOT register environment, team, user, access group,
	// edge stack, kubernetes, tag, or settings tools — they are admin-level
	// operations that should not be available to project-scoped agents.
	// The agent only needs local stack management and Docker proxy.
}
```

Note: this replaces the upstream `AddXxxFeatures()` calls with explicit `AddToolWrapped()` calls, which gives us control over which tools are exposed and wraps each with middleware. Admin tools (environments, teams, users, etc.) are intentionally excluded — they're for the CLI, not for agents.

- [ ] **Step 2: Verify compilation**

```bash
go mod tidy
make build
```

Expected: `dist/towline-mcp` compiles. It now accepts `-stack` and `-tier` flags.

- [ ] **Step 3: Test the binary prints help with missing flags**

```bash
./dist/towline-mcp 2>&1 | head -5
```

Expected: exits with "Both -server and -token flags are required" since no flags provided.

- [ ] **Step 4: Commit**

```bash
git add cmd/towline-mcp/main.go
git commit -m "feat: wire middleware into towline-mcp entry point with stack scoping and tier gating"
```

---

## Chunk 3: Towline Tool Handlers (Core)

Implements the towline-specific MCP tool handlers: service resolution utility, service health, service logs, env get/set, deployments. These all follow the same pattern: parse parameters, resolve service names to container IDs via Docker API labels, execute Docker API calls through the Portainer proxy, and return structured results.

### Task 9: Service name resolution utility

**Files:**
- Create: `internal/towline/resolve.go`
- Create: `internal/towline/resolve_test.go`

All towline tools accept compose service names instead of container IDs. This utility resolves service names to container IDs by querying the Docker API filtered by compose labels.

- [ ] **Step 1: Write failing test**

```go
// internal/towline/resolve_test.go
package towline

import (
	"encoding/json"
	"testing"
)

func TestParseContainersForService(t *testing.T) {
	// Simulate Docker /containers/json response
	containers := []map[string]any{
		{
			"Id": "abc123",
			"Labels": map[string]any{
				"com.docker.compose.project": "myapp-dev",
				"com.docker.compose.service": "app",
			},
			"State": "running",
		},
		{
			"Id": "def456",
			"Labels": map[string]any{
				"com.docker.compose.project": "myapp-dev",
				"com.docker.compose.service": "db",
			},
			"State": "running",
		},
		{
			"Id": "ghi789",
			"Labels": map[string]any{
				"com.docker.compose.project": "other-stack",
				"com.docker.compose.service": "app",
			},
			"State": "running",
		},
	}

	data, _ := json.Marshal(containers)
	result, err := parseContainersForStack(data, "myapp-dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(result))
	}

	appContainers := filterByService(result, "app")
	if len(appContainers) != 1 {
		t.Fatalf("expected 1 app container, got %d", len(appContainers))
	}
	if appContainers[0].ID != "abc123" {
		t.Errorf("expected container ID abc123, got %s", appContainers[0].ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/towline/ -run TestParseContainers
```

Expected: FAIL.

- [ ] **Step 3: Implement service resolution**

```go
// internal/towline/resolve.go
package towline

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/changethisusername/towline/pkg/portainer/models"
)

// ContainerInfo holds resolved container information.
type ContainerInfo struct {
	ID      string
	Service string
	State   string
	Status  string
	Names   []string
}

// ProxyFunc calls the Portainer Docker proxy and returns the response body.
type ProxyFunc func(opts models.DockerProxyRequestOptions) ([]byte, error)

// parseContainersForStack parses a Docker /containers/json response and filters by stack name.
func parseContainersForStack(data []byte, stackName string) ([]ContainerInfo, error) {
	var containers []map[string]any
	if err := json.Unmarshal(data, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse container list: %w", err)
	}

	var result []ContainerInfo
	for _, c := range containers {
		labels, _ := c["Labels"].(map[string]any)
		if labels == nil {
			continue
		}
		project, _ := labels["com.docker.compose.project"].(string)
		if project != stackName {
			continue
		}
		service, _ := labels["com.docker.compose.service"].(string)
		state, _ := c["State"].(string)
		status, _ := c["Status"].(string)
		id, _ := c["Id"].(string)

		var names []string
		if rawNames, ok := c["Names"].([]any); ok {
			for _, n := range rawNames {
				if s, ok := n.(string); ok {
					names = append(names, strings.TrimPrefix(s, "/"))
				}
			}
		}

		result = append(result, ContainerInfo{
			ID:      id,
			Service: service,
			State:   state,
			Status:  status,
			Names:   names,
		})
	}

	return result, nil
}

// filterByService returns only containers matching the given service name.
func filterByService(containers []ContainerInfo, service string) []ContainerInfo {
	var result []ContainerInfo
	for _, c := range containers {
		if c.Service == service {
			result = append(result, c)
		}
	}
	return result
}

// ResolveService queries Docker API to find container(s) for a service in the stack.
func ResolveService(proxyFn ProxyFunc, envID int, stackName, service string) ([]ContainerInfo, error) {
	body, err := proxyFn(models.DockerProxyRequestOptions{
		EnvironmentID: envID,
		Method:        "GET",
		Path:          "/containers/json",
		QueryParams: map[string]string{
			"all":     "true",
			"filters": fmt.Sprintf(`{"label":["com.docker.compose.project=%s","com.docker.compose.service=%s"]}`, stackName, service),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	return parseContainersForStack(body, stackName)
}

// ResolveAllServices queries Docker API to find all containers in the stack.
func ResolveAllServices(proxyFn ProxyFunc, envID int, stackName string) ([]ContainerInfo, error) {
	body, err := proxyFn(models.DockerProxyRequestOptions{
		EnvironmentID: envID,
		Method:        "GET",
		Path:          "/containers/json",
		QueryParams: map[string]string{
			"all":     "true",
			"filters": fmt.Sprintf(`{"label":["com.docker.compose.project=%s"]}`, stackName),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	return parseContainersForStack(body, stackName)
}

// Note: ProxyFunc is wired in cmd/towline-mcp/main.go using the Portainer client's
// ProxyDockerRequest method (which returns *http.Response). The wiring reads the
// response body and returns []byte.
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -v ./internal/towline/ -run TestParseContainers
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/towline/resolve.go internal/towline/resolve_test.go
git commit -m "feat: add service name resolution utility for towline tools"
```

### Task 10: Implement towline_service_health handler

**Files:**
- Create: `internal/towline/health.go`
- Create: `internal/towline/health_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/towline/health_test.go
package towline

import (
	"encoding/json"
	"testing"
)

func TestParseHealthFromStats(t *testing.T) {
	// Simulate Docker /containers/{id}/stats?stream=false response
	statsJSON := `{
		"cpu_stats": {
			"cpu_usage": {"total_usage": 100000000},
			"system_cpu_usage": 10000000000,
			"online_cpus": 4
		},
		"precpu_stats": {
			"cpu_usage": {"total_usage": 90000000},
			"system_cpu_usage": 9000000000
		},
		"memory_stats": {
			"usage": 134217728,
			"limit": 536870912
		}
	}`

	health, err := parseContainerStats([]byte(statsJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if health.MemoryUsageMB < 127 || health.MemoryUsageMB > 129 {
		t.Errorf("expected ~128 MB memory, got %.1f", health.MemoryUsageMB)
	}
	if health.MemoryLimitMB < 511 || health.MemoryLimitMB > 513 {
		t.Errorf("expected ~512 MB memory limit, got %.1f", health.MemoryLimitMB)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/towline/ -run TestParseHealth
```

- [ ] **Step 3: Implement health handler**

```go
// internal/towline/health.go
package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
)

// ServiceHealth represents the health of a single service.
type ServiceHealth struct {
	Service       string   `json:"service"`
	State         string   `json:"state"`
	Uptime        string   `json:"uptime,omitempty"`
	RestartCount  int      `json:"restartCount"`
	CpuPercent    float64  `json:"cpuPercent"`
	MemoryUsageMB float64  `json:"memoryUsageMB"`
	MemoryLimitMB float64  `json:"memoryLimitMB"`
	ExitCode      *int     `json:"exitCode"`
}

type containerStats struct {
	CpuPercent    float64
	MemoryUsageMB float64
	MemoryLimitMB float64
}

func parseContainerStats(data []byte) (*containerStats, error) {
	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
			OnlineCPUs     int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Limit uint64 `json:"limit"`
		} `json:"memory_stats"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse stats: %w", err)
	}

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	cpuPercent := 0.0
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(raw.CPUStats.OnlineCPUs) * 100.0
	}

	return &containerStats{
		CpuPercent:    cpuPercent,
		MemoryUsageMB: float64(raw.MemoryStats.Usage) / 1024 / 1024,
		MemoryLimitMB: float64(raw.MemoryStats.Limit) / 1024 / 1024,
	}, nil
}

// HandleServiceHealth returns the handler for towline_service_health.
func (h *Handlers) HandleServiceHealth() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)
		service, _ := parser.GetString("service", false)

		var containers []ContainerInfo
		var err error

		if service != "" {
			containers, err = ResolveService(h.proxyFn, h.EnvID, h.StackName, service)
		} else {
			containers, err = ResolveAllServices(h.proxyFn, h.EnvID, h.StackName)
		}
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to list containers", err), nil
		}

		var results []ServiceHealth
		for _, c := range containers {
			health := ServiceHealth{
				Service: c.Service,
				State:   c.State,
			}

			if c.State == "running" {
				// Get stats
				statsBody, err := h.proxyFn(models.DockerProxyRequestOptions{
					EnvironmentID: h.EnvID,
					Method:        "GET",
					Path:          fmt.Sprintf("/containers/%s/stats", c.ID),
					QueryParams:   map[string]string{"stream": "false"},
				})
				if err == nil {
					stats, err := parseContainerStats(statsBody)
					if err == nil {
						health.CpuPercent = stats.CpuPercent
						health.MemoryUsageMB = stats.MemoryUsageMB
						health.MemoryLimitMB = stats.MemoryLimitMB
					}
				}

				// Get inspect for restart count, uptime, exit code
				inspectBody, err := h.proxyFn(models.DockerProxyRequestOptions{
					EnvironmentID: h.EnvID,
					Method:        "GET",
					Path:          fmt.Sprintf("/containers/%s/json", c.ID),
				})
				if err == nil {
					health.RestartCount, health.Uptime, health.ExitCode = parseInspectInfo(inspectBody)
				}
			}

			results = append(results, health)
		}

		data, _ := json.Marshal(results)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func parseInspectInfo(data []byte) (restartCount int, uptime string, exitCode *int) {
	var inspect struct {
		RestartCount int `json:"RestartCount"`
		State        struct {
			StartedAt  string `json:"StartedAt"`
			FinishedAt string `json:"FinishedAt"`
			ExitCode   int    `json:"ExitCode"`
			Running    bool   `json:"Running"`
		} `json:"State"`
	}

	if err := json.Unmarshal(data, &inspect); err != nil {
		return 0, "", nil
	}

	restartCount = inspect.RestartCount

	if inspect.State.Running {
		started, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt)
		if err == nil {
			uptime = time.Since(started).Round(time.Second).String()
		}
	} else {
		exitCode = &inspect.State.ExitCode
	}

	return restartCount, uptime, exitCode
}
```

- [ ] **Step 4: Update Handlers struct to hold proxyFn**

Update `internal/towline/towline.go`:

```go
// internal/towline/towline.go
package towline

import (
	"github.com/changethisusername/towline/internal/mcp"
)

// Handlers holds dependencies for towline-specific tool handlers.
type Handlers struct {
	Server    *mcp.PortainerMCPServer
	StackName string
	EnvID     int
	proxyFn   ProxyFunc
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(server *mcp.PortainerMCPServer, stackName string, envID int, proxyFn ProxyFunc) *Handlers {
	return &Handlers{
		Server:    server,
		StackName: stackName,
		EnvID:     envID,
		proxyFn:   proxyFn,
	}
}
```

- [ ] **Step 5: Run tests**

```bash
go test -v ./internal/towline/ -run TestParseHealth
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/towline/
git commit -m "feat: implement towline_service_health handler"
```

### Task 11: Implement towline_service_logs handler

**Files:**
- Create: `internal/towline/logs.go`

- [ ] **Step 1: Implement logs handler**

```go
// internal/towline/logs.go
package towline

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
)

// HandleServiceLogs returns the handler for towline_service_logs.
func (h *Handlers) HandleServiceLogs() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		service, err := parser.GetString("service", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("service is required", err), nil
		}

		tail, _ := parser.GetInt("tail", false)
		if tail <= 0 {
			tail = 200
		}

		since, _ := parser.GetString("since", false)
		filter, _ := parser.GetString("filter", false)

		containers, err := ResolveService(h.proxyFn, h.EnvID, h.StackName, service)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to resolve service", err), nil
		}
		if len(containers) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("no containers found for service '%s'", service)), nil
		}

		// Get logs from the first container (primary replica)
		c := containers[0]
		queryParams := map[string]string{
			"stdout": "true",
			"stderr": "true",
			"tail":   fmt.Sprintf("%d", tail),
		}
		if since != "" {
			queryParams["since"] = since
		}

		body, err := h.proxyFn(models.DockerProxyRequestOptions{
			EnvironmentID: h.EnvID,
			Method:        "GET",
			Path:          fmt.Sprintf("/containers/%s/logs", c.ID),
			QueryParams:   queryParams,
		})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get logs", err), nil
		}

		// Strip Docker multiplexed stream headers (8-byte prefix per frame)
		logText := stripDockerLogHeaders(body)

		// Apply filter if specified
		if filter != "" {
			var filtered []string
			for _, line := range strings.Split(logText, "\n") {
				if strings.Contains(line, filter) {
					filtered = append(filtered, line)
				}
			}
			logText = strings.Join(filtered, "\n")
		}

		return mcp.NewToolResultText(logText), nil
	}
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream header from each frame.
// Docker log output format: [stream_type(1)][0][0][0][size(4)][payload(size)]
func stripDockerLogHeaders(data []byte) string {
	var result bytes.Buffer
	buf := bytes.NewReader(data)

	for buf.Len() > 0 {
		// Read 8-byte header
		header := make([]byte, 8)
		n, err := buf.Read(header)
		if err != nil || n < 8 {
			// Not a multiplexed stream, return raw
			result.Write(header[:n])
			remaining, _ := io.ReadAll(buf)
			result.Write(remaining)
			break
		}

		// Extract frame size from bytes 4-7
		frameSize := binary.BigEndian.Uint32(header[4:8])
		if frameSize == 0 {
			continue
		}

		// Read frame payload
		frame := make([]byte, frameSize)
		n, err = buf.Read(frame)
		result.Write(frame[:n])
	}

	return result.String()
}
```

Note: need to add `"io"` to the imports for `io.ReadAll`. Let me fix that — actually the `io` import is already used by `bytes.NewReader`. The function uses `buf` which is a `*bytes.Reader`, not `io.Reader`, but `io.ReadAll` accepts any `io.Reader`.

- [ ] **Step 2: Verify compilation**

```bash
make build
```

- [ ] **Step 3: Commit**

```bash
git add internal/towline/logs.go
git commit -m "feat: implement towline_service_logs handler with Docker log header stripping"
```

### Task 12: Implement towline_env_get and towline_env_set handlers

**Files:**
- Create: `internal/towline/env.go`
- Create: `internal/towline/env_test.go`

- [ ] **Step 1: Write failing test for env masking**

```go
// internal/towline/env_test.go
package towline

import (
	"testing"
)

func TestShouldMaskEnvVar(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"DATABASE_URL", false},
		{"API_KEY", true},
		{"api_key", true},
		{"DB_PASSWORD", true},
		{"db_password", true},
		{"SECRET_VALUE", true},
		{"AUTH_TOKEN", true},
		{"APP_NAME", false},
		{"PORT", false},
		{"POSTGRES_PASSWORD", true},
		{"AWS_SECRET_ACCESS_KEY", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldMaskEnvVar(tt.name); got != tt.expected {
				t.Errorf("shouldMaskEnvVar(%q) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/towline/ -run TestShouldMask
```

- [ ] **Step 3: Implement env handlers**

```go
// internal/towline/env.go
package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
)

var sensitivePatterns = []string{"KEY", "SECRET", "PASSWORD", "TOKEN"}

// shouldMaskEnvVar returns true if the variable name suggests it's sensitive.
func shouldMaskEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// HandleEnvGet returns the handler for towline_env_get.
func (h *Handlers) HandleEnvGet() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)
		name, _ := parser.GetString("name", false)

		// Get stack env vars via Portainer local stack API
		stacks, err := h.Server.Client().GetLocalStacks()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get stacks", err), nil
		}

		var targetStack *models.LocalStack
		for _, s := range stacks {
			if s.Name == h.StackName {
				s := s // capture loop var
				targetStack = &s
				break
			}
		}

		if targetStack == nil {
			return mcp.NewToolResultError(fmt.Sprintf("stack '%s' not found", h.StackName)), nil
		}

		envMap := make(map[string]string)
		for _, env := range targetStack.Env {
			// Skip internal towline vars
			if strings.HasPrefix(env.Name, "_TOWLINE_") {
				continue
			}
			if name != "" && env.Name != name {
				continue
			}
			value := env.Value
			if shouldMaskEnvVar(env.Name) {
				value = "****"
			}
			envMap[env.Name] = value
		}

		data, _ := json.Marshal(envMap)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// HandleEnvSet returns the handler for towline_env_set.
func (h *Handlers) HandleEnvSet() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)

		name, err := parser.GetString("name", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("name is required", err), nil
		}
		value, err := parser.GetString("value", true)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("value is required", err), nil
		}

		// Get current stack
		stacks, err := h.Server.Client().GetLocalStacks()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get stacks", err), nil
		}

		var targetStack *models.LocalStack
		for _, s := range stacks {
			if s.Name == h.StackName {
				s := s
				targetStack = &s
				break
			}
		}

		if targetStack == nil {
			return mcp.NewToolResultError(fmt.Sprintf("stack '%s' not found", h.StackName)), nil
		}

		// Update or add the env var
		found := false
		for i, env := range targetStack.Env {
			if env.Name == name {
				targetStack.Env[i].Value = value
				found = true
				break
			}
		}
		if !found {
			targetStack.Env = append(targetStack.Env, models.LocalStackEnvVar{Name: name, Value: value})
		}

		// Get current compose to preserve it
		composeFile, err := h.Server.Client().GetLocalStackFile(targetStack.ID)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get compose file", err), nil
		}

		// Update the stack (env only, compose unchanged)
		err = h.Server.Client().UpdateLocalStack(targetStack.ID, targetStack.EndpointID, composeFile, targetStack.Env, false, false)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to update stack env", err), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Environment variable '%s' set successfully. A service restart may be needed to pick up the change.", name)), nil
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test -v ./internal/towline/ -run TestShouldMask
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/towline/env.go internal/towline/env_test.go
git commit -m "feat: implement towline_env_get and towline_env_set handlers"
```

### Task 13: Implement towline_deployments handler

**Files:**
- Create: `internal/towline/deployments.go`
- Create: `internal/towline/deployments_test.go`

- [ ] **Step 1: Write failing test for deployment log parsing**

```go
// internal/towline/deployments_test.go
package towline

import (
	"encoding/json"
	"testing"
)

func TestParseDeploymentLog(t *testing.T) {
	logJSON := `[
		{"id":"d_001","timestamp":"2026-03-16T14:00:00Z","description":"Initial deploy","diff":"","outcome":"success"},
		{"id":"d_002","timestamp":"2026-03-16T14:30:00Z","description":"Added redis","diff":"+  redis:","outcome":"success"}
	]`

	entries, err := parseDeploymentLog(logJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Description != "Added redis" {
		t.Errorf("expected 'Added redis', got '%s'", entries[1].Description)
	}
}

func TestDeploymentLogCapping(t *testing.T) {
	var entries []DeploymentEntry
	for i := 0; i < 55; i++ {
		entries = append(entries, DeploymentEntry{ID: fmt.Sprintf("d_%03d", i)})
	}
	capped := capDeploymentLog(entries, 50)
	if len(capped) != 50 {
		t.Fatalf("expected 50 entries, got %d", len(capped))
	}
	// Should keep the most recent (last) 50
	if capped[0].ID != "d_005" {
		t.Errorf("expected first entry d_005, got %s", capped[0].ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/towline/ -run TestParseDeploy
```

- [ ] **Step 3: Implement deployments handler**

```go
// internal/towline/deployments.go
package towline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/changethisusername/towline/pkg/toolgen"
)

const (
	deploymentEnvVar = "_TOWLINE_DEPLOYMENTS"
	maxDeployments   = 50
)

// DeploymentEntry represents a single deployment in the log.
type DeploymentEntry struct {
	ID          string `json:"id"`
	Timestamp   string `json:"timestamp"`
	Description string `json:"description"`
	Diff        string `json:"diff"`
	Outcome     string `json:"outcome"`
}

func parseDeploymentLog(logJSON string) ([]DeploymentEntry, error) {
	if logJSON == "" {
		return nil, nil
	}
	var entries []DeploymentEntry
	if err := json.Unmarshal([]byte(logJSON), &entries); err != nil {
		return nil, fmt.Errorf("failed to parse deployment log: %w", err)
	}
	return entries, nil
}

func capDeploymentLog(entries []DeploymentEntry, max int) []DeploymentEntry {
	if len(entries) <= max {
		return entries
	}
	return entries[len(entries)-max:]
}

func generateDeploymentID() string {
	b := make([]byte, 3)
	rand.Read(b)
	return "d_" + hex.EncodeToString(b)
}

// HandleDeployments returns the handler for towline_deployments.
func (h *Handlers) HandleDeployments() server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		parser := toolgen.NewParameterParser(request)
		limit, _ := parser.GetInt("limit", false)
		if limit <= 0 {
			limit = 10
		}

		log, err := h.readDeploymentLog()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to read deployment log", err), nil
		}

		// Return most recent N entries
		if len(log) > limit {
			log = log[len(log)-limit:]
		}

		data, _ := json.Marshal(log)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// readDeploymentLog reads the deployment log from the stack's env vars.
func (h *Handlers) readDeploymentLog() ([]DeploymentEntry, error) {
	stacks, err := h.Server.Client().GetLocalStacks()
	if err != nil {
		return nil, err
	}

	for _, s := range stacks {
		if s.Name == h.StackName {
			for _, env := range s.Env {
				if env.Name == deploymentEnvVar {
					return parseDeploymentLog(env.Value)
				}
			}
			return nil, nil // No deployment log yet
		}
	}

	return nil, fmt.Errorf("stack '%s' not found", h.StackName)
}

// RecordDeployment adds an entry to the deployment log.
// Called by middleware after successful stack updates.
func (h *Handlers) RecordDeployment(description, previousCompose, newCompose string, outcome string) error {
	entries, _ := h.readDeploymentLog()

	diff := computeSimpleDiff(previousCompose, newCompose)

	entry := DeploymentEntry{
		ID:          generateDeploymentID(),
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Description: description,
		Diff:        diff,
		Outcome:     outcome,
	}

	entries = append(entries, entry)
	entries = capDeploymentLog(entries, maxDeployments)

	return h.writeDeploymentLog(entries)
}

func (h *Handlers) writeDeploymentLog(entries []DeploymentEntry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}

	// Get current stack
	stacks, err := h.Server.Client().GetLocalStacks()
	if err != nil {
		return err
	}

	for _, s := range stacks {
		if s.Name == h.StackName {
			// Update or add the deployment env var
			found := false
			for i, env := range s.Env {
				if env.Name == deploymentEnvVar {
					s.Env[i].Value = string(data)
					found = true
					break
				}
			}
			if !found {
				s.Env = append(s.Env, models.LocalStackEnvVar{Name: deploymentEnvVar, Value: string(data)})
			}

			// Get current compose to preserve
			compose, err := h.Server.Client().GetLocalStackFile(s.ID)
			if err != nil {
				return err
			}

			return h.Server.Client().UpdateLocalStack(s.ID, s.EndpointID, compose, s.Env, false, false)
		}
	}

	return fmt.Errorf("stack '%s' not found", h.StackName)
}

// computeSimpleDiff produces a basic unified-style diff between two strings.
func computeSimpleDiff(old, new string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	var diff []string
	// Simple line-by-line comparison (not a real unified diff algorithm,
	// but sufficient for compose files which are typically short)
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}

		if oldLine != newLine {
			if oldLine != "" {
				diff = append(diff, "- "+oldLine)
			}
			if newLine != "" {
				diff = append(diff, "+ "+newLine)
			}
		}
	}

	return strings.Join(diff, "\n")
}
```

- [ ] **Step 4: Fix test — add missing fmt import**

The test uses `fmt.Sprintf` so needs `"fmt"` import. Update the test file to include it.

- [ ] **Step 5: Run tests**

```bash
go test -v ./internal/towline/ -run "TestParseDeploy|TestDeploymentLog"
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/towline/deployments.go internal/towline/deployments_test.go
git commit -m "feat: implement towline_deployments handler with stack env var storage"
```

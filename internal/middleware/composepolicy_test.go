package middleware

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	towline "github.com/changethisusername/towline"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCompose(t *testing.T) {
	tests := []struct {
		name    string
		compose string
		wantErr string // substring of a violation; empty means allowed
	}{
		{
			name: "named volumes and ports are allowed",
			compose: `services:
  web:
    image: nginx
    ports: ["80:80"]
    volumes:
      - data:/data
      - /anonymous
      - type: volume
        source: data
        target: /more
      - type: tmpfs
        target: /tmp
volumes:
  data:
`,
		},
		{name: "privileged true", compose: "services:\n  a:\n    image: x\n    privileged: true\n", wantErr: "privileged"},
		{name: "privileged interpolated", compose: "services:\n  a:\n    image: x\n    privileged: ${P}\n", wantErr: "privileged"},
		{name: "privileged false allowed", compose: "services:\n  a:\n    image: x\n    privileged: false\n"},
		{name: "host network", compose: "services:\n  a:\n    image: x\n    network_mode: host\n", wantErr: "network_mode"},
		{name: "container network", compose: "services:\n  a:\n    image: x\n    network_mode: \"container:abc\"\n", wantErr: "network_mode"},
		{name: "bridge network allowed", compose: "services:\n  a:\n    image: x\n    network_mode: bridge\n"},
		{name: "host pid", compose: "services:\n  a:\n    image: x\n    pid: host\n", wantErr: "pid"},
		{name: "host ipc", compose: "services:\n  a:\n    image: x\n    ipc: host\n", wantErr: "ipc"},
		{name: "host userns", compose: "services:\n  a:\n    image: x\n    userns_mode: host\n", wantErr: "userns_mode"},
		{name: "cap_add", compose: "services:\n  a:\n    image: x\n    cap_add: [SYS_ADMIN]\n", wantErr: "cap_add"},
		{name: "devices", compose: "services:\n  a:\n    image: x\n    devices: [\"/dev/sda:/dev/sda\"]\n", wantErr: "devices"},
		{name: "seccomp unconfined", compose: "services:\n  a:\n    image: x\n    security_opt: [\"seccomp:unconfined\"]\n", wantErr: "security_opt"},
		{name: "root bind mount", compose: "services:\n  a:\n    image: x\n    volumes: [\"/:/host\"]\n", wantErr: "bind mount"},
		{name: "docker socket", compose: "services:\n  a:\n    image: x\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock\"]\n", wantErr: "bind mount"},
		{name: "relative bind mount", compose: "services:\n  a:\n    image: x\n    volumes: [\"./data:/data\"]\n", wantErr: "bind mount"},
		{name: "home bind mount", compose: "services:\n  a:\n    image: x\n    volumes: [\"~/x:/data\"]\n", wantErr: "bind mount"},
		{name: "interpolated bind mount", compose: "services:\n  a:\n    image: x\n    volumes: [\"${SRC}:/data\"]\n", wantErr: "bind mount"},
		{name: "long syntax bind", compose: "services:\n  a:\n    image: x\n    volumes:\n      - type: bind\n        source: /\n        target: /host\n", wantErr: "volume type"},
		{name: "long syntax volume with path source", compose: "services:\n  a:\n    image: x\n    volumes:\n      - type: volume\n        source: /etc\n        target: /host\n", wantErr: "volume source"},
		{name: "volumes_from", compose: "services:\n  a:\n    image: x\n    volumes_from: [other]\n", wantErr: "volumes_from"},
		{name: "local driver bind via driver_opts", compose: "services:\n  a:\n    image: x\nvolumes:\n  v:\n    driver_opts:\n      type: none\n      o: bind\n      device: /\n", wantErr: "driver_opts"},
		{name: "absolute env_file", compose: "services:\n  a:\n    image: x\n    env_file: /data/portainer.key\n", wantErr: "env_file"},
		{name: "parent env_file", compose: "services:\n  a:\n    image: x\n    env_file: [\"../../portainer.db\"]\n", wantErr: "env_file"},
		{name: "relative env_file allowed", compose: "services:\n  a:\n    image: x\n    env_file: [.env]\n"},
		{name: "secret from host file", compose: "services:\n  a:\n    image: x\nsecrets:\n  s:\n    file: /data/portainer.key\n", wantErr: "file sources"},
		{name: "local build context", compose: "services:\n  a:\n    build: .\n", wantErr: "build context"},
		{name: "root build context", compose: "services:\n  a:\n    build:\n      context: /\n", wantErr: "build context"},
		{name: "remote build allowed", compose: "services:\n  a:\n    build:\n      context: https://github.com/org/repo.git#main\n      dockerfile: Dockerfile\n"},
		{name: "build host network", compose: "services:\n  a:\n    build:\n      context: https://github.com/org/repo.git\n      network: host\n", wantErr: "network"},
		{name: "external_links", compose: "services:\n  a:\n    image: x\n    external_links: [victim_db]\n", wantErr: "external_links"},
		{name: "merge key privileged", compose: "x-base: &b\n  privileged: true\nservices:\n  a:\n    <<: *b\n    image: x\n", wantErr: "privileged"},
		{name: "include", compose: "include:\n  - /etc/compose.yml\nservices: {}\n", wantErr: "include"},
		{name: "invalid yaml", compose: "services: [", wantErr: "not valid YAML"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := ValidateCompose(tt.compose)
			if tt.wantErr == "" {
				assert.Empty(t, violations)
				return
			}
			require.NotEmpty(t, violations)
			assert.Contains(t, strings.Join(violations, "\n"), tt.wantErr)
		})
	}
}

// TestValidateCompose_BundledTemplates ensures every compose file we ship
// passes the policy the agent is held to.
func TestValidateCompose_BundledTemplates(t *testing.T) {
	err := fs.WalkDir(towline.EmbeddedTemplates, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".yml") {
			return nil
		}
		data, err := fs.ReadFile(towline.EmbeddedTemplates, p)
		require.NoError(t, err)
		assert.Empty(t, ValidateCompose(string(data)), p)
		return nil
	})
	require.NoError(t, err)
}

func TestComposePolicyMiddleware(t *testing.T) {
	called := false
	next := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}

	handler := NewComposePolicy("updateLocalStack")(next)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file": "services:\n  a:\n    image: x\n    privileged: true\n"}
	result, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.False(t, called)

	req.Params.Arguments = map[string]any{"file": "services:\n  a:\n    image: x\n"}
	result, err = handler(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.True(t, called)

	// Other tools pass straight through.
	called = false
	_, _ = NewComposePolicy("listLocalStacks")(next)(context.Background(), mcp.CallToolRequest{})
	assert.True(t, called)
}

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
		{name: "long syntax bind", compose: "services:\n  a:\n    image: x\n    volumes:\n      - type: bind\n        source: /\n        target: /host\n", wantErr: "bind mount of"},
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
		{name: "empty file allowed", compose: ""},
		{name: "leading document marker allowed", compose: "---\nservices:\n  a:\n    image: x\n"},
		{name: "second document", compose: "services:\n  a:\n    image: x\n---\nservices:\n  a:\n    privileged: true\n", wantErr: "single YAML document"},
		{name: "trailing empty document", compose: "services:\n  a:\n    image: x\n---\n", wantErr: "single YAML document"},
		{name: "invalid second document", compose: "services: {}\n---\nservices: [\n", wantErr: "single YAML document"},
		{name: "external volume", compose: "services:\n  a:\n    image: x\n    volumes: [\"v:/d\"]\nvolumes:\n  v:\n    external: true\n", wantErr: `volume "v": "external" is not allowed`},
		{name: "external volume false", compose: "services:\n  a:\n    image: x\nvolumes:\n  v:\n    external: false\n", wantErr: `"external" is not allowed`},
		{name: "named volume of another project", compose: "services:\n  a:\n    image: x\n    volumes: [\"v:/d\"]\nvolumes:\n  v:\n    name: otherapp_db_data\n", wantErr: `volume "v": "name" is not allowed`},
		{name: "local volume driver allowed", compose: "services:\n  a:\n    image: x\nvolumes:\n  v:\n    driver: local\n  w: {}\n"},
		{name: "external network", compose: "services:\n  a:\n    image: x\n    networks: [n]\nnetworks:\n  n:\n    external: true\n", wantErr: `network "n": "external" is not allowed`},
		{name: "named network of another project", compose: "services:\n  a:\n    image: x\nnetworks:\n  n:\n    name: otherapp_default\n", wantErr: `network "n": "name" is not allowed`},
		{name: "network driver_opts", compose: "services:\n  a:\n    image: x\nnetworks:\n  n:\n    driver_opts:\n      parent: eth0\n", wantErr: "driver_opts"},
		{name: "host network driver", compose: "services:\n  a:\n    image: x\nnetworks:\n  n:\n    driver: host\n", wantErr: "only the bridge and overlay"},
		{name: "macvlan network driver", compose: "services:\n  a:\n    image: x\nnetworks:\n  n:\n    driver: macvlan\n", wantErr: "only the bridge and overlay"},
		{name: "bridge networks allowed", compose: "services:\n  a:\n    image: x\n    networks: [front, back]\nnetworks:\n  front:\n  back:\n    driver: bridge\n  swarm:\n    driver: overlay\n"},
		{name: "network_mode none allowed", compose: "services:\n  a:\n    image: x\n    network_mode: none\n"},
		{name: "network_mode other network", compose: "services:\n  a:\n    image: x\n    network_mode: otherapp_default\n", wantErr: `network_mode "otherapp_default" is not allowed`},
		{name: "network_mode service", compose: "services:\n  a:\n    image: x\n    network_mode: \"service:b\"\n", wantErr: "network_mode"},
		{name: "network_mode interpolated", compose: "services:\n  a:\n    image: x\n    network_mode: ${MODE}\n", wantErr: "network_mode"},
		{name: "network_mode non-string", compose: "services:\n  a:\n    image: x\n    network_mode: [host]\n", wantErr: "network_mode"},
		{name: "privileged post_start hook", compose: "services:\n  a:\n    image: x\n    post_start:\n      - command: id\n        privileged: true\n", wantErr: "post_start hook privileged mode is not allowed"},
		{name: "privileged pre_stop hook", compose: "services:\n  a:\n    image: x\n    pre_stop:\n      - command: id\n        privileged: ${P}\n", wantErr: "pre_stop hook privileged mode is not allowed"},
		{name: "unprivileged hooks allowed", compose: "services:\n  a:\n    image: x\n    post_start:\n      - command: ./init.sh\n        user: root\n    pre_stop:\n      - command: ./drain.sh\n        privileged: false\n"},
		{name: "hook not a list", compose: "services:\n  a:\n    image: x\n    post_start: {command: id, privileged: true}\n", wantErr: "post_start must be a list"},
		{name: "gpu reservation allowed", compose: "services:\n  a:\n    image: x\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [gpu]\n              count: all\n            - driver: nvidia\n              device_ids: [\"0\"]\n              capabilities: [gpu, compute, utility]\n"},
		{name: "cdi device reservation", compose: "services:\n  a:\n    image: x\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - driver: cdi\n              device_ids: [\"vendor.com/device=all\"]\n              capabilities: [gpu]\n", wantErr: `driver "cdi"`},
		{name: "device reservation options", compose: "services:\n  a:\n    image: x\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [gpu]\n              options: {x: y}\n", wantErr: `"options"`},
		{name: "device reservation other capability", compose: "services:\n  a:\n    image: x\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [tpu]\n", wantErr: `capability "tpu"`},
		{name: "device reservation without capabilities", compose: "services:\n  a:\n    image: x\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - count: 1\n", wantErr: "deploy.resources.reservations.devices"},
		{name: "device reservation not a list", compose: "services:\n  a:\n    image: x\n    deploy:\n      resources:\n        reservations:\n          devices: /dev/sda\n", wantErr: "deploy.resources.reservations.devices value"},
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

	handler := NewComposePolicy("updateLocalStack", ComposePolicy{})(next)
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
	_, _ = NewComposePolicy("listLocalStacks", ComposePolicy{})(next)(context.Background(), mcp.CallToolRequest{})
	assert.True(t, called)
}

func TestComposePolicy_AllowBindMounts(t *testing.T) {
	policy := ComposePolicy{AllowBindMounts: []string{"/srv/app", "."}}
	tests := []struct {
		name    string
		volume  string
		allowed bool
	}{
		{"exact allowed path", "/srv/app:/data", true},
		{"below allowed path", "/srv/app/uploads:/data", true},
		{"sibling with shared prefix", "/srv/application:/data", false},
		{"escape via dot-dot", "/srv/app/../../etc:/data", false},
		{"other host path", "/etc:/data", false},
		{"relative inside stack", "./config:/config", true},
		{"relative escape", "../../data:/data", false},
		{"interpolated", "${SRC}:/data", false},
		{"docker socket", "/var/run/docker.sock:/var/run/docker.sock", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compose := "services:\n  a:\n    image: x\n    volumes: [\"" + tt.volume + "\"]\n"
			assert.Equal(t, tt.allowed, len(policy.Validate(compose)) == 0, policy.Validate(compose))
		})
	}

	long := "services:\n  a:\n    image: x\n    volumes:\n      - type: bind\n        source: /srv/app\n        target: /d\n"
	assert.Empty(t, policy.Validate(long))

	// Other rules still apply when bind mounts are allowed.
	assert.NotEmpty(t, policy.Validate("services:\n  a:\n    image: x\n    privileged: true\n"))
}

func TestComposePolicy_Disabled(t *testing.T) {
	called := false
	handler := NewComposePolicy("updateLocalStack", ComposePolicy{Disabled: true})(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"file": "services:\n  a:\n    image: x\n    privileged: true\n"}
	_, err := handler(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestBindMountSources(t *testing.T) {
	compose := `services:
  b:
    image: x
    volumes:
      - data:/data
      - /srv/a:/a
      - type: bind
        source: ./cfg
        target: /cfg
  a:
    image: x
    volumes: ["/srv/a:/again", "/var/run/docker.sock:/var/run/docker.sock", "/anon"]
`
	assert.Equal(t, []string{"/srv/a", "/var/run/docker.sock", "./cfg"}, BindMountSources(compose))
	assert.Nil(t, BindMountSources("services: ["))
}

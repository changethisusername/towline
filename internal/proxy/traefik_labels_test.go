package proxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertInOrder checks that each substring appears in s after the previous one.
func assertInOrder(t *testing.T, s string, parts ...string) {
	t.Helper()
	pos := 0
	for _, p := range parts {
		i := strings.Index(s[pos:], p)
		if !assert.GreaterOrEqual(t, i, 0, "%q not found (in order) in:\n%s", p, s) {
			return
		}
		pos += i + len(p)
	}
}

func TestTraefikManager_Add_Labels(t *testing.T) {
	tests := []struct {
		name        string
		compose     string
		domain      string
		port        int
		wantErr     string
		wantOrder   []string
		wantContain []string
		wantAbsent  []string
	}{
		{
			name: "list labels keep order and entries without '='",
			compose: `services:
  web:
    image: nginx
    labels:
      - "com.example.z=1"
      - "com.example.flag"
      - "com.example.a=2"
`,
			domain:    "example.com",
			port:      80,
			wantOrder: []string{"com.example.z=1", "com.example.flag", "com.example.a=2", "traefik.enable=true", "traefik.http.routers.mystack-web.rule=Host(`example.com`)", "loadbalancer.server.port=80"},
		},
		{
			name: "map labels stay a map in order",
			compose: `services:
  web:
    image: nginx
    labels:
      com.example.z: "1"
      com.example.a: "2"
`,
			domain:    "example.com",
			port:      8080,
			wantOrder: []string{"com.example.z: \"1\"", "com.example.a: \"2\"", "traefik.enable: \"true\"", "traefik.http.routers.mystack-web.rule: Host(`example.com`)", "traefik.http.services.mystack-web.loadbalancer.server.port: \"8080\""},
		},
		{
			name: "existing traefik labels are edited in place",
			compose: `services:
  web:
    image: nginx
    labels:
      - "traefik.enable=false"
      - "com.example.keep=1"
      - "traefik.http.services.mystack-web.loadbalancer.server.port=3000"
`,
			domain:     "example.com",
			port:       80,
			wantOrder:  []string{"traefik.enable=true", "com.example.keep=1", "traefik.http.services.mystack-web.loadbalancer.server.port=80", "traefik.http.routers.mystack-web.rule"},
			wantAbsent: []string{"traefik.enable=false", "port=3000"},
		},
		{
			name: "comments are preserved",
			compose: `services:
  web:
    image: nginx
    labels:
      # keep me
      - "com.example.a=1"
`,
			domain:      "example.com",
			port:        80,
			wantContain: []string{"# keep me", "com.example.a=1"},
		},
		{
			name: "second domain on the same service is refused",
			compose: `services:
  web:
    image: nginx
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mystack-web.rule=Host(` + "`a.example.com`" + `)"
`,
			domain:  "b.example.com",
			port:    80,
			wantErr: "only one domain per service",
		},
		{
			name: "same domain twice is refused",
			compose: `services:
  web:
    image: nginx
    labels:
      - "traefik.http.routers.mystack-web.rule=Host(` + "`a.example.com`" + `)"
`,
			domain:  "A.example.com",
			port:    80,
			wantErr: "already routed",
		},
		{
			name: "domain used by another service is refused",
			compose: `services:
  web:
    image: nginx
  api:
    image: api
    labels:
      - "traefik.http.routers.mystack-api.rule=Host(` + "`a.example.com`" + `)"
`,
			domain:  "a.example.com",
			port:    80,
			wantErr: `already routed to service "api"`,
		},
		{
			name: "labels alias is refused",
			compose: `x-labels: &l
  - "com.example.a=1"
services:
  web:
    image: nginx
    labels: *l
`,
			domain:  "example.com",
			port:    80,
			wantErr: "YAML alias",
		},
		{
			name: "labels inherited through merge key are refused",
			compose: `x-base: &base
  image: nginx
  labels:
    com.example.a: "1"
services:
  web:
    <<: *base
`,
			domain:  "example.com",
			port:    80,
			wantErr: "merge key",
		},
		{
			name: "merge key inside labels map is refused",
			compose: `x-l: &l
  com.example.a: "1"
services:
  web:
    image: nginx
    labels:
      <<: *l
      com.example.b: "2"
`,
			domain:  "example.com",
			port:    80,
			wantErr: "merge key",
		},
		{
			name: "anchored labels reused elsewhere are refused",
			compose: `services:
  web:
    image: nginx
    labels: &l
      - "com.example.a=1"
  api:
    image: api
    labels: *l
`,
			domain:  "example.com",
			port:    80,
			wantErr: "anchored",
		},
		{
			name: "merge key without labels is fine",
			compose: `x-base: &base
  image: nginx
services:
  web:
    <<: *base
`,
			domain:      "example.com",
			port:        80,
			wantContain: []string{"<<: *base", "traefik.enable=true"},
		},
		{
			name: "invalid port",
			compose: `services:
  web:
    image: nginx
`,
			domain:  "example.com",
			port:    70000,
			wantErr: "invalid port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &TraefikManager{StackName: "mystack"}
			out, err := m.Add(tt.compose, "web", tt.domain, tt.port)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assertInOrder(t, out, tt.wantOrder...)
			for _, s := range tt.wantContain {
				assert.Contains(t, out, s)
			}
			for _, s := range tt.wantAbsent {
				assert.NotContains(t, out, s)
			}
		})
	}
}

func TestTraefikManager_Remove_Labels(t *testing.T) {
	routed := `services:
  web:
    image: nginx
    labels:
      - "com.example.z=1"
      - "traefik.enable=true"
      - "traefik.http.routers.mystack-web.rule=Host(` + "`example.com`" + `)"
      - "com.example.flag"
      - "traefik.http.services.mystack-web.loadbalancer.server.port=80"
      - "com.example.a=2"
`
	tests := []struct {
		name        string
		compose     string
		domain      string
		wantErr     string
		wantOrder   []string
		wantAbsent  []string
		wantContain []string
	}{
		{
			name:       "exact domain removes only the router and keeps other labels in order",
			compose:    routed,
			domain:     "example.com",
			wantOrder:  []string{"com.example.z=1", "com.example.flag", "com.example.a=2"},
			wantAbsent: []string{"traefik."},
		},
		{
			name:    "substring of the routed domain is not a match",
			compose: routed,
			domain:  "ample.com",
			wantErr: "not found",
		},
		{
			name:    "superdomain of the routed domain is not a match",
			compose: routed,
			domain:  "www.example.com",
			wantErr: "not found",
		},
		{
			name: "no route is an error, not a redeploy",
			compose: `services:
  web:
    image: nginx
    labels:
      - "com.example.a=1"
`,
			domain:  "example.com",
			wantErr: "nothing to remove",
		},
		{
			name: "rule covering several hosts is refused",
			compose: `services:
  web:
    image: nginx
    labels:
      - "traefik.http.routers.mystack-web.rule=Host(` + "`example.com`" + `) || Host(` + "`www.example.com`" + `)"
`,
			domain:  "example.com",
			wantErr: "not a single Host rule",
		},
		{
			name: "traefik.enable is kept while other traefik routers remain",
			compose: `services:
  web:
    image: nginx
    labels:
      traefik.enable: "true"
      traefik.http.routers.mystack-web.rule: Host(` + "`example.com`" + `)
      traefik.http.routers.custom.rule: Host(` + "`other.example.com`" + `)
`,
			domain:      "example.com",
			wantContain: []string{"traefik.enable", "traefik.http.routers.custom.rule"},
			wantAbsent:  []string{"mystack-web"},
		},
		{
			name: "labels alias is refused",
			compose: `x-labels: &l
  - "traefik.http.routers.mystack-web.rule=Host(` + "`example.com`" + `)"
services:
  web:
    image: nginx
    labels: *l
`,
			domain:  "example.com",
			wantErr: "YAML alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &TraefikManager{StackName: "mystack"}
			out, err := m.Remove(tt.compose, "web", tt.domain)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assertInOrder(t, out, tt.wantOrder...)
			for _, s := range tt.wantContain {
				assert.Contains(t, out, s)
			}
			for _, s := range tt.wantAbsent {
				assert.NotContains(t, out, s)
			}
		})
	}
}

func TestTraefikManager_Remove_LastLabelDropsKey(t *testing.T) {
	tests := []struct {
		name    string
		compose string
	}{
		{
			name: "list form",
			compose: `services:
  web:
    image: nginx
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.mystack-web.rule=Host(` + "`example.com`" + `)"
`,
		},
		{
			name: "map form",
			compose: `services:
  web:
    image: nginx
    labels:
      traefik.enable: true
      traefik.http.routers.mystack-web.rule: Host(` + "`example.com`" + `)
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &TraefikManager{StackName: "mystack"}
			out, err := m.Remove(tt.compose, "web", "example.com")
			require.NoError(t, err)
			assert.NotContains(t, out, "labels")
			assert.Contains(t, out, "image: nginx")
		})
	}
}

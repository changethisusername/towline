package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const caddyTestCompose = `services:
  web:
    image: nginx
  api:
    image: node
`

// fakeCaddy serves GET/POST on the routes endpoint and DELETE on /id/{id}.
type fakeCaddy struct {
	routes   []caddyRoute
	posted   []caddyRoute
	deleted  []string
	postCode int
}

func (f *fakeCaddy) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/config/apps/http/servers/srv0/routes":
			body, _ := json.Marshal(f.routes)
			_, _ = w.Write(body)
		case r.Method == "POST" && r.URL.Path == "/config/apps/http/servers/srv0/routes":
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			if f.postCode != 0 {
				w.WriteHeader(f.postCode)
				_, _ = w.Write([]byte("internal error"))
				return
			}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var route caddyRoute
			require.NoError(t, json.Unmarshal(body, &route))
			f.posted = append(f.posted, route)
		case r.Method == "DELETE":
			f.deleted = append(f.deleted, r.URL.EscapedPath())
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}
	}))
}

func route(id, host, dial string) caddyRoute {
	return caddyRoute{
		ID:     id,
		Match:  []caddyMatch{{Host: []string{host}}},
		Handle: []caddyHandle{{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: dial}}}},
	}
}

func TestCaddyManager_Add(t *testing.T) {
	f := &fakeCaddy{}
	ts := f.server(t)
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	result, err := m.Add(caddyTestCompose, "web", "example.com", 8080)
	require.NoError(t, err)

	// Compose content should be returned unchanged
	assert.Equal(t, caddyTestCompose, result)

	require.Len(t, f.posted, 1)
	r := f.posted[0]
	assert.Equal(t, "towline:myapp-dev:web:example.com", r.ID)
	require.Len(t, r.Match, 1)
	assert.Equal(t, []string{"example.com"}, r.Match[0].Host)
	require.Len(t, r.Handle, 1)
	assert.Equal(t, "reverse_proxy", r.Handle[0].Handler)
	assert.Equal(t, "web:8080", r.Handle[0].Upstreams[0].Dial)
}

func TestCaddyManager_Add_Rejects(t *testing.T) {
	tests := []struct {
		name     string
		service  string
		domain   string
		port     int
		existing []caddyRoute
		wantErr  string
	}{
		{name: "service not in stack", service: "db", domain: "example.com", port: 80, wantErr: "not found in compose"},
		{name: "upstream injection via service", service: "10.0.0.5:22#", domain: "example.com", port: 80, wantErr: "invalid service name"},
		{name: "invalid domain", service: "web", domain: "example.com/../x", port: 80, wantErr: "invalid domain"},
		{name: "invalid port", service: "web", domain: "example.com", port: 70000, wantErr: "invalid port"},
		{
			name: "domain owned by another stack", service: "web", domain: "shop.example.com", port: 80,
			existing: []caddyRoute{route("towline:other-dev:web:shop.example.com", "shop.example.com", "web:80")},
			wantErr:  "already routed",
		},
		{
			name: "domain owned by a manual route", service: "web", domain: "Shop.Example.com", port: 80,
			existing: []caddyRoute{route("manual", "shop.example.com", "10.0.0.1:80")},
			wantErr:  "already routed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeCaddy{routes: tt.existing}
			ts := f.server(t)
			defer ts.Close()

			m := NewCaddyManager(ts.URL, "myapp-dev")
			_, err := m.Add(caddyTestCompose, tt.service, tt.domain, tt.port)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, f.posted)
		})
	}
}

func TestCaddyManager_Add_APIError(t *testing.T) {
	f := &fakeCaddy{postCode: http.StatusInternalServerError}
	ts := f.server(t)
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	_, err := m.Add(caddyTestCompose, "web", "example.com", 8080)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Caddy API error")
	assert.Contains(t, err.Error(), "500")
}

func TestCaddyManager_Remove(t *testing.T) {
	f := &fakeCaddy{}
	ts := f.server(t)
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	result, err := m.Remove("compose-content", "web", "example.com")
	require.NoError(t, err)
	assert.Equal(t, "compose-content", result)
	assert.Equal(t, []string{"/id/towline:myapp-dev:web:example.com"}, f.deleted)
}

func TestCaddyManager_Remove_InvalidInput(t *testing.T) {
	f := &fakeCaddy{}
	ts := f.server(t)
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	_, err := m.Remove("compose", "../../config", "example.com")
	require.Error(t, err)
	assert.Empty(t, f.deleted)
}

func TestCaddyManager_Remove_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	_, err := m.Remove("compose", "web", "example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Caddy API error")
}

func TestCaddyManager_List_OnlyOwnStack(t *testing.T) {
	f := &fakeCaddy{routes: []caddyRoute{
		route("towline:myapp-dev:web:example.com", "example.com", "web:8080"),
		route("towline:myapp-dev:api:api.example.com", "api.example.com", "api:3000"),
		route("towline:myapp-dev-2:web:other.example.com", "other.example.com", "web:80"),
		route("towline:other:web:x.com", "x.com", "web:80"),
		route("other-route", "other.com", "10.0.0.1:80"),
	}}
	ts := f.server(t)
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	mappings, err := m.List("compose")
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	assert.Equal(t, "web", mappings[0].Service)
	assert.Equal(t, "example.com", mappings[0].Domain)
	assert.Equal(t, 8080, mappings[0].Port)
	assert.Equal(t, BackendCaddy, mappings[0].Method)

	assert.Equal(t, "api", mappings[1].Service)
	assert.Equal(t, "api.example.com", mappings[1].Domain)
	assert.Equal(t, 3000, mappings[1].Port)
}

func TestCaddyManager_List_Empty(t *testing.T) {
	f := &fakeCaddy{}
	ts := f.server(t)
	defer ts.Close()

	m := NewCaddyManager(ts.URL, "myapp-dev")
	mappings, err := m.List("compose")
	require.NoError(t, err)
	assert.Empty(t, mappings)
}

func TestParseCaddyRoutes(t *testing.T) {
	prefix := routeIDPrefix("s")
	tests := []struct {
		name   string
		routes []caddyRoute
		want   []DomainMapping
	}{
		{
			name:   "parses service and port",
			routes: []caddyRoute{route("towline:s:myservice:mydomain.com", "mydomain.com", "myservice:4000")},
			want:   []DomainMapping{{Service: "myservice", Domain: "mydomain.com", Port: 4000, Method: BackendCaddy}},
		},
		{
			name:   "skips routes of other owners",
			routes: []caddyRoute{route("manual-route", "other.com", "x:1")},
		},
		{
			name:   "skips routes without host match",
			routes: []caddyRoute{{ID: "towline:s:web:example.com", Match: []caddyMatch{}}},
		},
		{
			name:   "tolerates missing upstreams",
			routes: []caddyRoute{{ID: "towline:s:web:example.com", Match: []caddyMatch{{Host: []string{"example.com"}}}}},
			want:   []DomainMapping{{Domain: "example.com", Method: BackendCaddy}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseCaddyRoutes(tt.routes, prefix))
		})
	}
}

func TestRouteID(t *testing.T) {
	assert.Equal(t, "towline:myapp-dev:web:example.com", routeID("myapp-dev", "web", "example.com"))
}

func TestNewCaddyManager(t *testing.T) {
	m := NewCaddyManager("http://localhost:2019/", "s")
	assert.Equal(t, "http://localhost:2019", m.AdminURL)
	assert.Equal(t, "s", m.StackName)
	assert.NotNil(t, m.client)
}

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

func TestCaddyManager_Add(t *testing.T) {
	var receivedRoute caddyRoute

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/config/apps/http/servers/srv0/routes", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		err = json.Unmarshal(body, &receivedRoute)
		require.NoError(t, err)

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL)

	result, err := m.Add("compose-content", "web", "example.com", 8080)
	require.NoError(t, err)

	// Compose content should be returned unchanged
	assert.Equal(t, "compose-content", result)

	// Verify the route sent to Caddy
	assert.Equal(t, "towline-web-example.com", receivedRoute.ID)
	require.Len(t, receivedRoute.Match, 1)
	require.Len(t, receivedRoute.Match[0].Host, 1)
	assert.Equal(t, "example.com", receivedRoute.Match[0].Host[0])
	require.Len(t, receivedRoute.Handle, 1)
	assert.Equal(t, "reverse_proxy", receivedRoute.Handle[0].Handler)
	require.Len(t, receivedRoute.Handle[0].Upstreams, 1)
	assert.Equal(t, "web:8080", receivedRoute.Handle[0].Upstreams[0].Dial)
}

func TestCaddyManager_Add_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL)

	_, err := m.Add("compose", "web", "example.com", 8080)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Caddy API error")
	assert.Contains(t, err.Error(), "500")
}

func TestCaddyManager_Remove(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		assert.Equal(t, "/id/towline-web-example.com", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL)

	result, err := m.Remove("compose-content", "web", "example.com")
	require.NoError(t, err)
	assert.Equal(t, "compose-content", result)
}

func TestCaddyManager_Remove_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL)

	_, err := m.Remove("compose", "web", "example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Caddy API error")
}

func TestCaddyManager_List(t *testing.T) {
	routes := []caddyRoute{
		{
			ID:    "towline-web-example.com",
			Match: []caddyMatch{{Host: []string{"example.com"}}},
			Handle: []caddyHandle{{
				Handler:   "reverse_proxy",
				Upstreams: []caddyUpstream{{Dial: "web:8080"}},
			}},
		},
		{
			ID:    "towline-api-api.example.com",
			Match: []caddyMatch{{Host: []string{"api.example.com"}}},
			Handle: []caddyHandle{{
				Handler:   "reverse_proxy",
				Upstreams: []caddyUpstream{{Dial: "api:3000"}},
			}},
		},
		{
			ID:    "other-route",
			Match: []caddyMatch{{Host: []string{"other.com"}}},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/config/apps/http/servers/srv0/routes", r.URL.Path)

		body, _ := json.Marshal(routes)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL)

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
	assert.Equal(t, BackendCaddy, mappings[1].Method)
}

func TestCaddyManager_List_Empty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	m := NewCaddyManager(ts.URL)

	mappings, err := m.List("compose")
	require.NoError(t, err)
	assert.Empty(t, mappings)
}

func TestParseCaddyRoutes(t *testing.T) {
	routes := []caddyRoute{
		{
			ID:    "towline-myservice-mydomain.com",
			Match: []caddyMatch{{Host: []string{"mydomain.com"}}},
			Handle: []caddyHandle{{
				Handler:   "reverse_proxy",
				Upstreams: []caddyUpstream{{Dial: "myservice:4000"}},
			}},
		},
	}

	mappings := ParseCaddyRoutes(routes)
	require.Len(t, mappings, 1)
	assert.Equal(t, "myservice", mappings[0].Service)
	assert.Equal(t, "mydomain.com", mappings[0].Domain)
	assert.Equal(t, 4000, mappings[0].Port)
}

func TestParseCaddyRoutes_SkipsNonTowline(t *testing.T) {
	routes := []caddyRoute{
		{
			ID:    "manual-route",
			Match: []caddyMatch{{Host: []string{"other.com"}}},
		},
	}

	mappings := ParseCaddyRoutes(routes)
	assert.Empty(t, mappings)
}

func TestParseCaddyRoutes_NoMatch(t *testing.T) {
	routes := []caddyRoute{
		{
			ID:    "towline-web-example.com",
			Match: []caddyMatch{},
		},
	}

	mappings := ParseCaddyRoutes(routes)
	assert.Empty(t, mappings)
}

func TestParseCaddyRoutes_NoUpstreams(t *testing.T) {
	routes := []caddyRoute{
		{
			ID:     "towline-web-example.com",
			Match:  []caddyMatch{{Host: []string{"example.com"}}},
			Handle: []caddyHandle{},
		},
	}

	mappings := ParseCaddyRoutes(routes)
	require.Len(t, mappings, 1)
	assert.Equal(t, "", mappings[0].Service)
	assert.Equal(t, "example.com", mappings[0].Domain)
	assert.Equal(t, 0, mappings[0].Port)
}

func TestRouteID(t *testing.T) {
	id := routeID("web", "example.com")
	assert.Equal(t, "towline-web-example.com", id)
}

func TestNewCaddyManager(t *testing.T) {
	m := NewCaddyManager("http://localhost:2019")
	assert.Equal(t, "http://localhost:2019", m.AdminURL)
	assert.NotNil(t, m.client)
}

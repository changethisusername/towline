package proxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCompose = `services:
  web:
    image: nginx:latest
    ports:
      - "80:80"
  api:
    image: myapp:latest
    ports:
      - "8080:8080"
`

func TestTraefikManager_Add(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	result, err := m.Add(testCompose, "web", "example.com", 80)
	require.NoError(t, err)

	// Verify traefik labels are present
	assert.Contains(t, result, "traefik.enable=true")
	assert.Contains(t, result, "traefik.http.routers.mystack-web.rule=Host(`example.com`)")
	assert.Contains(t, result, "traefik.http.routers.mystack-web.entrypoints=websecure")
	assert.Contains(t, result, "traefik.http.routers.mystack-web.tls.certresolver=letsencrypt")
	assert.Contains(t, result, "traefik.http.services.mystack-web.loadbalancer.server.port=80")
}

func TestTraefikManager_Add_ServiceNotFound(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	_, err := m.Add(testCompose, "nonexistent", "example.com", 80)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTraefikManager_Add_InvalidYAML(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	_, err := m.Add("not: [valid: yaml", "web", "example.com", 80)
	require.Error(t, err)
}

func TestTraefikManager_List(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	// First add a domain
	withDomain, err := m.Add(testCompose, "web", "example.com", 80)
	require.NoError(t, err)

	// Then list
	mappings, err := m.List(withDomain)
	require.NoError(t, err)
	require.Len(t, mappings, 1)

	assert.Equal(t, "web", mappings[0].Service)
	assert.Equal(t, "example.com", mappings[0].Domain)
	assert.Equal(t, 80, mappings[0].Port)
	assert.Equal(t, BackendTraefik, mappings[0].Method)
}

func TestTraefikManager_List_Empty(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	mappings, err := m.List(testCompose)
	require.NoError(t, err)
	assert.Empty(t, mappings)
}

func TestTraefikManager_List_MultipleServices(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	// Add domains to both services
	withDomain1, err := m.Add(testCompose, "web", "web.example.com", 80)
	require.NoError(t, err)

	withDomain2, err := m.Add(withDomain1, "api", "api.example.com", 8080)
	require.NoError(t, err)

	mappings, err := m.List(withDomain2)
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	// Collect domains (order may vary due to map iteration)
	domains := make(map[string]string)
	for _, m := range mappings {
		domains[m.Service] = m.Domain
	}

	assert.Equal(t, "web.example.com", domains["web"])
	assert.Equal(t, "api.example.com", domains["api"])
}

func TestTraefikManager_Remove(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	// Add then remove
	withDomain, err := m.Add(testCompose, "web", "example.com", 80)
	require.NoError(t, err)

	result, err := m.Remove(withDomain, "web", "example.com")
	require.NoError(t, err)

	// Verify traefik labels are gone
	assert.NotContains(t, result, "traefik.enable")
	assert.NotContains(t, result, "traefik.http.routers")
	assert.NotContains(t, result, "traefik.http.services")
}

func TestTraefikManager_Remove_ServiceNotFound(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	_, err := m.Remove(testCompose, "nonexistent", "example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTraefikManager_Remove_PreservesOtherLabels(t *testing.T) {
	compose := `services:
  web:
    image: nginx:latest
    labels:
      - "com.example.env=production"
`

	m := &TraefikManager{StackName: "mystack"}

	// Add traefik labels
	withDomain, err := m.Add(compose, "web", "example.com", 80)
	require.NoError(t, err)
	assert.Contains(t, withDomain, "com.example.env=production")

	// Remove traefik labels
	result, err := m.Remove(withDomain, "web", "example.com")
	require.NoError(t, err)

	// Original label should still be there
	assert.Contains(t, result, "com.example.env=production")
	assert.NotContains(t, result, "traefik.enable")
}

func TestExtractRouterName(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"traefik.http.routers.mystack-web.rule", "mystack-web"},
		{"traefik.http.routers.mystack-api.entrypoints", "mystack-api"},
		{"traefik.http.services.mystack-web.loadbalancer.server.port", ""},
		{"traefik.enable", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := extractRouterName(tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetLabelsMap_SliceFormat(t *testing.T) {
	svcDef := map[string]interface{}{
		"labels": []interface{}{
			"key1=value1",
			"key2=value2",
		},
	}

	result := getLabelsMap(svcDef)
	assert.Equal(t, "value1", result["key1"])
	assert.Equal(t, "value2", result["key2"])
}

func TestGetLabelsMap_MapFormat(t *testing.T) {
	svcDef := map[string]interface{}{
		"labels": map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		},
	}

	result := getLabelsMap(svcDef)
	assert.Equal(t, "value1", result["key1"])
	assert.Equal(t, "value2", result["key2"])
}

func TestGetLabelsMap_NoLabels(t *testing.T) {
	svcDef := map[string]interface{}{
		"image": "nginx:latest",
	}

	result := getLabelsMap(svcDef)
	assert.Empty(t, result)
}

func TestTraefikManager_List_InvalidYAML(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	_, err := m.List("not: [valid: yaml")
	require.Error(t, err)
}

func TestTraefikManager_Add_NoServicesSection(t *testing.T) {
	m := &TraefikManager{StackName: "mystack"}

	_, err := m.Add("version: '3'\n", "web", "example.com", 80)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no services section")
}

func TestTraefikManager_RouterName(t *testing.T) {
	m := &TraefikManager{StackName: "myapp"}
	name := m.routerName("web")
	assert.Equal(t, "myapp-web", name)
}

func TestTraefikManager_RoundTrip(t *testing.T) {
	m := &TraefikManager{StackName: "prod"}

	// Add
	added, err := m.Add(testCompose, "api", "api.example.com", 8080)
	require.NoError(t, err)

	// List should show it
	mappings, err := m.List(added)
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	assert.Equal(t, "api", mappings[0].Service)
	assert.Equal(t, "api.example.com", mappings[0].Domain)
	assert.Equal(t, 8080, mappings[0].Port)

	// Remove
	removed, err := m.Remove(added, "api", "api.example.com")
	require.NoError(t, err)

	// List should be empty
	mappings, err = m.List(removed)
	require.NoError(t, err)

	// No traefik-related mappings
	for _, m := range mappings {
		if strings.Contains(m.Domain, "api.example.com") {
			t.Error("domain should have been removed")
		}
	}
}

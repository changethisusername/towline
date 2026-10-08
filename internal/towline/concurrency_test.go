package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/proxy"
	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// statefulStackClient is an in-memory Portainer for one stack. Reads and
// writes are individually atomic but slow, so unsynchronized
// read-modify-write cycles interleave and lose updates.
type statefulStackClient struct {
	mockPortainerClient
	mu      sync.Mutex
	compose string
	env     []models.LocalStackEnvVar
	updates int
}

func (c *statefulStackClient) GetLocalStacks() ([]models.LocalStack, error) {
	c.mu.Lock()
	env := append([]models.LocalStackEnvVar(nil), c.env...)
	c.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	return defaultStacks(env...), nil
}

func (c *statefulStackClient) GetLocalStackFile(id int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.compose, nil
}

func (c *statefulStackClient) UpdateLocalStack(id, endpointId int, file string, env []models.LocalStackEnvVar, prune, pullImage bool) error {
	time.Sleep(2 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compose = file
	c.env = append([]models.LocalStackEnvVar(nil), env...)
	c.updates++
	return nil
}

func (c *statefulStackClient) history(t *testing.T) []DeploymentEntry {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.env {
		if e.Name == deploymentsEnvVar {
			var entries []DeploymentEntry
			require.NoError(t, json.Unmarshal([]byte(e.Value), &entries))
			return entries
		}
	}
	return nil
}

func newStatefulHandlers(t *testing.T, compose string) (*Handlers, *statefulStackClient) {
	t.Helper()
	cli := &statefulStackClient{compose: compose}
	srv := setupTestServer(t, cli)
	return &Handlers{Server: srv, StackName: testStackName, EnvID: testEnvID}, cli
}

func TestConcurrentStackMutationsDoNotLoseUpdates(t *testing.T) {
	const n = 8
	compose := "services:\n"
	for i := 0; i < n; i++ {
		compose += fmt.Sprintf("  svc%d:\n    image: nginx\n", i)
	}

	tests := []struct {
		name  string
		call  func(h *Handlers, i int) (bool, error)
		check func(t *testing.T, c *statefulStackClient)
	}{
		{
			name: "parallel env_set keeps every variable",
			call: func(h *Handlers, i int) (bool, error) {
				res, err := h.HandleEnvSet()(context.Background(), newRequest(map[string]any{
					"name": fmt.Sprintf("VAR_%d", i), "value": "v",
				}))
				return err == nil && !res.IsError, err
			},
			check: func(t *testing.T, c *statefulStackClient) {
				names := map[string]bool{}
				for _, e := range c.env {
					names[e.Name] = true
				}
				for i := 0; i < n; i++ {
					assert.True(t, names[fmt.Sprintf("VAR_%d", i)], "VAR_%d lost", i)
				}
			},
		},
		{
			name: "parallel scale keeps every service's replicas",
			call: func(h *Handlers, i int) (bool, error) {
				res, err := h.HandleScale()(context.Background(), newRequest(map[string]any{
					"service": fmt.Sprintf("svc%d", i), "replicas": float64(2),
				}))
				return err == nil && !res.IsError, err
			},
			check: func(t *testing.T, c *statefulStackClient) {
				var doc struct {
					Services map[string]struct {
						Deploy struct {
							Replicas int `yaml:"replicas"`
						} `yaml:"deploy"`
					} `yaml:"services"`
				}
				require.NoError(t, yaml.Unmarshal([]byte(c.compose), &doc))
				for i := 0; i < n; i++ {
					assert.Equal(t, 2, doc.Services[fmt.Sprintf("svc%d", i)].Deploy.Replicas, "svc%d scale lost", i)
				}
			},
		},
		{
			name: "parallel traefik domains_add keeps every route",
			call: func(h *Handlers, i int) (bool, error) {
				res, err := h.HandleDomainsAdd()(context.Background(), newRequest(map[string]any{
					"service": fmt.Sprintf("svc%d", i), "domain": fmt.Sprintf("d%d.example.com", i), "method": "traefik",
				}))
				return err == nil && !res.IsError, err
			},
			check: func(t *testing.T, c *statefulStackClient) {
				for i := 0; i < n; i++ {
					assert.True(t, strings.Contains(c.compose, fmt.Sprintf("d%d.example.com", i)), "route for svc%d lost", i)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, cli := newStatefulHandlers(t, compose)
			h.ProxyManager = &proxy.TraefikManager{StackName: testStackName}

			var wg sync.WaitGroup
			ok := make([]bool, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					ok[i], _ = tt.call(h, i)
				}(i)
			}
			wg.Wait()

			for i := 0; i < n; i++ {
				assert.True(t, ok[i], "call %d failed", i)
			}
			tt.check(t, cli)

			// Each change is recorded exactly once, in the same update.
			assert.Equal(t, n, cli.updates)
			assert.Len(t, cli.history(t), n)
		})
	}
}

func TestConcurrentRecordDeployment(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "parallel RecordDeployment keeps every entry", n: 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, cli := newStatefulHandlers(t, sampleComposeFile)
			var wg sync.WaitGroup
			for i := 0; i < tt.n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					assert.NoError(t, h.RecordDeployment(fmt.Sprintf("d%d", i), "", "", "success"))
				}(i)
			}
			wg.Wait()

			entries := cli.history(t)
			require.Len(t, entries, tt.n)
			ids := map[int]bool{}
			for _, e := range entries {
				ids[e.ID] = true
			}
			assert.Len(t, ids, tt.n, "deployment IDs must be unique")
		})
	}
}

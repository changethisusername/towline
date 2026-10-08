package towline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/changethisusername/towline/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleDomainsAdd_CaddyRedeploysAlias(t *testing.T) {
	tests := []struct {
		name        string
		updateErr   error
		wantError   bool
		wantText    string
		wantDeleted int
	}{
		{name: "alias is deployed with the route", wantText: "network alias web.mystack.towline"},
		{name: "failed redeploy removes the route again", updateErr: errors.New("portainer down"), wantError: true, wantText: "route was removed again", wantDeleted: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var posted, deleted int
			caddy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodGet:
					_, _ = w.Write([]byte("[]"))
				case http.MethodPost:
					posted++
				case http.MethodDelete:
					deleted++
				}
			}))
			defer caddy.Close()

			cli := new(mockPortainerClient)
			cli.On("GetLocalStacks").Return(defaultStacks(), nil)
			cli.On("GetLocalStackFile", testStackID).Return(sampleComposeFile, nil)
			cli.On("UpdateLocalStack", testStackID, testEnvID,
				mock.MatchedBy(func(c string) bool { return strings.Contains(c, "web.mystack.towline") }),
				mock.Anything, false, false).Return(tt.updateErr)

			h := setupHandlers(t, cli, nil)
			cm := proxy.NewCaddyManager(caddy.URL, testStackName)
			h.ProxyManager, h.CaddyManager = cm, cm

			result, err := h.HandleDomainsAdd()(context.Background(), newRequest(map[string]any{
				"service": "web", "domain": "example.com", "port": float64(80),
			}))
			require.NoError(t, err)
			assert.Equal(t, tt.wantError, result.IsError)
			assert.Contains(t, resultText(t, result), tt.wantText)
			assert.Equal(t, 1, posted)
			assert.Equal(t, tt.wantDeleted, deleted)
			cli.AssertExpectations(t)
		})
	}
}

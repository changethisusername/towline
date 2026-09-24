package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhook_SubmitAndStatus(t *testing.T) {
	var got Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/abc":
			_, _ = w.Write([]byte(`{"status":"approved"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/weird":
			_, _ = w.Write([]byte(`{"status":"yes"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wh, err := NewWebhook(srv.URL+"/", "secret")
	require.NoError(t, err)

	err = wh.Submit(context.Background(), Request{ID: "abc", Project: "p", Action: "deleteLocalStack"})
	require.NoError(t, err)
	assert.Equal(t, "abc", got.ID)
	assert.Equal(t, "deleteLocalStack", got.Action)

	status, err := wh.Status(context.Background(), "abc")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, status)

	_, err = wh.Status(context.Background(), "weird")
	assert.ErrorContains(t, err, "unknown status")

	_, err = wh.Status(context.Background(), "missing")
	assert.ErrorContains(t, err, "404")
}

func TestNewWebhook_InvalidURL(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "not a url", "http://"} {
		_, err := NewWebhook(u, "")
		assert.Error(t, err, u)
	}
}

func TestStore_PeekDoesNotConsume(t *testing.T) {
	s := NewStore()
	defer s.Stop()

	args := map[string]any{"id": 1}
	token := s.Request("deleteLocalStack", args)

	withToken := map[string]any{"id": 1, "approvalToken": token}
	require.NotNil(t, s.Peek(token, withToken))
	require.NotNil(t, s.Peek(token, withToken))
	assert.Nil(t, s.Peek(token, map[string]any{"id": 2, "approvalToken": token}))

	s.Consume(token)
	assert.Nil(t, s.Peek(token, withToken))
}

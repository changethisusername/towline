package gateway

import (
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/middleware"
)

func publicGrant(t *testing.T) (*Store, *Connection, *tokenPair) {
	t.Helper()
	return publicGrantAt(t, "")
}

func publicGrantAt(t *testing.T, path string) (*Store, *Connection, *tokenPair) {
	t.Helper()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	known := func(string) bool { return true }
	if _, err := s.OpenPairing("p", []string{"alpha"}, middleware.ScopeRead, known); err != nil {
		t.Fatal(err)
	}
	clientID, _, err := s.registerClient("app", []string{"https://claude.ai/api/mcp/auth_callback"}, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.approveClient(clientID, s.Pairings()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	verifier, challenge := pkce()
	code, _ := s.issueCode(conn, "https://claude.ai/api/mcp/auth_callback", true, "https://x/mcp", challenge)
	first, _, err := s.redeemCode(conn, code, "https://claude.ai/api/mcp/auth_callback", verifier, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, conn, first
}

// Two holders of one refresh token can't each keep a chain: once one
// successor is used, the sibling turning up revokes the grant.
func TestRefreshForkRevokesGrant(t *testing.T) {
	s, conn, first := publicGrant(t)
	a, _, err := s.refreshAccess(conn, first.Refresh, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.refreshAccess(conn, first.Refresh, "") // retry within grace
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := s.refreshAccess(conn, a.Refresh, "")
	if err != nil {
		t.Fatal(err)
	}
	// A retry of the successor that won is still fine.
	if _, _, err := s.refreshAccess(conn, a.Refresh, ""); err != nil {
		t.Fatalf("retry of the used successor: %v", err)
	}
	if _, _, err := s.refreshAccess(conn, b.Refresh, ""); err == nil {
		t.Fatal("sibling successor accepted after the other was used")
	}
	if _, _, err := s.refreshAccess(conn, a2.Refresh, ""); err == nil {
		t.Fatal("grant not revoked after a fork")
	}
}

// Many legitimate refreshes must not evict the record that detects reuse
// of the original token.
func TestRotationKeepsReuseRecordsUnderChurn(t *testing.T) {
	s, conn, first := publicGrant(t)
	cur := first
	for i := 0; i < maxTokensPerConnection+10; i++ {
		next, _, err := s.refreshAccess(conn, cur.Refresh, "")
		if err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
		cur = next
	}
	s.now = func() time.Time { return time.Now().Add(2 * refreshGrace) }
	if _, _, err := s.refreshAccess(conn, first.Refresh, ""); err == nil {
		t.Fatal("original token accepted after churn")
	}
	if _, _, err := s.refreshAccess(conn, cur.Refresh, ""); err == nil {
		t.Fatal("grant not revoked")
	}
}

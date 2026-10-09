package gateway

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/changethisusername/towline/internal/approval"
)

func TestIPKeyGroupsIPv6By64(t *testing.T) {
	a := ipKey(net.ParseIP("2001:db8:1:2::1"))
	b := ipKey(net.ParseIP("2001:db8:1:2:ffff::9"))
	c := ipKey(net.ParseIP("2001:db8:1:3::1"))
	if a != b || a == c || a != "2001:db8:1:2::/64" {
		t.Fatalf("keys: %s %s %s", a, b, c)
	}
	if got := ipKey(net.ParseIP("192.0.2.7")); got != "192.0.2.7" {
		t.Fatalf("ipv4 key: %s", got)
	}
}

// Windows are dropped once their own period ends, so short-lived keys
// don't pile up for an hour.
func TestRateLimiterDropsExpiredWindows(t *testing.T) {
	now := time.Now()
	l := newRateLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 1000; i++ {
		l.allow("k"+itoa(i), 5, time.Minute)
	}
	if !l.allow("long", 1, time.Hour) || l.allow("long", 1, time.Hour) {
		t.Fatal("limit not enforced")
	}
	now = now.Add(2 * time.Minute)
	l.allow("x", 5, time.Minute)
	if len(l.windows) != 2 { // "long" and "x"
		t.Fatalf("%d windows left", len(l.windows))
	}
	if l.allow("long", 1, time.Hour) {
		t.Fatal("hour window reset early")
	}
}

func TestPublicURLKeepsIPv6Brackets(t *testing.T) {
	for in, want := range map[string]string{
		"https://[::1]:443":       "https://[::1]",
		"https://[2001:DB8::1]":   "https://[2001:db8::1]",
		"https://[::1]:8443":      "https://[::1]:8443",
		"https://Example.COM:443": "https://example.com",
	} {
		cfg := &Config{PublicURL: in, DataDir: t.TempDir(), OwnerTokenHash: approval.HashToken(testOwnerToken),
			Portainer: PortainerConfig{URL: "http://p"}, Projects: []ProjectConfig{{Name: "alpha", Tier: "dev", Token: "t"}}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if cfg.PublicURL != want {
			t.Errorf("%s: got %s, want %s", in, cfg.PublicURL, want)
		}
	}
}

func TestCleanClientNameDropsInvisibleCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"Claude\u200b":      "Claude",
		"\ufeffClaude":      "Claude",
		"Cla\u0085ude":      "Claude",
		"A\u2028B\u2029C":   "ABC",
		"\u3164\u115f":      "Unnamed app",
		"Evil\u202eppa.exe": "Evilppa.exe",
		"  Codex  ":         "Codex",
		"Caf\u00e9":         "Caf\u00e9",
	} {
		if got := cleanClientName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// Token writes happen after the lock is released, but they still reach
// disk before the call returns.
func TestRefreshedTokensSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, conn, first := publicGrantAt(t, path)
	second, _, err := s.refreshAccess(conn, first.Refresh, "")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Authenticate(second.Access, "https://x/mcp") == nil {
		t.Fatal("access token lost on restart")
	}
	c2 := s2.Connection(conn.ID)
	if _, _, err := s2.refreshAccess(c2, second.Refresh, ""); err != nil {
		t.Fatalf("refresh after restart: %v", err)
	}
	s2.now = func() time.Time { return time.Now().Add(2 * refreshGrace) }
	if _, _, err := s2.refreshAccess(c2, first.Refresh, ""); err == nil {
		t.Fatal("rotated token accepted after restart")
	}
}

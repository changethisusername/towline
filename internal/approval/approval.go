package approval

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

const (
	// TokenTTL bounds how long a human has to act on an approval request.
	TokenTTL        = 30 * time.Minute
	CleanupInterval = 30 * time.Second
)

type PendingApproval struct {
	// RequestID identifies the request on the approval server. It is never
	// shown to the agent, so an agent that can reach the server cannot
	// submit its own request under that ID and have it approved in place of
	// the real one (e.g. after the server restarts and forgets it).
	RequestID string
	ToolName  string
	// Caller is the remote client the token was issued to ("" for the
	// local stdio agent). A token only works for the caller it was issued to.
	Caller    string
	Args      map[string]any
	ArgsHash  string
	CreatedAt time.Time
	claimed   bool
}

type Store struct {
	mu      sync.Mutex
	pending map[string]*PendingApproval
	stopCh  chan struct{}
}

func NewStore() *Store {
	s := &Store{
		pending: make(map[string]*PendingApproval),
		stopCh:  make(chan struct{}),
	}
	go s.cleanup()
	return s
}

func (s *Store) Request(toolName string, args map[string]any) string {
	return s.RequestFor(toolName, "", args)
}

// RequestFor is Request for a token that only the given caller may use.
func (s *Store) RequestFor(toolName, caller string, args map[string]any) string {
	token := generateToken()
	s.mu.Lock()
	s.pending[token] = &PendingApproval{
		RequestID: generateToken(),
		ToolName:  toolName,
		Caller:    caller,
		Args:      args,
		ArgsHash:  hashArgs(args),
		CreatedAt: time.Now(),
	}
	s.mu.Unlock()
	return token
}

// Validate checks a token and returns the pending approval if valid.
// Returns nil if the token is invalid, expired, or the args don't match.
func (s *Store) Validate(token string, currentArgs map[string]any) *PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[token]
	if !ok {
		return nil
	}
	if time.Since(p.CreatedAt) > TokenTTL {
		delete(s.pending, token)
		return nil
	}
	delete(s.pending, token)

	// Verify the arguments match what was approved (excluding approvalToken itself)
	if hashArgs(currentArgs) != p.ArgsHash {
		return nil
	}

	return p
}

// Peek returns the pending approval for a token without consuming it, or nil
// if the token is unknown, expired, or was issued for different arguments.
func (s *Store) Peek(token string, currentArgs map[string]any) *PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[token]
	if !ok {
		return nil
	}
	if time.Since(p.CreatedAt) > TokenTTL {
		delete(s.pending, token)
		return nil
	}
	if hashArgs(currentArgs) != p.ArgsHash {
		return nil
	}
	return p
}

// Claim is Peek for a caller about to act on the token: it also marks the
// token in use, so concurrent re-calls with the same token cannot all
// proceed. The caller must Consume or Release it. Returns nil if the token is
// unknown, expired, mismatched, or already claimed.
func (s *Store) Claim(token string, currentArgs map[string]any) *PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[token]
	if !ok || p.claimed {
		return nil
	}
	if time.Since(p.CreatedAt) > TokenTTL {
		delete(s.pending, token)
		return nil
	}
	if hashArgs(currentArgs) != p.ArgsHash {
		return nil
	}
	p.claimed = true
	return p
}

// Release returns a claimed token so it can be used again later.
func (s *Store) Release(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.pending[token]; ok {
		p.claimed = false
	}
}

// Consume removes a token so it cannot be used again.
func (s *Store) Consume(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, token)
}

// hashArgs produces a deterministic hash of tool arguments, excluding
// the approvalToken field (which changes between request and re-call).
func hashArgs(args map[string]any) string {
	filtered := make(map[string]any, len(args))
	for k, v := range args {
		if k == "approvalToken" {
			continue
		}
		filtered[k] = v
	}

	// Sort keys for deterministic serialization
	keys := make([]string, 0, len(filtered))
	for k := range filtered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		ordered = append(ordered, k, filtered[k])
	}

	data, err := json.Marshal(ordered)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (s *Store) Stop() {
	close(s.stopCh)
}

func (s *Store) cleanup() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for token, p := range s.pending {
				if now.Sub(p.CreatedAt) > TokenTTL {
					delete(s.pending, token)
				}
			}
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}

func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

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
	TokenTTL        = 5 * time.Minute
	CleanupInterval = 30 * time.Second
)

type PendingApproval struct {
	ToolName  string
	Args      map[string]any
	ArgsHash  string
	CreatedAt time.Time
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
	token := generateToken()
	s.mu.Lock()
	s.pending[token] = &PendingApproval{
		ToolName:  toolName,
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

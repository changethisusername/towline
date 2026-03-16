package approval

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
		CreatedAt: time.Now(),
	}
	s.mu.Unlock()
	return token
}

func (s *Store) Validate(token string) *PendingApproval {
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
	return p
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
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

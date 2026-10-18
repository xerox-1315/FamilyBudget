package agent

import (
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"
)

const (
	historyTurns = 4
	historyTTL   = 30 * time.Minute
	pendingTTL   = 10 * time.Minute
)

// turn is one exchange: the user message, tool calls and results, and the final reply.
type turn struct {
	messages []openai.ChatCompletionMessage
}

type pendingDelete struct {
	ids     []int64
	summary string
	expires time.Time
}

type session struct {
	turns    []turn
	lastSeen time.Time
	pending  *pendingDelete
}

// memory keeps short per-user conversation state in process memory.
type memory struct {
	mu       sync.Mutex
	sessions map[int64]*session
	now      func() time.Time
}

func newMemory(now func() time.Time) *memory {
	return &memory{sessions: map[int64]*session{}, now: now}
}

func (m *memory) get(userID int64) *session {
	s, ok := m.sessions[userID]
	if !ok || m.now().Sub(s.lastSeen) > historyTTL {
		s = &session{}
		m.sessions[userID] = s
	}
	s.lastSeen = m.now()
	return s
}

func (m *memory) history(userID int64) []openai.ChatCompletionMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []openai.ChatCompletionMessage
	for _, t := range m.get(userID).turns {
		out = append(out, t.messages...)
	}
	return out
}

func (m *memory) addTurn(userID int64, msgs []openai.ChatCompletionMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.get(userID)
	s.turns = append(s.turns, turn{messages: msgs})
	if len(s.turns) > historyTurns {
		s.turns = s.turns[len(s.turns)-historyTurns:]
	}
}

func (m *memory) setPending(userID int64, p *pendingDelete) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p != nil {
		p.expires = m.now().Add(pendingTTL)
	}
	m.get(userID).pending = p
}

// takePending returns and clears the pending deletion, if it has not expired.
func (m *memory) takePending(userID int64) *pendingDelete {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.get(userID)
	p := s.pending
	s.pending = nil
	if p == nil || m.now().After(p.expires) {
		return nil
	}
	return p
}

// peekPending returns the ids of a pending deletion without clearing it.
func (m *memory) peekPending(userID int64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[userID]
	if !ok || s.pending == nil || m.now().After(s.pending.expires) {
		return nil
	}
	return append([]int64(nil), s.pending.ids...)
}

// prune drops idle sessions; called periodically to bound memory use.
func (m *memory) prune() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if m.now().Sub(s.lastSeen) > historyTTL {
			delete(m.sessions, id)
		}
	}
}

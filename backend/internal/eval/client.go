package eval

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"

	"familybudget/internal/agent"
)

// ErrDailyLimit means the provider's daily quota for a model is used up.
var ErrDailyLimit = errors.New("daily rate limit reached")

const maxWait = 90 * time.Second

var retryInRe = regexp.MustCompile(`try again in ([0-9hm.]+s)`)

// meter wraps the LLM client: it waits out per-minute rate limits patiently (evals must
// measure quality, not quotas) and counts calls, tokens and time spent in the model.
type meter struct {
	inner agent.ChatClient

	mu       sync.Mutex
	calls    int
	tokens   int
	busy     time.Duration // time inside successful calls
	waited   time.Duration // time spent waiting for rate limits
	dailyHit bool
}

func (m *meter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	for attempt := 0; ; attempt++ {
		start := time.Now()
		resp, err := m.inner.CreateChatCompletion(ctx, req)
		if err == nil {
			m.mu.Lock()
			m.calls++
			m.tokens += resp.Usage.TotalTokens
			m.busy += time.Since(start)
			m.mu.Unlock()
			return resp, nil
		}
		wait, limited, daily := rateLimit(err)
		if daily {
			m.mu.Lock()
			m.dailyHit = true
			m.mu.Unlock()
			return resp, ErrDailyLimit
		}
		if !limited || attempt >= 8 {
			return resp, err
		}
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(wait):
		}
		m.mu.Lock()
		m.waited += wait
		m.mu.Unlock()
	}
}

type meterSnapshot struct {
	calls  int
	tokens int
	busy   time.Duration
	waited time.Duration
}

func (m *meter) snapshot() meterSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return meterSnapshot{calls: m.calls, tokens: m.tokens, busy: m.busy, waited: m.waited}
}

func (m *meter) exhausted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dailyHit
}

func (a meterSnapshot) sub(b meterSnapshot) meterSnapshot {
	return meterSnapshot{calls: a.calls - b.calls, tokens: a.tokens - b.tokens, busy: a.busy - b.busy, waited: a.waited - b.waited}
}

// rateLimit classifies a provider error: how long to wait, whether it is a rate limit,
// and whether it is a daily quota that waiting a minute will not fix.
func rateLimit(err error) (time.Duration, bool, bool) {
	var apiErr *openai.APIError
	var reqErr *openai.RequestError
	var msg string
	switch {
	case errors.As(err, &apiErr) && apiErr.HTTPStatusCode == http.StatusTooManyRequests:
		msg = apiErr.Message
	case errors.As(err, &reqErr) && reqErr.HTTPStatusCode == http.StatusTooManyRequests:
		msg = string(reqErr.Body)
	case errors.As(err, &apiErr) && apiErr.HTTPStatusCode >= 500:
		return 5 * time.Second, true, false
	default:
		return 0, false, false
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "per day") || strings.Contains(lower, "(tpd)") || strings.Contains(lower, "(rpd)") {
		return 0, true, true
	}
	d := 5 * time.Second
	if m := retryInRe.FindStringSubmatch(msg); m != nil {
		if parsed, err := time.ParseDuration(m[1]); err == nil {
			d = parsed + 500*time.Millisecond
		}
	}
	return min(d, maxWait), true, false
}

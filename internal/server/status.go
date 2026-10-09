package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/4rji/lantern/internal/qmd"
)

// statusTTL is how long a QMD health result is reused.
const statusTTL = 3 * time.Second

// pillHealthTimeout keeps page loads fast when QMD hangs instead of refusing connections.
const pillHealthTimeout = 2 * time.Second

// Status is the QMD connection state shown in the nav pill and banner.
type Status struct {
	Up        bool
	Uptime    time.Duration
	Error     string
	CheckedAt time.Time
}

type healthChecker interface {
	Health(ctx context.Context) (qmd.Health, error)
}

// statusCache checks QMD's /health at most once per statusTTL.
type statusCache struct {
	client healthChecker
	now    func() time.Time

	mu   sync.Mutex
	last Status
}

func newStatusCache(client healthChecker) *statusCache {
	return &statusCache{client: client, now: time.Now}
}

// Get returns the cached status, refreshing it when stale. Concurrent callers
// wait for a single in-flight check.
func (s *statusCache) Get(ctx context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.CheckedAt.IsZero() && s.now().Sub(s.last.CheckedAt) < statusTTL {
		return s.last
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pillHealthTimeout)
	defer cancel()
	h, err := s.client.Health(ctx)
	st := Status{CheckedAt: s.now()}
	switch {
	case err == nil:
		st.Up = true
		st.Uptime = time.Duration(h.Uptime * float64(time.Second))
	case errors.Is(err, qmd.ErrUnavailable):
		st.Error = "QMD is not running"
	default:
		st.Error = err.Error()
	}
	s.last = st
	return st
}

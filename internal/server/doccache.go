package server

import (
	"strings"
	"sync"
	"time"

	"github.com/4rji/lantern/internal/library"
	"github.com/4rji/lantern/internal/qmdconfig"
)

// docCacheTTL is how long a collection walk is reused. Short enough that new
// or edited notes show up almost immediately.
const docCacheTTL = 5 * time.Second

type docCacheEntry struct {
	docs []library.Doc
	err  error
	at   time.Time
}

// docCache memoizes collection walks for a few seconds.
type docCache struct {
	now func() time.Time

	mu      sync.Mutex
	entries map[string]docCacheEntry
}

func newDocCache() *docCache {
	return &docCache{now: time.Now, entries: map[string]docCacheEntry{}}
}

// Docs returns the collection's documents, walking the folder when the cached
// result is stale or the collection settings changed.
func (c *docCache) Docs(col *qmdconfig.Collection) ([]library.Doc, error) {
	key := strings.Join(append([]string{col.Name, col.Path, col.Pattern}, col.Ignore...), "\x00")
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < docCacheTTL {
		return e.docs, e.err
	}
	docs, err := library.Walk(col)
	c.mu.Lock()
	c.entries[key] = docCacheEntry{docs: docs, err: err, at: c.now()}
	c.mu.Unlock()
	return docs, err
}

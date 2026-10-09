package server

import (
	"os"
	"sync"
	"time"

	"github.com/4rji/lantern/internal/qmdconfig"
)

// configStore loads QMD's index.yml and reloads it when the file changes,
// so collections added while lantern runs show up without a restart.
type configStore struct {
	path string

	mu      sync.Mutex
	cfg     *qmdconfig.Config
	modTime time.Time
	size    int64
}

func newConfigStore(path string) *configStore {
	return &configStore{path: path}
}

// Path is the index.yml location in use.
func (s *configStore) Path() string { return s.path }

// Load returns the current config. Errors (missing or invalid file) are returned every call.
func (s *configStore) Load() (*qmdconfig.Config, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg != nil && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return s.cfg, nil
	}
	cfg, err := qmdconfig.Load(s.path)
	if err != nil {
		return nil, err
	}
	s.cfg, s.modTime, s.size = cfg, info.ModTime(), info.Size()
	return cfg, nil
}

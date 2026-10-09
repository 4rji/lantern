package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4rji/lantern/internal/qmd"
)

// fakeHealth simulates QMD being up or down. query, if set, answers /query.
type fakeHealth struct {
	mu    sync.Mutex
	up    bool
	err   error
	calls int
	query func(ctx context.Context, q qmd.QueryRequest) ([]qmd.Result, error)
}

func (f *fakeHealth) Query(ctx context.Context, q qmd.QueryRequest) ([]qmd.Result, error) {
	if f.query == nil {
		return nil, qmd.ErrUnavailable
	}
	return f.query(ctx, q)
}

func (f *fakeHealth) set(up bool) {
	f.mu.Lock()
	f.up = up
	f.mu.Unlock()
}

func (f *fakeHealth) Health(ctx context.Context) (qmd.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return qmd.Health{}, f.err
	}
	if !f.up {
		return qmd.Health{}, qmd.ErrUnavailable
	}
	return qmd.Health{Status: "ok", Uptime: 3725}, nil
}

func writeConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "index.yml")
	if err := os.WriteFile(p, []byte("collections:\n  demo:\n    path: /tmp/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestServer(t *testing.T, h QMD, configPath string) *Server {
	t.Helper()
	s, err := New(Options{Version: "test", QMDURL: "http://localhost:8181", QMDConfigPath: configPath, LocalOnly: true}, h)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, h http.Handler, target string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "127.0.0.1:8090"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(body)
}

func mustContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("body does not contain %q", s)
		}
	}
}

func mustNotContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(body, s) {
			t.Errorf("body unexpectedly contains %q", s)
		}
	}
}

func TestPagesRender(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	tests := []struct {
		path, title, nav string
	}{
		{"/", "<title>Library · lantern</title>", `href="/" aria-current="page"`},
		{"/search", "<title>Search · lantern</title>", `href="/search" aria-current="page"`},
		{"/ask", "<title>Ask AI · lantern</title>", `href="/ask" aria-current="page"`},
		{"/settings", "<title>Settings · lantern</title>", `href="/settings" aria-current="page"`},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, body := get(t, s, tt.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q", ct)
			}
			mustContain(t, body, tt.title, tt.nav, `data-theme="dark"`, "data-theme-toggle", "lantern test")
			mustNotContain(t, body, "Index", "indexes") // UI talks about content, not indexes
		})
	}
}

func TestStatusPillAndBanner(t *testing.T) {
	h := &fakeHealth{up: true}
	s := newTestServer(t, h, writeConfig(t))
	now := time.Now()
	s.status.now = func() time.Time { return now }

	_, body := get(t, s, "/")
	mustContain(t, body, "QMD connected", "pill-ok", "uptime 1h 2m")
	mustNotContain(t, body, "QMD is not running.")

	h.set(false)
	now = now.Add(statusTTL) // expire the cache
	_, body = get(t, s, "/")
	mustContain(t, body, "QMD not running", "pill-down", "QMD is not running.", "qmd mcp --http --daemon")

	// Library still renders without QMD.
	resp, _ := get(t, s, "/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("library status = %d without QMD", resp.StatusCode)
	}
}

func TestStatusPartial(t *testing.T) {
	h := &fakeHealth{up: false}
	s := newTestServer(t, h, writeConfig(t))
	now := time.Now()
	s.status.now = func() time.Time { return now }

	resp, body := get(t, s, "/partials/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mustContain(t, body, `id="qmd-status"`, `hx-get="/partials/status"`, "QMD not running",
		`id="qmd-banner" hx-swap-oob="true"`, "QMD is not running.")
	mustNotContain(t, body, "<html")

	h.set(true)
	now = now.Add(statusTTL)
	_, body = get(t, s, "/partials/status")
	mustContain(t, body, "QMD connected", `id="qmd-banner" hx-swap-oob="true"`)
	mustNotContain(t, body, "QMD is not running.")
}

func TestStatusNotResponding(t *testing.T) {
	s := newTestServer(t, &fakeHealth{err: qmd.ErrTimeout}, writeConfig(t))
	_, body := get(t, s, "/")
	mustContain(t, body, "QMD not responding", qmd.ErrTimeout.Error())
}

func TestStatusCacheTTL(t *testing.T) {
	h := &fakeHealth{up: true}
	c := newStatusCache(h)
	now := time.Now()
	c.now = func() time.Time { return now }

	c.Get(context.Background())
	c.Get(context.Background())
	if h.calls != 1 {
		t.Errorf("calls = %d, want 1 within TTL", h.calls)
	}
	now = now.Add(statusTTL)
	if st := c.Get(context.Background()); !st.Up || st.Uptime != 3725*time.Second {
		t.Errorf("status = %+v", st)
	}
	if h.calls != 2 {
		t.Errorf("calls = %d, want 2 after TTL", h.calls)
	}
}

func TestNotFound(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	resp, body := get(t, s, "/no/such/page")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}
	mustContain(t, body, "Page not found", "/no/such/page", "Back to the Library")
}

func TestPanicRecovery(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	h := s.recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	resp, body := get(t, h, "/")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d", resp.StatusCode)
	}
	mustContain(t, body, "Something went wrong", "panic: boom")
}

func TestStaticAssets(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	_, page := get(t, s, "/")
	for _, name := range []string{"app.css", "app.js", "htmx.min.js", "favicon.svg"} {
		url := s.assetURL(name)
		if !strings.Contains(url, "?v=") {
			t.Errorf("%s has no content hash: %s", name, url)
		}
		mustContain(t, page, url)
		resp, body := get(t, s, url)
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("GET %s = %d (%d bytes)", url, resp.StatusCode, len(body))
		}
	}
	resp, _ := get(t, s, "/static/app.css")
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("css Content-Type = %q", ct)
	}
}

func TestConfigErrorBanner(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yml")
	s := newTestServer(t, &fakeHealth{up: true}, missing)
	resp, body := get(t, s, "/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	mustContain(t, body, "Can't read QMD's config", missing)

	s = newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	_, body = get(t, s, "/")
	mustNotContain(t, body, "Can't read QMD's config")
}

func TestConfigStoreReloads(t *testing.T) {
	p := writeConfig(t)
	cs := newConfigStore(p)
	cfg, err := cs.Load()
	if err != nil || len(cfg.Collections) != 1 {
		t.Fatalf("Load = %v, %v", cfg, err)
	}
	again, _ := cs.Load()
	if again != cfg {
		t.Error("unchanged file should return the cached config")
	}
	content := "collections:\n  demo:\n    path: /tmp/demo\n  more:\n    path: /tmp/more\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(p, later, later)
	cfg, err = cs.Load()
	if err != nil || len(cfg.Collections) != 2 {
		t.Errorf("after change: %v, %v", cfg, err)
	}
	os.Remove(p)
	if _, err := cs.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestHostGuard(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	for host, want := range map[string]int{
		"127.0.0.1:8090":  http.StatusOK,
		"localhost:8090":  http.StatusOK,
		"[::1]:8090":      http.StatusOK,
		"LOCALHOST":       http.StatusOK,
		"evil.example:80": http.StatusForbidden,
		"192.168.1.10":    http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: status = %d, want %d", host, rec.Code, want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, writeConfig(t))
	resp, _ := get(t, s, "/")
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers = %v", resp.Header)
	}
}

func TestFormatUptime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		9 * time.Second:               "9s",
		12 * time.Minute:              "12m",
		3*time.Hour + 5*time.Minute:   "3h 5m",
		50*time.Hour + 10*time.Minute: "2d 2h",
	} {
		if got := formatUptime(d); got != want {
			t.Errorf("formatUptime(%v) = %q, want %q", d, got, want)
		}
	}
}

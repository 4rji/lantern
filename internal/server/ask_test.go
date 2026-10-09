package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4rji/lantern/internal/ai"
)

const testClaudeKey = "sk-ant-api03-SECRETSECRETSECRET-ABCD"

func serverWithProviders(t *testing.T, env map[string]string) *Server {
	t.Helper()
	cfgPath := searchLibrary(t)
	s, err := New(Options{
		Version: "test", QMDURL: "http://localhost:8181", QMDConfigPath: cfgPath, LocalOnly: true,
		Providers: ai.Providers(func(k string) string { return env[k] }),
	}, &fakeHealth{up: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAskPage(t *testing.T) {
	s := serverWithProviders(t, map[string]string{ai.EnvAnthropicKey: testClaudeKey})
	resp, body := get(t, s, "/ask")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mustContain(t, body,
		"Coming soon — AI answers are not implemented yet",
		`value="claude" checked`, "Claude", "OpenAI",
		"Key detected", "Key missing",
		ai.DefaultClaudeModel,
		`value="demo" checked`, `name="question"`,
		`disabled aria-disabled="true"`,
		"The answer will appear here.", "Documents used for the answer",
	)
	mustNotContain(t, body, testClaudeKey, "ABCD")
}

func TestAskPagePrefersConfiguredProvider(t *testing.T) {
	s := serverWithProviders(t, map[string]string{ai.EnvOpenAIKey: "sk-openai-key-1234567"})
	_, body := get(t, s, "/ask")
	mustContain(t, body, `value="openai" checked`)
	mustNotContain(t, body, `value="claude" checked`)

	s = serverWithProviders(t, nil)
	_, body = get(t, s, "/ask")
	mustContain(t, body, `value="claude" checked`) // nothing configured: first one
	if strings.Count(body, "Key missing") != 2 {
		t.Error("both providers should show Key missing")
	}
}

func postAsk(t *testing.T, s http.Handler, body, contentType, origin string) (int, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/ask", strings.NewReader(body))
	req.Host = "127.0.0.1:8090"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	return rec.Code, out
}

func TestAPIAsk(t *testing.T) {
	s := serverWithProviders(t, map[string]string{ai.EnvAnthropicKey: testClaudeKey})
	tests := []struct {
		name, body, ct, origin string
		code                   int
		err                    string
	}{
		{"not implemented", `{"provider":"claude","question":"q","collections":["demo"]}`, "application/json", "", 501, "not implemented"},
		{"default provider", `{"question":"q"}`, "application/json; charset=utf-8", "", 501, "not implemented"},
		{"openai without key", `{"provider":"openai","question":"q"}`, "application/json", "", 501, "not implemented"},
		{"same origin", `{"question":"q"}`, "application/json", "http://127.0.0.1:8090", 501, "not implemented"},
		{"cross origin", `{"question":"q"}`, "application/json", "https://evil.example", 403, "cross-origin request refused"},
		{"form post", `question=q`, "application/x-www-form-urlencoded", "", 415, "expected application/json"},
		{"bad json", `{`, "application/json", "", 400, "invalid JSON body"},
		{"unknown provider", `{"provider":"gemini"}`, "application/json", "", 400, "unknown provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out := postAsk(t, s, tt.body, tt.ct, tt.origin)
			if code != tt.code || out["error"] != tt.err {
				t.Errorf("got %d %v, want %d %q", code, out, tt.code, tt.err)
			}
		})
	}
}

func TestAPIAskMethod(t *testing.T) {
	s := serverWithProviders(t, nil)
	resp, _ := get(t, s, "/api/ask")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/ask = %d", resp.StatusCode)
	}
}

func TestSettingsPage(t *testing.T) {
	s := serverWithProviders(t, map[string]string{ai.EnvAnthropicKey: testClaudeKey, ai.EnvClaudeModel: "claude-sonnet-5-5"})
	resp, body := get(t, s, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mustContain(t, body,
		"http://localhost:8181", "Connected", "uptime 1h 2m",
		`href="/c/demo/"`, "Demo notes for lantern", "ignores Archive/**", "**/*.md",
		`href="/c/notes/"`, "Folder not found on this computer.",
		"Key detected", "••••ABCD", "claude-sonnet-5-5", ai.EnvClaudeModel,
		"Key missing", ai.EnvOpenAIKey, "not chosen yet",
		"zed://file{path}:{line}:{col}",
		"lantern test",
	)
	mustNotContain(t, body, testClaudeKey, "SECRET")
}

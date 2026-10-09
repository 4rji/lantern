package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4rji/lantern/internal/ai"
	"github.com/4rji/lantern/internal/qmd"
)

const testClaudeKey = "sk-ant-api03-SECRETSECRETSECRET-ABCD"

// fakeProvider answers by actually using the toolbox it is given.
type fakeProvider struct {
	id, name   string
	configured bool
	ask        func(ctx context.Context, req ai.AskRequest) (ai.AskResponse, error)
	got        *ai.AskRequest
}

func (f *fakeProvider) ID() string          { return f.id }
func (f *fakeProvider) DisplayName() string { return f.name }
func (f *fakeProvider) Configured() bool    { return f.configured }
func (f *fakeProvider) Model() string       { return "fake-model-1" }
func (f *fakeProvider) Ask(ctx context.Context, req ai.AskRequest) (ai.AskResponse, error) {
	f.got = &req
	return f.ask(ctx, req)
}

// searchAndRead is a provider turn: search, read the top hit, answer citing it.
func searchAndRead(ctx context.Context, req ai.AskRequest) (ai.AskResponse, error) {
	if _, err := req.Tools.Call(ctx, "search_documents", json.RawMessage(`{"query":"deploy"}`)); err != nil {
		return ai.AskResponse{}, err
	}
	if _, err := req.Tools.Call(ctx, "read_document", json.RawMessage(`{"collection":"demo","path":"work/deploy/guide.md"}`)); err != nil {
		return ai.AskResponse{}, err
	}
	answer := "Build, then roll out (`demo/work/deploy/guide.md`). See also `demo/README.md`.\n\n<script>alert(1)</script>"
	return ai.AskResponse{AnswerMarkdown: answer, Sources: req.Tools.Sources(answer)}, nil
}

func serverWithProviders(t *testing.T, env map[string]string) *Server {
	t.Helper()
	return serverWith(t, &fakeHealth{up: true}, ai.Providers(func(k string) string { return env[k] })...)
}

func serverWith(t *testing.T, q QMD, providers ...ai.Provider) *Server {
	t.Helper()
	s, err := New(Options{
		Version: "test", QMDURL: "http://localhost:8181", QMDConfigPath: searchLibrary(t), LocalOnly: true,
		Providers: providers,
	}, q)
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
		`value="claude" data-configured="true"`, `value="openai" data-configured="false"`,
		`data-key-var="LANTERN_OPENAI_API_KEY"`,
		"Key detected", "Key missing", ai.DefaultClaudeModel, ai.DefaultOpenAIModel,
		`value="demo" checked`, `name="question"`, "data-ask-submit",
		"sent to the provider you pick",
	)
	mustNotContain(t, body, testClaudeKey, "ABCD", "Coming soon")
}

func TestAskPagePrefersConfiguredProvider(t *testing.T) {
	s := serverWithProviders(t, map[string]string{ai.EnvOpenAIKey: "sk-openai-key-1234567"})
	_, body := get(t, s, "/ask")
	mustContain(t, body, `value="openai" data-configured="true" data-key-var="LANTERN_OPENAI_API_KEY" data-name="OpenAI" checked`)

	s = serverWithProviders(t, nil)
	_, body = get(t, s, "/ask")
	mustContain(t, body, `data-name="Claude" checked`) // nothing configured: first one
	if strings.Count(body, "Key missing") != 2 {
		t.Error("both providers should show Key missing")
	}
}

func postAsk(t *testing.T, s http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/ask", strings.NewReader(body))
	req.Host = "127.0.0.1:8090"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func jsonError(rec *httptest.ResponseRecorder) string {
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out["error"]
}

func TestAPIAskValidation(t *testing.T) {
	ok := &fakeProvider{id: "claude", name: "Claude", configured: true, ask: searchAndRead}
	nokey := &fakeProvider{id: "openai", name: "OpenAI", configured: false, ask: searchAndRead}
	s := serverWith(t, newRecordingQMD(t, "qmd_query_lex.json"), ok, nokey)

	long := strings.Repeat("a", maxQuestionLength+1)
	tests := []struct {
		name, body string
		headers    map[string]string
		code       int
		err        string
	}{
		{"cross origin", `{"question":"q","collections":["demo"]}`, map[string]string{"Origin": "https://evil.example"}, 403, "cross-origin request refused"},
		{"form post", `question=q`, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, 415, "expected application/json"},
		{"bad json", `{`, nil, 400, "invalid JSON body"},
		{"unknown provider", `{"provider":"gemini","question":"q","collections":["demo"]}`, nil, 400, "unknown provider"},
		{"empty question", `{"question":"   ","collections":["demo"]}`, nil, 400, "Type a question first."},
		{"long question", `{"question":"` + long + `","collections":["demo"]}`, nil, 400, "too long"},
		{"no key", `{"provider":"openai","question":"q","collections":["demo"]}`, nil, 400, "Set LANTERN_OPENAI_API_KEY"},
		{"no collections", `{"question":"q"}`, nil, 400, "Pick at least one collection."},
		{"unknown collections", `{"question":"q","collections":["nope"]}`, nil, 400, "Pick at least one collection."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postAsk(t, s, tt.body, tt.headers)
			if rec.Code != tt.code || !strings.Contains(jsonError(rec), tt.err) {
				t.Errorf("got %d %q, want %d %q", rec.Code, jsonError(rec), tt.code, tt.err)
			}
		})
	}
	if ok.got != nil {
		t.Error("the provider must not be called for invalid requests")
	}
}

func TestAPIAskQMDDown(t *testing.T) {
	p := &fakeProvider{id: "claude", name: "Claude", configured: true, ask: searchAndRead}
	s := serverWith(t, &fakeHealth{up: false}, p)
	rec := postAsk(t, s, `{"question":"q","collections":["demo"]}`, nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(jsonError(rec), "QMD is not running") {
		t.Errorf("got %d %q", rec.Code, jsonError(rec))
	}
	if p.got != nil {
		t.Error("the provider must not be called when QMD is down")
	}
}

func TestAPIAskJSON(t *testing.T) {
	q := newRecordingQMD(t, "qmd_query_lex.json")
	p := &fakeProvider{id: "claude", name: "Claude", configured: true, ask: searchAndRead}
	s := serverWith(t, q, p)

	rec := postAsk(t, s, `{"question":"  How do I deploy?  ","collections":["demo","nope","demo"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if p.got.Question != "How do I deploy?" || strings.Join(p.got.Collections, ",") != "demo" {
		t.Errorf("provider got %+v", p.got)
	}
	if got := q.last.Collections; len(got) != 1 || got[0] != "demo" {
		t.Errorf("search collections = %v (must be limited to the chosen ones)", got)
	}

	var res askResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Provider != "Claude" || res.Model != "fake-model-1" || res.Elapsed == "" {
		t.Errorf("result = %+v", res)
	}
	if len(res.Sources) != 2 || res.Sources[0].URL != "/c/demo/work/deploy/guide.md" || res.Sources[0].Title != "Deploy guide" ||
		res.Sources[1].Path != "README.md" {
		t.Errorf("sources = %+v", res.Sources)
	}
	mustContain(t, res.AnswerHTML,
		`<a href="/c/demo/work/deploy/guide.md"><code>demo/work/deploy/guide.md</code></a>`,
		`<a href="/c/demo/README.md"><code>demo/README.md</code></a>`)
	mustNotContain(t, res.AnswerHTML, "<script>")
}

// readSSE parses an event stream into (event, data) pairs.
func readSSE(t *testing.T, body string) [][2]string {
	t.Helper()
	var out [][2]string
	var name string
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			out = append(out, [2]string{name, strings.TrimPrefix(line, "data: ")})
		}
	}
	return out
}

func TestAPIAskStream(t *testing.T) {
	p := &fakeProvider{id: "claude", name: "Claude", configured: true, ask: searchAndRead}
	s := serverWith(t, newRecordingQMD(t, "qmd_query_lex.json"), p)
	rec := postAsk(t, s, `{"question":"q","collections":["demo"]}`, map[string]string{"Accept": "text/event-stream"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	events := readSSE(t, rec.Body.String())
	var names []string
	for _, e := range events {
		names = append(names, e[0])
	}
	if strings.Join(names, ",") != "progress,progress,answer" {
		t.Fatalf("events = %v", names)
	}
	if !strings.Contains(events[0][1], "Searching for “deploy”") || !strings.Contains(events[1][1], "Reading demo/work/deploy/guide.md") {
		t.Errorf("progress = %v", events[:2])
	}
	var res askResult
	if err := json.Unmarshal([]byte(events[2][1]), &res); err != nil || len(res.Sources) == 0 {
		t.Errorf("answer event = %s (%v)", events[2][1], err)
	}
}

func TestAPIAskErrors(t *testing.T) {
	tests := []struct {
		err  error
		code int
		msg  string
	}{
		{&ai.RefusalError{Provider: "Claude", Category: "cyber"}, 422, "Claude declined to answer (cyber). Try rephrasing"},
		{&ai.ProviderError{Provider: "Claude", Status: 429, Message: "rate limited or out of credits; wait a moment and try again"}, 502, "rate limited"},
		{ai.ErrTooManySteps, 500, "without finishing an answer"},
		{ai.ErrTimeout, 504, "no answer within"},
	}
	for _, tt := range tests {
		p := &fakeProvider{id: "claude", name: "Claude", configured: true, ask: func(context.Context, ai.AskRequest) (ai.AskResponse, error) {
			return ai.AskResponse{}, tt.err
		}}
		s := serverWith(t, &fakeHealth{up: true, query: func(context.Context, qmd.QueryRequest) ([]qmd.Result, error) { return nil, nil }}, p)

		rec := postAsk(t, s, `{"question":"q","collections":["demo"]}`, nil)
		if rec.Code != tt.code || !strings.Contains(jsonError(rec), tt.msg) {
			t.Errorf("%v: JSON got %d %q", tt.err, rec.Code, jsonError(rec))
		}
		rec = postAsk(t, s, `{"question":"q","collections":["demo"]}`, map[string]string{"Accept": "text/event-stream"})
		events := readSSE(t, rec.Body.String())
		if len(events) == 0 || events[len(events)-1][0] != "error" || !strings.Contains(events[len(events)-1][1], tt.msg) {
			t.Errorf("%v: stream got %v", tt.err, events)
		}
	}
}

func TestLinkCitations(t *testing.T) {
	src := []askSource{{Collection: "demo", Path: "a b.md", URL: "/c/demo/a%20b.md"}}
	got := linkCitations("See `demo/a b.md` and [`demo/a b.md`](x) twice `demo/a b.md`.", src)
	want := "See [`demo/a b.md`](/c/demo/a%20b.md) and [`demo/a b.md`](x) twice [`demo/a b.md`](/c/demo/a%20b.md)."
	if got != want {
		t.Errorf("linkCitations =\n%s\nwant\n%s", got, want)
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
	s := serverWithProviders(t, map[string]string{ai.EnvAnthropicKey: testClaudeKey, ai.EnvClaudeModel: "claude-sonnet-5-5", ai.EnvClaudeEffort: "high"})
	resp, body := get(t, s, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mustContain(t, body,
		"http://localhost:8181", "Connected", "uptime 1h 2m",
		`href="/c/demo/"`, "Demo notes for lantern", "ignores Archive/**", "**/*.md",
		`href="/c/notes/"`, "Folder not found on this computer.",
		"Key detected", "••••ABCD", "claude-sonnet-5-5", ai.EnvClaudeModel, "effort <code>high</code>", ai.EnvClaudeEffort,
		"Key missing", ai.EnvOpenAIKey, ai.DefaultOpenAIModel,
		"zed://file{path}:{line}:{col}",
		"lantern test",
	)
	mustNotContain(t, body, testClaudeKey, "SECRET")
}

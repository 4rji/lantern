package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAPI replays canned JSON responses in order and records each request.
type fakeAPI struct {
	t         *testing.T
	path      string
	responses []string
	status    int // when non-zero, every request fails with this status

	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != f.path {
		f.t.Errorf("unexpected path %s", r.URL.Path)
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	f.mu.Lock()
	n := len(f.bodies)
	f.bodies = append(f.bodies, body)
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if f.status != 0 {
		w.WriteHeader(f.status)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
		return
	}
	if n >= len(f.responses) {
		n = len(f.responses) - 1 // keep repeating the last response
	}
	io.WriteString(w, f.responses[n])
}

func startFake(t *testing.T, f *fakeAPI) string {
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv.URL
}

func askRequest(t *testing.T) (AskRequest, *[]string) {
	tb, _, progress := newTestToolbox(t)
	return AskRequest{Question: "How do I roll back a deploy?", Collections: []string{"demo"}, Tools: tb}, progress
}

// ---------- Claude ----------

func claudeMsg(stop string, content string) string {
	return `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[` + content +
		`],"stop_reason":"` + stop + `","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`
}

func newTestClaude(url string) *Claude {
	c := NewClaude(env(map[string]string{EnvAnthropicKey: "sk-ant-test-key-0000"}))
	c.baseURL = url
	return c
}

func TestClaudeToolLoop(t *testing.T) {
	f := &fakeAPI{path: "/v1/messages", responses: []string{
		claudeMsg("tool_use", `{"type":"tool_use","id":"toolu_1","name":"search_documents","input":{"query":"roll back"}}`),
		claudeMsg("tool_use", `{"type":"tool_use","id":"toolu_2","name":"read_document","input":{"collection":"demo","path":"work/deploy/guide.md"}},`+
			`{"type":"tool_use","id":"toolu_3","name":"read_document","input":{"collection":"other","path":"private.md"}}`),
		claudeMsg("end_turn", `{"type":"text","text":"Roll back with kubectl rollout undo (`+"`demo/work/deploy/guide.md`"+`)."}`),
	}}
	c := newTestClaude(startFake(t, f))
	req, progress := askRequest(t)

	resp, err := c.Ask(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.AnswerMarkdown, "kubectl rollout undo") {
		t.Errorf("answer = %q", resp.AnswerMarkdown)
	}
	if len(resp.Sources) != 1 || resp.Sources[0].Path != "work/deploy/guide.md" || resp.Sources[0].Title != "Deploy guide" {
		t.Errorf("sources = %+v", resp.Sources)
	}
	if len(f.bodies) != 3 {
		t.Fatalf("API calls = %d, want 3", len(f.bodies))
	}

	first, h := f.bodies[0], f.headers[0]
	if h.Get("X-Api-Key") != "sk-ant-test-key-0000" {
		t.Errorf("x-api-key = %q", h.Get("X-Api-Key"))
	}
	if !strings.Contains(h.Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("anthropic-beta = %q", h.Get("Anthropic-Beta"))
	}
	if first["model"] != "claude-opus-5-5" || first["fallbacks"] != "default" {
		t.Errorf("model/fallbacks = %v / %v", first["model"], first["fallbacks"])
	}
	if oc, _ := first["output_config"].(map[string]any); oc["effort"] != "medium" {
		t.Errorf("output_config = %v", first["output_config"])
	}
	if _, ok := first["thinking"]; ok {
		t.Error("thinking must be left unset on the default model")
	}
	if _, ok := first["tool_choice"]; ok {
		t.Error("tool_choice must not be forced")
	}
	if tools, _ := first["tools"].([]any); len(tools) != 3 {
		t.Errorf("tools = %d", len(tools))
	}
	if msg := first["messages"].([]any)[0].(map[string]any); !strings.Contains(jsonString(msg), "Collections you may use: demo") {
		t.Errorf("user message = %s", jsonString(msg))
	}

	// The third request carries both tool results; the forbidden read is an error result.
	third := jsonString(f.bodies[2])
	if !strings.Contains(third, "Roll back with `kubectl rollout undo`") {
		t.Error("read_document result not sent back")
	}
	if !strings.Contains(third, `"is_error":true`) || !strings.Contains(third, "may not be read") {
		t.Errorf("forbidden read should come back as an error result:\n%s", third)
	}
	if want := []string{"Asking Claude…", "Searching for “roll back”…", "Reading demo/work/deploy/guide.md…"}; !hasAll(*progress, want) {
		t.Errorf("progress = %v", *progress)
	}
}

func TestClaudeRefusal(t *testing.T) {
	f := &fakeAPI{path: "/v1/messages", responses: []string{
		`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":"refusal","stop_sequence":null,` +
			`"stop_details":{"type":"refusal","category":"cyber","explanation":null},"usage":{"input_tokens":10,"output_tokens":0}}`,
	}}
	req, _ := askRequest(t)
	_, err := newTestClaude(startFake(t, f)).Ask(context.Background(), req)
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.Category != "cyber" {
		t.Errorf("err = %v", err)
	}
}

func TestClaudeTooManySteps(t *testing.T) {
	f := &fakeAPI{path: "/v1/messages", responses: []string{
		claudeMsg("tool_use", `{"type":"tool_use","id":"toolu_x","name":"list_collections","input":{}}`),
	}}
	req, _ := askRequest(t)
	_, err := newTestClaude(startFake(t, f)).Ask(context.Background(), req)
	if !errors.Is(err, ErrTooManySteps) {
		t.Errorf("err = %v", err)
	}
	if len(f.bodies) != MaxToolRounds+1 {
		t.Errorf("API calls = %d, want %d", len(f.bodies), MaxToolRounds+1)
	}
}

func TestClaudeErrors(t *testing.T) {
	f := &fakeAPI{path: "/v1/messages", status: http.StatusUnauthorized}
	req, _ := askRequest(t)
	_, err := newTestClaude(startFake(t, f)).Ask(context.Background(), req)
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != 401 || !strings.Contains(pe.Message, EnvAnthropicKey) {
		t.Errorf("err = %v", err)
	}

	if _, err := NewClaude(env(nil)).Ask(context.Background(), req); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: err = %v", err)
	}
}

func TestClaudeNoFallbackOnHaiku(t *testing.T) {
	f := &fakeAPI{path: "/v1/messages", responses: []string{claudeMsg("end_turn", `{"type":"text","text":"Nothing found."}`)}}
	c := NewClaude(env(map[string]string{EnvAnthropicKey: "k", EnvClaudeModel: "claude-haiku-5-5"}))
	c.baseURL = startFake(t, f)
	req, _ := askRequest(t)
	if _, err := c.Ask(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.bodies[0]["fallbacks"]; ok {
		t.Error("Haiku has no server-side fallback; fallbacks must not be sent")
	}
}

// ---------- OpenAI ----------

func openaiResp(id, status, output string) string {
	return `{"id":"` + id + `","object":"response","created_at":1,"status":"` + status + `","model":"gpt-6-astra","output":[` + output +
		`],"incomplete_details":null,"error":null,"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
}

func openaiText(text string) string {
	return `{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":` +
		jsonString(text) + `,"annotations":[]}]}`
}

func newTestOpenAI(url string) *OpenAI {
	o := NewOpenAI(env(map[string]string{EnvOpenAIKey: "sk-openai-test-0000"}))
	o.baseURL = url + "/v1/"
	return o
}

func TestOpenAIToolLoop(t *testing.T) {
	f := &fakeAPI{path: "/v1/responses", responses: []string{
		openaiResp("resp_1", "completed", `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"search_documents","arguments":"{\"query\":\"roll back\"}","status":"completed"}`),
		openaiResp("resp_2", "completed", `{"type":"function_call","id":"fc_2","call_id":"call_2","name":"read_document","arguments":"{\"collection\":\"demo\",\"path\":\"work/deploy/guide.md\"}","status":"completed"}`),
		openaiResp("resp_3", "completed", openaiText("Use kubectl rollout undo (`demo/work/deploy/guide.md`).")),
	}}
	o := newTestOpenAI(startFake(t, f))
	req, progress := askRequest(t)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.AnswerMarkdown, "kubectl rollout undo") || len(resp.Sources) != 1 || resp.Sources[0].Title != "Deploy guide" {
		t.Errorf("resp = %+v", resp)
	}
	if len(f.bodies) != 3 {
		t.Fatalf("API calls = %d", len(f.bodies))
	}
	if f.headers[0].Get("Authorization") != "Bearer sk-openai-test-0000" {
		t.Errorf("Authorization = %q", f.headers[0].Get("Authorization"))
	}
	first := f.bodies[0]
	if first["model"] != "gpt-6-astra" || first["instructions"] == nil || first["previous_response_id"] != nil {
		t.Errorf("first request = %s", jsonString(first))
	}
	if r, _ := first["reasoning"].(map[string]any); r["effort"] != "medium" {
		t.Errorf("reasoning = %v", first["reasoning"])
	}
	second := f.bodies[1]
	if second["previous_response_id"] != "resp_1" || second["instructions"] == nil {
		t.Errorf("second request = %s", jsonString(second))
	}
	if s := jsonString(second["input"]); !strings.Contains(s, `"type":"function_call_output"`) || !strings.Contains(s, `"call_id":"call_1"`) || !strings.Contains(s, "demo/work/deploy/guide.md") {
		t.Errorf("tool output not sent back: %s", s)
	}
	if !hasAll(*progress, []string{"Asking OpenAI…", "Searching for “roll back”…", "Reading demo/work/deploy/guide.md…"}) {
		t.Errorf("progress = %v", *progress)
	}
}

func TestOpenAIRefusalAndLimits(t *testing.T) {
	f := &fakeAPI{path: "/v1/responses", responses: []string{
		openaiResp("resp_1", "completed", `{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"I can't help with that."}]}`),
	}}
	req, _ := askRequest(t)
	_, err := newTestOpenAI(startFake(t, f)).Ask(context.Background(), req)
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.Provider != "OpenAI" {
		t.Errorf("refusal: err = %v", err)
	}

	loop := &fakeAPI{path: "/v1/responses", responses: []string{
		openaiResp("resp_x", "completed", `{"type":"function_call","id":"fc","call_id":"c","name":"list_collections","arguments":"{}","status":"completed"}`),
	}}
	req, _ = askRequest(t)
	if _, err := newTestOpenAI(startFake(t, loop)).Ask(context.Background(), req); !errors.Is(err, ErrTooManySteps) {
		t.Errorf("loop: err = %v", err)
	}
	if len(loop.bodies) != MaxToolRounds+1 {
		t.Errorf("API calls = %d", len(loop.bodies))
	}

	bad := &fakeAPI{path: "/v1/responses", status: http.StatusUnauthorized}
	req, _ = askRequest(t)
	_, err = newTestOpenAI(startFake(t, bad)).Ask(context.Background(), req)
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != 401 || !strings.Contains(pe.Message, EnvOpenAIKey) {
		t.Errorf("401: err = %v", err)
	}
	if _, err := NewOpenAI(env(nil)).Ask(context.Background(), req); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: err = %v", err)
	}
}

func TestOpenAIIncomplete(t *testing.T) {
	f := &fakeAPI{path: "/v1/responses", responses: []string{
		strings.Replace(openaiResp("r", "incomplete", openaiText("Partial answer")), `"incomplete_details":null`, `"incomplete_details":{"reason":"max_output_tokens"}`, 1),
	}}
	req, _ := askRequest(t)
	resp, err := newTestOpenAI(startFake(t, f)).Ask(context.Background(), req)
	if err != nil || !strings.Contains(resp.AnswerMarkdown, "Partial answer") || !strings.Contains(resp.AnswerMarkdown, "cut off: max_output_tokens") {
		t.Errorf("resp = %+v, err = %v", resp, err)
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func hasAll(got, want []string) bool {
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

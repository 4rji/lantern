package qmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeQMD serves the Phase 0 fixtures and records the last /query body.
func fakeQMD(t *testing.T) (*httptest.Server, *QueryRequest) {
	t.Helper()
	var last QueryRequest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "qmd_health.json"))
	})
	mux.HandleFunc("POST /query", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		last = QueryRequest{}
		if err := json.Unmarshal(raw, &last); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		name := "qmd_query_lex.json"
		switch {
		case last.Searches[0].Query == "nothing":
			name = "qmd_query_empty.json"
		case last.Rerank:
			name = "qmd_query_smart.json"
		}
		w.Write(fixture(t, name))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestHealth(t *testing.T) {
	srv, _ := fakeQMD(t)
	h, err := New(srv.URL + "/").Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" || h.Uptime != 9 {
		t.Errorf("Health = %+v", h)
	}
}

func TestQueryFast(t *testing.T) {
	srv, last := fakeQMD(t)
	results, err := New(srv.URL).Query(context.Background(), FastQuery("deploy", []string{"demo"}, 5))
	if err != nil {
		t.Fatal(err)
	}
	want := QueryRequest{Searches: []Search{{Lex, "deploy"}}, Collections: []string{"demo"}, Limit: 5, Rerank: false}
	if !reflect.DeepEqual(*last, want) {
		t.Errorf("sent %+v, want %+v", *last, want)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	r := results[0]
	if r.DocID != "#d990bb" || r.File != "qmd://demo/work/deploy/guide.md" || r.Title != "How to deploy the API" ||
		r.Score != 0.92 || r.Line != 2 || r.Context != "Demo notes for lantern\n\nWork notes: deploys and meetings" {
		t.Errorf("first result = %+v", r)
	}
}

func TestQuerySmart(t *testing.T) {
	srv, last := fakeQMD(t)
	results, err := New(srv.URL).Query(context.Background(), SmartQuery("deploy", nil, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Searches) != 2 || last.Searches[0].Type != Lex || last.Searches[1].Type != Vec || !last.Rerank {
		t.Errorf("sent %+v", *last)
	}
	if len(results) != 5 || results[4].File != "qmd://demo/ideas/garden.md" {
		t.Errorf("got %d results: %+v", len(results), results)
	}
}

func TestQueryEmpty(t *testing.T) {
	srv, _ := fakeQMD(t)
	results, err := New(srv.URL).Query(context.Background(), FastQuery("nothing", nil, 5))
	if err != nil {
		t.Fatal(err)
	}
	if results == nil || len(results) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", results)
	}
}

func TestRequestJSON(t *testing.T) {
	// rerank must always be sent (QMD defaults it to true); empty optionals must be omitted.
	b, _ := json.Marshal(FastQuery("x", nil, 0))
	if got, want := string(b), `{"searches":[{"type":"lex","query":"x"}],"rerank":false}`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
	b, _ = json.Marshal(SmartQuery("x", []string{"a", "b"}, 20))
	if got, want := string(b), `{"searches":[{"type":"lex","query":"x"},{"type":"vec","query":"x"}],"collections":["a","b"],"limit":20,"rerank":true}`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
}

func TestValidate(t *testing.T) {
	many := make([]Search, MaxSearches+1)
	for i := range many {
		many[i] = Search{Lex, "x"}
	}
	tests := []struct {
		name string
		req  QueryRequest
	}{
		{"no searches", QueryRequest{}},
		{"too many searches", QueryRequest{Searches: many}},
		{"blank query", FastQuery("  ", nil, 5)},
		{"bad type", QueryRequest{Searches: []Search{{"fuzzy", "x"}}}},
		{"negative limit", FastQuery("x", nil, -1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("http://127.0.0.1:1").Query(context.Background(), tt.req)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens there now

	c := New(url)
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Health err = %v, want ErrUnavailable", err)
	}
	if _, err := c.Query(context.Background(), FastQuery("x", nil, 5)); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Query err = %v, want ErrUnavailable", err)
	}
}

func TestTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	c := New(srv.URL)
	c.QueryTimeout = 50 * time.Millisecond
	c.HealthTimeout = 50 * time.Millisecond
	if _, err := c.Query(context.Background(), SmartQuery("x", nil, 5)); !errors.Is(err, ErrTimeout) {
		t.Errorf("Query err = %v, want ErrTimeout", err)
	}
	if _, err := c.Health(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Errorf("Health err = %v, want ErrTimeout", err)
	}
}

func TestDefaultTimeouts(t *testing.T) {
	c := New("http://x")
	if c.HealthTimeout != 5*time.Second || c.QueryTimeout != 120*time.Second {
		t.Errorf("timeouts = %v / %v", c.HealthTimeout, c.QueryTimeout)
	}
}

func TestStatusError(t *testing.T) {
	tests := []struct {
		body, want string
	}{
		{`{"error":"searches is required"}`, "searches is required"},
		{`{"message":"boom"}`, "boom"},
		{"plain failure\n", "plain failure"},
	}
	for _, tt := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, tt.body)
		}))
		_, err := New(srv.URL).Query(context.Background(), FastQuery("x", nil, 5))
		srv.Close()
		var se *StatusError
		if !errors.As(err, &se) || se.Code != 400 || se.Message != tt.want {
			t.Errorf("body %q: err = %v", tt.body, err)
		}
	}
}

func TestCleanSnippet(t *testing.T) {
	var resp QueryResponse
	if err := json.Unmarshal(fixture(t, "qmd_query_smart.json"), &resp); err != nil {
		t.Fatal(err)
	}
	var cli []Result
	if err := json.Unmarshal(fixture(t, "qmd_cli_query_deploy.json"), &cli); err != nil {
		t.Fatal(err)
	}

	got := resp.Results[2].CleanSnippet()
	want := "# Weekly meeting\n- Decided to move the deploy to Thursdays.\n- [ ] Write the rollback runbook\n- [x] Upgrade the database"
	if got != want {
		t.Errorf("HTTP snippet:\n%q\nwant\n%q", got, want)
	}
	// The CLI snippet has no line prefixes; cleaning both must give the same text.
	if c := cli[2].CleanSnippet(); c != want {
		t.Errorf("CLI snippet:\n%q\nwant\n%q", c, want)
	}

	unprefixed := Result{Snippet: "2026: a big year\nmore text"}
	if got := unprefixed.CleanSnippet(); got != "2026: a big year\nmore text" {
		t.Errorf("unprefixed content was altered: %q", got)
	}
}

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/4rji/lantern/internal/qmd"
)

// recordingQMD answers /query from a fixture and remembers the last request.
type recordingQMD struct {
	fakeHealth
	mu   sync.Mutex
	last *qmd.QueryRequest
}

func newRecordingQMD(t *testing.T, fixture string) *recordingQMD {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var resp qmd.QueryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	r := &recordingQMD{fakeHealth: fakeHealth{up: true}}
	r.fakeHealth.query = func(ctx context.Context, q qmd.QueryRequest) ([]qmd.Result, error) {
		r.mu.Lock()
		r.last = &q
		r.mu.Unlock()
		return resp.Results, nil
	}
	return r
}

// searchLibrary is the demo tree with the files the QMD fixtures point at.
func searchLibrary(t *testing.T) string {
	cfgPath, root := demoLibrary(t, `  notes:
    path: /tmp/does-not-matter
    includeByDefault: false
`)
	os.WriteFile(filepath.Join(root, "work", "meeting-2026-10-01.md"), []byte("# Weekly meeting\n"), 0o644)
	return cfgPath
}

func TestSearchPageDefaults(t *testing.T) {
	s := newTestServer(t, &fakeHealth{up: true}, searchLibrary(t))
	_, body := get(t, s, "/search")
	mustContain(t, body,
		`name="q"`, `value="fast" checked`, `<option value="20" selected>`,
		`value="demo" checked`, // includeByDefault
		"Type a word or phrase",
	)
	mustNotContain(t, body, `value="notes" checked`, `hx-trigger="load"`)

	// A submitted form with nothing checked keeps nothing checked.
	_, body = get(t, s, "/search?f=1&q=x")
	mustNotContain(t, body, `value="demo" checked`)

	// A shared URL restores the state and loads results asynchronously.
	_, body = get(t, s, "/search?q=deploy&mode=smart&limit=50&c=notes&f=1")
	mustContain(t, body, `value="deploy"`, `value="smart" checked`, `<option value="50" selected>`,
		`value="notes" checked`, `hx-trigger="load"`, `/search/results?`)
	mustNotContain(t, body, `value="demo" checked`)
}

func TestSearchFastRequestAndResults(t *testing.T) {
	q := newRecordingQMD(t, "qmd_query_lex.json")
	s := newTestServer(t, q, searchLibrary(t))
	resp, body := get(t, s, "/search/results?q=deploy&mode=fast&limit=10&c=demo&f=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	want := qmd.QueryRequest{Searches: []qmd.Search{{Type: qmd.Lex, Query: "deploy"}}, Collections: []string{"demo"}, Limit: 10, Rerank: false}
	if !reflect.DeepEqual(*q.last, want) {
		t.Errorf("sent %+v, want %+v", *q.last, want)
	}
	mustContain(t, body,
		"3 results", "Fast",
		`href="/c/demo/work/deploy/guide.md"`, "How to deploy the API",
		`href="/c/demo/README.md"`,
		`href="/c/demo/work/meeting-2026-10-01.md"`,
		`<mark>deploy</mark>`,
		`style="width: 92%"`, "0.92",
	)
	mustNotContain(t, body, "@@", "<html")
	if got := resp.Header.Get("HX-Replace-Url"); got != "/search?c=demo&f=1&limit=10&mode=fast&q=deploy" {
		t.Errorf("HX-Replace-Url = %q", got)
	}
}

func TestSearchSmartRequest(t *testing.T) {
	q := newRecordingQMD(t, "qmd_query_smart.json")
	s := newTestServer(t, q, searchLibrary(t))
	_, body := get(t, s, "/search/results?q=how+do+I+ship&mode=smart&c=demo&c=notes&c=unknown&f=1")
	got := *q.last
	if len(got.Searches) != 2 || got.Searches[0].Type != qmd.Lex || got.Searches[1].Type != qmd.Vec || !got.Rerank || got.Limit != 20 {
		t.Errorf("sent %+v", got)
	}
	if !reflect.DeepEqual(got.Collections, []string{"demo", "notes"}) {
		t.Errorf("collections = %v (unknown names must be dropped)", got.Collections)
	}
	mustContain(t, body, "5 results", "Smart", `href="/c/demo/ideas/garden.md"`, `href="/c/demo/Archive/old.md"`)
}

func TestSearchResultNotOnDisk(t *testing.T) {
	q := &fakeHealth{up: true, query: func(ctx context.Context, _ qmd.QueryRequest) ([]qmd.Result, error) {
		return []qmd.Result{
			{File: "qmd://demo/gone.md", Title: "Gone", Score: 0.5},
			{File: "qmd://other/x.md", Title: "Other", Score: 0.4},
		}, nil
	}}
	s := newTestServer(t, q, searchLibrary(t))
	_, body := get(t, s, "/search/results?q=x&c=demo&f=1")
	mustContain(t, body, "Gone", "gone.md", "Other", "x.md", "(not found on this computer)")
	mustNotContain(t, body, `href="/c/demo/gone.md"`, `href="/c/other/`)
}

func TestSearchStates(t *testing.T) {
	cfg := searchLibrary(t)
	tests := []struct {
		name  string
		err   error
		empty bool
		query string
		want  string
	}{
		{"idle", nil, false, "/search/results?q=&c=demo&f=1", "Type a word or phrase"},
		{"none chosen", nil, false, "/search/results?q=x&f=1", "Pick at least one collection"},
		{"down", qmd.ErrUnavailable, false, "/search/results?q=x&c=demo&f=1", "QMD is not running."},
		{"timeout", qmd.ErrTimeout, false, "/search/results?q=x&c=demo&f=1", "QMD did not answer in time."},
		{"http error", &qmd.StatusError{Code: 400, Message: "bad searches"}, false, "/search/results?q=x&c=demo&f=1", "bad searches"},
		{"no hits fast", nil, true, "/search/results?q=zzz&c=demo&f=1", "Try Smart mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeHealth{up: true, query: func(ctx context.Context, _ qmd.QueryRequest) ([]qmd.Result, error) {
				if tt.err != nil {
					return nil, tt.err
				}
				return []qmd.Result{}, nil
			}}
			s := newTestServer(t, q, cfg)
			resp, body := get(t, s, tt.query)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d (htmx only swaps 2xx)", resp.StatusCode)
			}
			mustContain(t, body, tt.want)
		})
	}
}

func TestSearchLimitClamp(t *testing.T) {
	q := newRecordingQMD(t, "qmd_query_empty.json")
	s := newTestServer(t, q, searchLibrary(t))
	get(t, s, "/search/results?q=x&c=demo&f=1&limit=100000")
	if q.last.Limit != maxSearchLimit {
		t.Errorf("limit = %d", q.last.Limit)
	}
	get(t, s, "/search/results?q=x&c=demo&f=1&limit=-3")
	if q.last.Limit != defaultSearchLimit {
		t.Errorf("limit = %d", q.last.Limit)
	}
}

func TestSearchEscapesSnippets(t *testing.T) {
	q := &fakeHealth{up: true, query: func(ctx context.Context, _ qmd.QueryRequest) ([]qmd.Result, error) {
		return []qmd.Result{{File: "qmd://demo/README.md", Title: "<b>t</b>", Snippet: "1: <script>alert('deploy')</script>"}}, nil
	}}
	s := newTestServer(t, q, searchLibrary(t))
	_, body := get(t, s, "/search/results?q=deploy&c=demo&f=1")
	mustNotContain(t, body, "<script>alert", "<b>t</b>")
	mustContain(t, body, "&lt;script&gt;alert(&#39;<mark>deploy</mark>&#39;)&lt;/script&gt;")
}

func TestQueryTermsAndHighlight(t *testing.T) {
	if got := queryTerms(`How do I "deploy" the API? a`); !reflect.DeepEqual(got, []string{"deploy", "how", "the", "api", "do"}) {
		t.Errorf("queryTerms = %q", got)
	}
	got := string(highlight("Deploy the API & roll back", []string{"deploy", "api"}))
	if got != "<mark>Deploy</mark> the <mark>API</mark> &amp; roll back" {
		t.Errorf("highlight = %q", got)
	}
}

func TestSearchFormURLs(t *testing.T) {
	f := searchForm{Query: "a b", Mode: "smart", Limit: 10, Collections: []collectionOption{{Name: "x y", Selected: true}, {Name: "z"}}}
	u, _ := url.Parse(f.PageURL())
	if u.Path != "/search" || u.Query().Get("q") != "a b" || u.Query()["c"][0] != "x y" || len(u.Query()["c"]) != 1 {
		t.Errorf("PageURL = %s", f.PageURL())
	}
	if !strings.HasPrefix(f.ResultsURL(), "/search/results?") {
		t.Errorf("ResultsURL = %s", f.ResultsURL())
	}
}

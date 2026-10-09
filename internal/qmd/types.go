// Package qmd is the HTTP client for a running QMD server (GET /health, POST /query).
//
// Response fields observed in real QMD responses (captured 2026-10-09, see testdata/qmd_*.json):
//
//	GET /health -> {"status":"ok","uptime":9}
//	  status  string  "ok"
//	  uptime  number  seconds since the server started
//
//	POST /query -> {"results":[ ... ]}   (empty: {"results":[]})
//	  docid    string  short id with leading '#', e.g. "#d990bb"
//	  file     string  "qmd://<collection>/<relative/path>", e.g. "qmd://demo/work/deploy/guide.md"
//	  title    string  QMD's own title: the first "# H1", NOT the frontmatter title
//	                   (guide.md has frontmatter "Deploy guide" but QMD reports "How to deploy the API")
//	  score    number  0..1, results sorted by score descending
//	  context  string  all matching context descriptions, outermost first, joined by "\n\n"
//	  line     number  1-based line where the snippet starts
//	  snippet  string  over HTTP every line is prefixed with "<n>: "; the first line is a
//	                   diff-style hunk header like "@@ -1,4 @@ (0 before, 8 after)".
//	                   (`qmd query --json` returns the same snippet without the "<n>: " prefixes.)
//
// No other fields were present. Smart (lex+vec, rerank) and Fast (lex) return the same shape;
// Smart also returns vector-only matches.
package qmd

import (
	"regexp"
	"strings"
)

// Health is the body of GET /health.
type Health struct {
	Status string  `json:"status"`
	Uptime float64 `json:"uptime"` // seconds
}

// SearchType selects how one sub-search runs.
type SearchType string

const (
	Lex  SearchType = "lex"  // BM25 keywords
	Vec  SearchType = "vec"  // semantic vectors
	HyDE SearchType = "hyde" // hypothetical document
)

// Search is one entry of QueryRequest.Searches.
type Search struct {
	Type  SearchType `json:"type"`
	Query string     `json:"query"`
}

// QueryRequest is the body of POST /query. The first search gets 2x weight.
type QueryRequest struct {
	Searches       []Search `json:"searches"`
	Collections    []string `json:"collections,omitempty"` // plural only; QMD ignores "collection"
	Limit          int      `json:"limit,omitempty"`
	Rerank         bool     `json:"rerank"` // QMD defaults to true when omitted, so always send it
	Intent         string   `json:"intent,omitempty"`
	MinScore       float64  `json:"minScore,omitempty"`
	CandidateLimit int      `json:"candidateLimit,omitempty"`
}

// MaxSearches is the most sub-searches QMD accepts in one request.
const MaxSearches = 10

// QueryResponse is the body returned by POST /query.
type QueryResponse struct {
	Results []Result `json:"results"`
}

// Result is one search hit.
type Result struct {
	DocID   string  `json:"docid"`
	File    string  `json:"file"` // qmd://<collection>/<rel/path>
	Title   string  `json:"title"`
	Score   float64 `json:"score"`
	Context string  `json:"context"`
	Line    int     `json:"line"`
	Snippet string  `json:"snippet"`
}

var (
	linePrefix = regexp.MustCompile(`^\d+:( |$)`)
	hunkHeader = regexp.MustCompile(`^@@ .* @@`)
)

// CleanSnippet returns the snippet text without QMD's "<n>: " line prefixes
// and without the leading "@@ ... @@" hunk header line. Prefixes are only
// removed when every line has one, so content like "2026: year" survives
// in unprefixed snippets.
func (r Result) CleanSnippet() string {
	lines := strings.Split(r.Snippet, "\n")
	prefixed := true
	for _, l := range lines {
		if !linePrefix.MatchString(l) {
			prefixed = false
			break
		}
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if prefixed {
			l = linePrefix.ReplaceAllString(l, "")
		}
		if i == 0 && hunkHeader.MatchString(l) {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// FastQuery builds a keyword-only request without reranking.
func FastQuery(q string, collections []string, limit int) QueryRequest {
	return QueryRequest{
		Searches:    []Search{{Type: Lex, Query: q}},
		Collections: collections,
		Limit:       limit,
		Rerank:      false,
	}
}

// SmartQuery builds a hybrid keyword + semantic request with reranking.
func SmartQuery(q string, collections []string, limit int) QueryRequest {
	return QueryRequest{
		Searches:    []Search{{Type: Lex, Query: q}, {Type: Vec, Query: q}},
		Collections: collections,
		Limit:       limit,
		Rerank:      true,
	}
}

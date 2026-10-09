package server

import (
	"context"
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/4rji/lantern/internal/library"
	"github.com/4rji/lantern/internal/qmd"
	"github.com/4rji/lantern/internal/qmdconfig"
)

// Search limits offered in the UI.
var searchLimits = []int{10, 20, 50}

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
	maxQueryLength     = 500
)

// querier is the part of the QMD client that search needs.
type querier interface {
	Query(ctx context.Context, q qmd.QueryRequest) ([]qmd.Result, error)
}

// searchForm is the state of the search controls, parsed from the URL.
type searchForm struct {
	Query       string
	Mode        string // "fast" | "smart"
	Limit       int
	Collections []collectionOption
	Submitted   bool // the form was sent at least once (distinguishes "none checked" from "fresh")
}

type collectionOption struct {
	Name     string
	Selected bool
	Included bool // includeByDefault
}

// Selected returns the names of the chosen collections.
func (f searchForm) Selected() []string {
	var out []string
	for _, c := range f.Collections {
		if c.Selected {
			out = append(out, c.Name)
		}
	}
	return out
}

// Smart reports whether Smart mode is chosen.
func (f searchForm) Smart() bool { return f.Mode == "smart" }

// Limits lists the result-count choices.
func (f searchForm) Limits() []int { return searchLimits }

// ResultsURL is the partial endpoint for the current form state.
func (f searchForm) ResultsURL() string { return "/search/results?" + f.values().Encode() }

// PageURL is the shareable /search URL for the current form state.
func (f searchForm) PageURL() string { return "/search?" + f.values().Encode() }

func (f searchForm) values() url.Values {
	v := url.Values{}
	v.Set("q", f.Query)
	v.Set("mode", f.Mode)
	v.Set("limit", strconv.Itoa(f.Limit))
	for _, name := range f.Selected() {
		v.Add("c", name)
	}
	v.Set("f", "1")
	return v
}

func parseSearchForm(r *http.Request, cfg *qmdconfig.Config) searchForm {
	q := r.URL.Query()
	f := searchForm{
		Query:     strings.TrimSpace(q.Get("q")),
		Mode:      "fast",
		Limit:     defaultSearchLimit,
		Submitted: q.Get("f") == "1",
	}
	if len(f.Query) > maxQueryLength {
		f.Query = f.Query[:maxQueryLength]
	}
	if q.Get("mode") == "smart" {
		f.Mode = "smart"
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = min(n, maxSearchLimit)
	}
	chosen := map[string]bool{}
	for _, c := range q["c"] {
		chosen[c] = true
	}
	if cfg != nil {
		for _, c := range cfg.Sorted() {
			opt := collectionOption{Name: c.Name, Included: c.Included()}
			if f.Submitted || len(chosen) > 0 {
				opt.Selected = chosen[c.Name]
			} else {
				opt.Selected = c.Included()
			}
			f.Collections = append(f.Collections, opt)
		}
	}
	return f
}

// searchHit is one result as shown in the UI.
type searchHit struct {
	Title      string
	Collection string
	Path       string
	URL        string // empty when the file is not on this computer
	Score      float64
	ScorePct   int
	Snippet    template.HTML
	Line       int
	DocID      string
}

// searchResults is the data for the results partial.
type searchResults struct {
	Form    searchForm
	Hits    []searchHit
	Elapsed time.Duration
	// Exactly one of these states applies when Hits is empty.
	Idle       bool   // nothing typed yet
	NoneChosen bool   // no collection selected
	NoConfig   bool   // QMD's config could not be read
	ErrKind    string // "down" | "timeout" | "error"
	ErrMessage string
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	p := s.newPage(r, "Search", "search", nil)
	p.Data = parseSearchForm(r, p.Config)
	s.render(w, r, http.StatusOK, "search", p)
}

func (s *Server) handleSearchResults(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.configs.Load() // the layout banner reports config problems
	form := parseSearchForm(r, cfg)
	res := s.runSearch(r.Context(), cfg, form)
	if r.Context().Err() != nil {
		return // the browser replaced or aborted this request
	}
	// Keep the address bar shareable without adding a history entry per keystroke.
	w.Header().Set("HX-Replace-Url", form.PageURL())
	s.renderPartial(w, r, "search", "search-results", res)
}

func (s *Server) runSearch(ctx context.Context, cfg *qmdconfig.Config, form searchForm) searchResults {
	res := searchResults{Form: form}
	switch {
	case form.Query == "":
		res.Idle = true
		return res
	case cfg == nil:
		res.NoConfig = true
		return res
	case len(form.Selected()) == 0:
		res.NoneChosen = true
		return res
	}

	req := qmd.FastQuery(form.Query, form.Selected(), form.Limit)
	if form.Smart() {
		req = qmd.SmartQuery(form.Query, form.Selected(), form.Limit)
	}
	start := time.Now()
	results, err := s.query.Query(ctx, req)
	res.Elapsed = time.Since(start)
	if err != nil {
		switch {
		case errors.Is(err, qmd.ErrUnavailable):
			res.ErrKind = "down"
		case errors.Is(err, qmd.ErrTimeout):
			res.ErrKind = "timeout"
		default:
			res.ErrKind = "error"
			res.ErrMessage = err.Error()
		}
		return res
	}

	terms := queryTerms(form.Query)
	for _, r := range results {
		res.Hits = append(res.Hits, toHit(cfg, r, terms))
	}
	return res
}

func toHit(cfg *qmdconfig.Config, r qmd.Result, terms []string) searchHit {
	h := searchHit{
		Title:    r.Title,
		Score:    r.Score,
		ScorePct: int(min(max(r.Score, 0), 1)*100 + 0.5),
		Snippet:  highlight(r.CleanSnippet(), terms),
		Line:     r.Line,
		DocID:    r.DocID,
	}
	if col, rel, err := library.ParseURI(r.File); err == nil {
		h.Collection, h.Path = col, rel
	} else {
		h.Path = r.File
	}
	if c, rel, abs, err := library.Resolve(cfg, r.File); err == nil {
		h.Path = rel
		if _, err := os.Stat(abs); err == nil {
			h.URL = library.WebPath(c.Name, rel, false)
		}
	}
	if h.Title == "" {
		h.Title = library.FallbackTitle(path.Base(h.Path))
	}
	return h
}

var termSplit = regexp.MustCompile(`[\s"'()\[\]{}:,;!?]+`)

// queryTerms extracts words worth highlighting (2+ letters), longest first.
func queryTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range termSplit.Split(strings.ToLower(q), -1) {
		t = strings.Trim(t, ".-*+")
		if len([]rune(t)) < 2 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// highlight escapes text and wraps case-insensitive matches of terms in <mark>.
func highlight(text string, terms []string) template.HTML {
	if len(terms) == 0 || text == "" {
		return template.HTML(html.EscapeString(text))
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = regexp.QuoteMeta(t)
	}
	re, err := regexp.Compile(`(?i)` + strings.Join(quoted, "|"))
	if err != nil {
		return template.HTML(html.EscapeString(text))
	}
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(text, -1) {
		b.WriteString(html.EscapeString(text[last:m[0]]))
		b.WriteString("<mark>")
		b.WriteString(html.EscapeString(text[m[0]:m[1]]))
		b.WriteString("</mark>")
		last = m[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	return template.HTML(b.String())
}

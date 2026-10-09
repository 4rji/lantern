package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/4rji/lantern/internal/ai"
	"github.com/4rji/lantern/internal/library"
	"github.com/4rji/lantern/internal/render"
)

const (
	// maxAskBody caps the JSON body of POST /api/ask.
	maxAskBody = 64 << 10
	// maxQuestionLength caps the question, in characters.
	maxQuestionLength = 4000
)

type providerOption struct {
	ID          string
	DisplayName string
	Configured  bool
	Model       string
	KeyVar      string
	Selected    bool
}

type askPageData struct {
	Providers   []providerOption
	Collections []collectionOption
}

func (s *Server) handleAskPage(w http.ResponseWriter, r *http.Request) {
	p := s.newPage(r, "Ask AI", "ask", nil)
	data := askPageData{}
	selected := ""
	for _, pr := range s.opts.Providers {
		if pr.Configured() {
			selected = pr.ID()
			break
		}
	}
	for i, pr := range s.opts.Providers {
		if selected == "" && i == 0 {
			selected = pr.ID()
		}
		data.Providers = append(data.Providers, providerOption{
			ID:          pr.ID(),
			DisplayName: pr.DisplayName(),
			Configured:  pr.Configured(),
			Model:       pr.Model(),
			KeyVar:      providerVars[pr.ID()][0],
			Selected:    pr.ID() == selected,
		})
	}
	if p.Config != nil {
		for _, c := range p.Config.Sorted() {
			data.Collections = append(data.Collections, collectionOption{Name: c.Name, Included: c.Included(), Selected: c.Included()})
		}
	}
	p.Data = data
	s.render(w, r, http.StatusOK, "ask", p)
}

// askRequest is the JSON body of POST /api/ask.
type askRequest struct {
	Provider    string   `json:"provider"`
	Question    string   `json:"question"`
	Collections []string `json:"collections"`
}

// askSource is a document an answer is based on.
type askSource struct {
	Collection string `json:"collection"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

// askResult is the successful answer, as JSON or as the SSE "answer" event.
type askResult struct {
	Provider       string      `json:"provider"`
	Model          string      `json:"model"`
	AnswerMarkdown string      `json:"answer_markdown"`
	AnswerHTML     string      `json:"answer_html"`
	Sources        []askSource `json:"sources"`
	Elapsed        string      `json:"elapsed"`
}

// handleAPIAsk answers a question with the chosen provider. With
// "Accept: text/event-stream" it streams progress events, then the answer;
// otherwise it returns one JSON object.
func (s *Server) handleAPIAsk(w http.ResponseWriter, r *http.Request) {
	// Only same-origin JSON requests: a cross-site page can't send JSON without a
	// CORS preflight, and its Origin header won't match. This keeps other sites
	// from spending the user's API credits.
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "expected application/json")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
			writeJSONError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
	}
	var req askRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAskBody)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Provider == "" && len(s.opts.Providers) > 0 {
		req.Provider = s.opts.Providers[0].ID()
	}
	p := ai.Find(s.opts.Providers, req.Provider)
	if p == nil {
		writeJSONError(w, http.StatusBadRequest, "unknown provider")
		return
	}
	question := strings.TrimSpace(req.Question)
	switch {
	case question == "":
		writeJSONError(w, http.StatusBadRequest, "Type a question first.")
		return
	case len([]rune(question)) > maxQuestionLength:
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("The question is too long (max %d characters).", maxQuestionLength))
		return
	case !p.Configured():
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("%s has no API key. Set %s and restart lantern.", p.DisplayName(), providerVars[p.ID()][0]))
		return
	}
	cfg, err := s.configs.Load()
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "lantern can't read QMD's config: "+err.Error())
		return
	}
	var allowed []string
	for _, name := range req.Collections {
		if cfg.Collections[name] != nil && !slices.Contains(allowed, name) {
			allowed = append(allowed, name)
		}
	}
	if len(allowed) == 0 {
		writeJSONError(w, http.StatusBadRequest, "Pick at least one collection.")
		return
	}
	if st := s.status.Get(r.Context()); !st.Up {
		writeJSONError(w, http.StatusServiceUnavailable, "QMD is not running. Start it with `qmd mcp --http --daemon`, then ask again.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), ai.AskTimeout)
	defer cancel()

	var events *sseWriter
	progress := func(string) {}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		events = newSSEWriter(w)
		progress = func(msg string) { events.send("progress", map[string]string{"message": msg}) }
	}

	start := time.Now()
	tools := ai.NewToolbox(s.query.Query, cfg, allowed, progress)
	resp, err := p.Ask(ctx, ai.AskRequest{Question: question, Collections: allowed, Tools: tools})
	if r.Context().Err() != nil {
		return // the browser stopped waiting
	}
	if err != nil {
		code, msg := askErrorMessage(p, err)
		if events != nil {
			events.send("error", map[string]string{"message": msg})
		} else {
			writeJSONError(w, code, msg)
		}
		return
	}

	result := s.askResult(p, resp, time.Since(start))
	if events != nil {
		events.send("answer", result)
	} else {
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) askResult(p ai.Provider, resp ai.AskResponse, elapsed time.Duration) askResult {
	res := askResult{
		Provider:       p.DisplayName(),
		Model:          p.Model(),
		AnswerMarkdown: resp.AnswerMarkdown,
		Sources:        []askSource{},
		Elapsed:        formatSeconds(elapsed),
	}
	for _, src := range resp.Sources {
		res.Sources = append(res.Sources, askSource{
			Collection: src.Collection,
			Path:       src.Path,
			Title:      src.Title,
			URL:        library.WebPath(src.Collection, src.Path, false),
		})
	}
	md := linkCitations(resp.AnswerMarkdown, res.Sources)
	if doc, err := s.renderer.Render([]byte(md), render.Location{}); err == nil {
		res.AnswerHTML = string(doc.HTML)
	}
	return res
}

// linkCitations turns `collection/path` citations of known sources into links
// to their Document pages.
func linkCitations(md string, sources []askSource) string {
	for _, src := range sources {
		cite := "`" + src.Collection + "/" + src.Path + "`"
		link := "[" + cite + "](" + src.URL + ")"
		var b strings.Builder
		rest := md
		for {
			i := strings.Index(rest, cite)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:i])
			if i > 0 && rest[i-1] == '[' { // already a link
				b.WriteString(cite)
			} else {
				b.WriteString(link)
			}
			rest = rest[i+len(cite):]
		}
		md = b.String()
	}
	return md
}

// askErrorMessage turns an Ask error into an HTTP status and a message for the user.
func askErrorMessage(p ai.Provider, err error) (int, string) {
	var refusal *ai.RefusalError
	var perr *ai.ProviderError
	switch {
	case errors.As(err, &refusal):
		return http.StatusUnprocessableEntity, refusal.Error() + ". Try rephrasing the question or another provider."
	case errors.As(err, &perr):
		return http.StatusBadGateway, perr.Error()
	case errors.Is(err, ai.ErrNoKey):
		return http.StatusBadRequest, fmt.Sprintf("%s has no API key. Set %s and restart lantern.", p.DisplayName(), providerVars[p.ID()][0])
	case errors.Is(err, ai.ErrTimeout):
		return http.StatusGatewayTimeout, err.Error()
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

// sseWriter writes Server-Sent Events. Progress arrives from tool goroutines,
// so writes are serialized.
type sseWriter struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newSSEWriter(w http.ResponseWriter) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, rc: http.NewResponseController(w)}
	s.rc.Flush()
	return s
}

func (s *sseWriter) send(event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	s.rc.Flush()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

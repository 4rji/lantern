package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/4rji/lantern/internal/ai"
)

// maxAskBody caps the JSON body of POST /api/ask.
const maxAskBody = 64 << 10

type providerOption struct {
	ID          string
	DisplayName string
	Configured  bool
	Model       string
	Selected    bool
}

type askPageData struct {
	Providers   []providerOption
	Collections []collectionOption
	// Enabled is false until AI answers are implemented.
	Enabled bool
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

// handleAPIAsk answers a question with the chosen provider. Until Phase 6 every
// provider returns ai.ErrNotImplemented, which maps to 501.
func (s *Server) handleAPIAsk(w http.ResponseWriter, r *http.Request) {
	// Only same-origin JSON requests: a cross-site page can't send JSON without a
	// CORS preflight, and its Origin header won't match. This keeps other sites
	// from spending the user's API credits once answers exist.
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
	resp, err := p.Ask(r.Context(), ai.AskRequest{Question: req.Question, Collections: req.Collections})
	if errors.Is(err, ai.ErrNotImplemented) {
		writeJSONError(w, http.StatusNotImplemented, "not implemented")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

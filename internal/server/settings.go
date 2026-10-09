package server

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/4rji/lantern/internal/ai"
	"github.com/4rji/lantern/internal/library"
)

type settingsCollection struct {
	Name     string
	URL      string
	Path     string
	Pattern  string
	Ignore   string
	Included bool
	Context  string
	DocCount int
	Err      string
}

type settingsProvider struct {
	DisplayName string
	Configured  bool
	MaskedKey   string
	KeyVar      string
	Model       string
	ModelVar    string
	Effort      string
	EffortVar   string
}

type settingsData struct {
	Collections []settingsCollection
	Providers   []settingsProvider
	EditorURI   string
	Platform    string
	GoVersion   string
}

// providerVars are each provider's key, model and effort environment variables.
var providerVars = map[string][3]string{
	"claude": {ai.EnvAnthropicKey, ai.EnvClaudeModel, ai.EnvClaudeEffort},
	"openai": {ai.EnvOpenAIKey, ai.EnvOpenAIModel, ai.EnvOpenAIEffort},
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	p := s.newPage(r, "Settings", "settings", nil)
	data := settingsData{
		EditorURI: s.editorTemplate(p.Config),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion: runtime.Version(),
	}
	if p.Config != nil {
		for _, c := range p.Config.Sorted() {
			sc := settingsCollection{
				Name:     c.Name,
				URL:      library.WebPath(c.Name, "", true),
				Path:     c.Path,
				Pattern:  c.Pattern,
				Ignore:   strings.Join(c.Ignore, ", "),
				Included: c.Included(),
				Context:  c.RootContext(),
			}
			docs, err := s.docs.Docs(c)
			if err != nil {
				sc.Err = friendlyWalkError(err)
			}
			sc.DocCount = len(docs)
			data.Collections = append(data.Collections, sc)
		}
	}
	for _, pr := range s.opts.Providers {
		sp := settingsProvider{
			DisplayName: pr.DisplayName(),
			Configured:  pr.Configured(),
			Model:       pr.Model(),
			KeyVar:      providerVars[pr.ID()][0],
			ModelVar:    providerVars[pr.ID()][1],
			EffortVar:   providerVars[pr.ID()][2],
		}
		if e, ok := pr.(interface{ Effort() string }); ok {
			sp.Effort = e.Effort()
		}
		if h, ok := pr.(ai.KeyHinter); ok {
			sp.MaskedKey = h.MaskedKey()
		}
		data.Providers = append(data.Providers, sp)
	}
	p.Data = data
	s.render(w, r, http.StatusOK, "settings", p)
}

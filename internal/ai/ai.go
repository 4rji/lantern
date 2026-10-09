// Package ai defines the AI provider interface (Claude, OpenAI).
//
// Both providers run the same tool loop: the model searches and reads the
// user's documents through a Toolbox, then answers citing collection/path.
package ai

import (
	"context"
	"strings"
)

// AskRequest is a question about the user's documents.
type AskRequest struct {
	Question    string
	Collections []string
	// Tools runs search_documents, read_document and list_collections for this
	// question, limited to Collections. Providers only translate tool calls.
	Tools *Toolbox
}

// Source is a document the answer is based on.
type Source struct {
	Collection string
	Path       string // relative path inside the collection
	Title      string
}

// AskResponse is an answer plus the documents it cites.
type AskResponse struct {
	AnswerMarkdown string
	Sources        []Source
}

// Provider answers questions using an LLM API.
type Provider interface {
	ID() string          // "claude" | "openai"
	DisplayName() string // "Claude" | "OpenAI"
	Configured() bool    // API key env var present
	Model() string
	Ask(ctx context.Context, req AskRequest) (AskResponse, error)
}

// KeyHinter is implemented by providers that can show which key they use,
// masked so only the last characters are visible.
type KeyHinter interface {
	MaskedKey() string
}

// Env abstracts environment lookups so providers are testable.
type Env func(key string) string

// Environment variables. App-specific names keep a global ANTHROPIC_API_KEY or
// OPENAI_API_KEY (used by other tools) from being picked up by accident.
const (
	EnvAnthropicKey = "LANTERN_ANTHROPIC_API_KEY"
	EnvOpenAIKey    = "LANTERN_OPENAI_API_KEY"
	EnvClaudeModel  = "LANTERN_CLAUDE_MODEL"
	EnvOpenAIModel  = "LANTERN_OPENAI_MODEL"
)

// DefaultClaudeModel is used when LANTERN_CLAUDE_MODEL is not set.
const DefaultClaudeModel = "claude-opus-5-5"

// Providers returns the available providers, in display order.
func Providers(env Env) []Provider {
	return []Provider{NewClaude(env), NewOpenAI(env)}
}

// Find returns the provider with the given ID, or nil.
func Find(providers []Provider, id string) Provider {
	for _, p := range providers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// envProvider holds what both providers read from the environment.
type envProvider struct {
	key   string
	model string
}

func newEnvProvider(env Env, keyVar, modelVar, defaultModel string) envProvider {
	model := strings.TrimSpace(env(modelVar))
	if model == "" {
		model = defaultModel
	}
	return envProvider{key: strings.TrimSpace(env(keyVar)), model: model}
}

func (p envProvider) Configured() bool { return p.key != "" }
func (p envProvider) Model() string    { return p.model }

// MaskedKey shows only the last 4 characters, or nothing for short keys.
func (p envProvider) MaskedKey() string {
	if p.key == "" {
		return ""
	}
	if len(p.key) < 12 {
		return "••••"
	}
	return "••••" + p.key[len(p.key)-4:]
}

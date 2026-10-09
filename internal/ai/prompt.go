package ai

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Limits shared by every provider.
const (
	// MaxToolRounds caps how many times the model may call tools for one question.
	MaxToolRounds = 8
	// AskTimeout bounds one whole question, tool calls included.
	AskTimeout = 5 * time.Minute
	// maxOutputTokens caps each model response.
	maxOutputTokens = 16000
)

// systemPrompt is the same for every question, so providers can cache it.
const systemPrompt = `You answer questions using the user's own documents: notes and markdown files indexed by QMD on their computer.

Use the tools to find and read what is relevant before answering. search_documents finds documents; its snippets are short, so read the most relevant documents with read_document before relying on them. list_collections describes the collections you may use.

Answer only from what the documents say. If they don't contain the answer, say so plainly and mention what you searched for; don't fill the gap with outside knowledge. Cite each document you rely on as collection/path in backticks (for example ` + "`notes/work/deploy.md`" + `), next to the claim it supports. Use only the collections listed in the user's message.

Write the answer in Markdown, in the language of the question.`

// userPrompt states which collections may be used, then the question.
func userPrompt(req AskRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Collections you may use: %s.\n\nQuestion:\n%s", strings.Join(req.Tools.Allowed(), ", "), strings.TrimSpace(req.Question))
	return b.String()
}

// Errors returned by Ask.
var (
	// ErrNoKey means the provider's API key environment variable is not set.
	ErrNoKey = errors.New("API key not set")
	// ErrTooManySteps means the model kept calling tools past MaxToolRounds.
	ErrTooManySteps = fmt.Errorf("the AI used more than %d search and read steps without finishing an answer; try a narrower question", MaxToolRounds)
	// ErrTimeout means the question took longer than AskTimeout.
	ErrTimeout = fmt.Errorf("no answer within %s", AskTimeout)
)

// RefusalError means the provider declined to answer.
type RefusalError struct {
	Provider string
	Category string // provider-specific, may be empty
}

func (e *RefusalError) Error() string {
	if e.Category != "" {
		return fmt.Sprintf("%s declined to answer (%s)", e.Provider, e.Category)
	}
	return e.Provider + " declined to answer"
}

// ProviderError is an error reported by the provider's API.
type ProviderError struct {
	Provider string
	Status   int // HTTP status, 0 when the request never got an answer
	Message  string
}

func (e *ProviderError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s API error (HTTP %d): %s", e.Provider, e.Status, e.Message)
	}
	return fmt.Sprintf("%s API error: %s", e.Provider, e.Message)
}

// friendlyStatus explains common HTTP statuses from either provider.
func friendlyStatus(provider, keyVar string, status int, raw string) string {
	switch {
	case status == 401 || status == 403:
		return fmt.Sprintf("the API key was rejected; check %s", keyVar)
	case status == 429:
		return "rate limited or out of credits; wait a moment and try again"
	case status == 529 || status >= 500:
		return provider + " is overloaded or unavailable; try again shortly"
	default:
		return raw
	}
}

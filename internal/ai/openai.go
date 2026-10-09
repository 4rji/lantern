package ai

import "context"

// OpenAI answers with OpenAI's API (implemented in Phase 6).
type OpenAI struct{ envProvider }

// NewOpenAI reads LANTERN_OPENAI_API_KEY and LANTERN_OPENAI_MODEL. The default
// model is chosen in Phase 6 from OpenAI's current docs, so it is empty until then.
func NewOpenAI(env Env) *OpenAI {
	return &OpenAI{newEnvProvider(env, EnvOpenAIKey, EnvOpenAIModel, "")}
}

func (*OpenAI) ID() string          { return "openai" }
func (*OpenAI) DisplayName() string { return "OpenAI" }

// Ask is not implemented yet.
func (*OpenAI) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	return AskResponse{}, ErrNotImplemented
}

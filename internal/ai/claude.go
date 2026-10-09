package ai

import "context"

// Claude answers with Anthropic's API (implemented in Phase 6).
type Claude struct{ envProvider }

// NewClaude reads LANTERN_ANTHROPIC_API_KEY and LANTERN_CLAUDE_MODEL.
func NewClaude(env Env) *Claude {
	return &Claude{newEnvProvider(env, EnvAnthropicKey, EnvClaudeModel, DefaultClaudeModel)}
}

func (*Claude) ID() string          { return "claude" }
func (*Claude) DisplayName() string { return "Claude" }

// Ask is not implemented yet.
func (*Claude) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	return AskResponse{}, ErrNotImplemented
}
